package subscription

import (
	"context"
	"encoding/base64"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
)

// The page shows the subscription URL (QR and deep links), never raw server links: those would
// bypass the HWID limit (docs/ARCHITECTURE.md §8.4). Its look comes from service.SubPage.
// The look follows the panel: Material 3 components over Catppuccin-style layered neutrals. The accent
// is the configured color; primary tones are mixed from it so any accent stays readable in both themes.
var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex"><title>{{.Heading}}</title>
<style>
:root{--accent:{{.Accent}}}
body{--bg:#121b1f;--card:#1b272b;--card2:#243137;--card3:#2b383d;--ink:#d1e1e8;--muted:#9eb2ba;--line:#324045;--outline:#65747b;
 --primary:color-mix(in oklab,var(--accent) 72%,#fff);--on-primary:color-mix(in oklab,var(--accent) 22%,#000);
 --tonal:color-mix(in oklab,var(--accent) 22%,var(--card2));--on-tonal:color-mix(in oklab,var(--accent) 18%,#fff);
 --good:var(--primary);--bad:#ff94ba;--bad-bg:color-mix(in oklab,#ff5fa2 22%,var(--card2));--sh:0 1px 3px rgba(0,0,0,.3),0 4px 8px 3px rgba(0,0,0,.15);color-scheme:dark}
body.light{--bg:#e5eaeb;--card:#f6f8f8;--card2:#e9eeef;--card3:#dbe1e3;--ink:#1f3136;--muted:#536569;--line:#cad2d4;--outline:#97a3a6;
 --primary:color-mix(in oklab,var(--accent) 68%,#000);--on-primary:#fff;
 --tonal:color-mix(in oklab,var(--accent) 20%,#fff);--on-tonal:color-mix(in oklab,var(--accent) 45%,#000);
 --bad:#b3245f;--bad-bg:color-mix(in oklab,#ff5fa2 16%,#fff);--sh:0 1px 2px rgba(0,0,0,.12),0 2px 6px 2px rgba(0,0,0,.06);color-scheme:light}
@media (prefers-color-scheme:light){body.auto{--bg:#e5eaeb;--card:#f6f8f8;--card2:#e9eeef;--card3:#dbe1e3;--ink:#1f3136;--muted:#536569;--line:#cad2d4;--outline:#97a3a6;
 --primary:color-mix(in oklab,var(--accent) 68%,#000);--on-primary:#fff;
 --tonal:color-mix(in oklab,var(--accent) 20%,#fff);--on-tonal:color-mix(in oklab,var(--accent) 45%,#000);
 --bad:#b3245f;--bad-bg:color-mix(in oklab,#ff5fa2 16%,#fff);--sh:0 1px 2px rgba(0,0,0,.12),0 2px 6px 2px rgba(0,0,0,.06);color-scheme:light}}
*{box-sizing:border-box}
body{margin:0;min-height:100vh;background:var(--bg);color:var(--ink);font:15px/1.5 "Roboto Flex",Roboto,Inter,system-ui,-apple-system,"Segoe UI",sans-serif;letter-spacing:.1px;-webkit-font-smoothing:antialiased}
main{max-width:600px;margin:0 auto;padding:24px 16px 40px}
svg{width:18px;height:18px;flex:none;fill:none;stroke:currentColor;stroke-width:2;stroke-linecap:round;stroke-linejoin:round}
.card{background:var(--card);border-radius:24px;padding:20px;margin-bottom:12px}
.hero{display:flex;gap:16px;align-items:center;padding:24px 20px;background:linear-gradient(135deg,color-mix(in oklab,var(--accent) 22%,var(--card)) 0%,var(--card) 70%)}
.avatar{width:56px;height:56px;border-radius:18px;flex:none;display:grid;place-items:center;background:var(--primary);color:var(--on-primary);font-size:24px;font-weight:500}
h1{font-size:28px;line-height:36px;font-weight:400;margin:0}
.muted{color:var(--muted);font-size:14px}.desc{color:var(--muted);white-space:pre-line}
h2{font-size:16px;line-height:24px;font-weight:500;margin:0 0 14px;letter-spacing:.15px}
.ann{display:flex;gap:12px;align-items:flex-start;border-radius:16px;padding:14px 16px;margin-bottom:12px;background:var(--tonal);color:var(--on-tonal);text-decoration:none;white-space:pre-line}
a.ann:hover{filter:brightness(1.06)}
.chip{display:inline-flex;align-items:center;gap:6px;height:28px;padding:0 12px;border-radius:8px;font-size:13px;font-weight:500;background:var(--tonal);color:var(--on-tonal)}
.chip:before{content:"";width:7px;height:7px;border-radius:50%;background:var(--primary)}
.chip.bad{background:var(--bad-bg);color:var(--bad)}.chip.bad:before{background:var(--bad)}
.stat{display:flex;justify-content:space-between;align-items:center;gap:12px;padding:10px 0}
.stat+.stat{border-top:1px solid var(--line)}
.stat span:first-child{color:var(--muted)}.stat span:last-child{font-weight:500;text-align:right}
.bar{height:8px;border-radius:4px;background:var(--card3);overflow:hidden;margin:2px 0 8px}.bar i{display:block;height:100%;border-radius:4px;background:var(--primary)}
.qr{display:block;margin:0 auto 16px;width:216px;height:216px;background:#fff;border-radius:20px;padding:10px}
.copy{display:flex;gap:8px;align-items:center;background:var(--card3);border-radius:16px;padding:6px 6px 6px 14px}
.copy input{flex:1;min-width:0;border:0;background:none;color:var(--primary);font:12.5px "JetBrains Mono",ui-monospace,monospace;outline:none;padding:8px 0}
.btn,button{position:relative;display:inline-flex;align-items:center;justify-content:center;gap:8px;height:40px;padding:0 20px;border-radius:999px;border:0;
 background:var(--primary);color:var(--on-primary);text-decoration:none;font:500 14px/1 sans-serif;font-family:inherit;letter-spacing:.1px;cursor:pointer;white-space:nowrap;transition:box-shadow .2s,filter .2s}
.btn:hover,button:hover{box-shadow:var(--sh);filter:brightness(1.05)}
.btn:active,button:active{filter:brightness(.95)}
.btn:focus-visible,button:focus-visible,summary:focus-visible{outline:3px solid var(--primary);outline-offset:2px}
.btn.tonal{background:var(--tonal);color:var(--on-tonal)}
.btn.outlined{background:none;color:var(--primary);box-shadow:inset 0 0 0 1px var(--outline)}
.btn.text{background:none;color:var(--primary);padding:0 12px;height:36px}.btn.text:hover{box-shadow:none;background:color-mix(in srgb,var(--primary) 8%,transparent)}
.btn.wide{display:flex;width:100%;height:48px}
.apps{display:grid;grid-template-columns:repeat(auto-fill,minmax(170px,1fr));gap:10px}
.app{position:relative;background:var(--card2);border-radius:16px;padding:16px;display:flex;flex-direction:column;gap:10px}
.app b{font-size:16px;font-weight:500}.app .note{font-size:12.5px;color:var(--muted);margin-top:-6px}
.app:not(.rec) .btn:not(.text){background:var(--tonal);color:var(--on-tonal)}
.app .dl{align-self:flex-start;margin:-4px 0 -6px -12px}
.app.rec{background:color-mix(in oklab,var(--accent) 12%,var(--card2));box-shadow:inset 0 0 0 1px color-mix(in oklab,var(--primary) 55%,transparent)}
.app.rec b:after{content:"рекомендуем";margin-left:8px;vertical-align:2px;font-size:11px;font-weight:500;padding:2px 8px;border-radius:6px;background:var(--primary);color:var(--on-primary)}
details summary{list-style:none;display:inline-flex;align-items:center;gap:6px;cursor:pointer;color:var(--primary);font-weight:500;font-size:14px;margin:14px 0 4px;padding:8px 12px;border-radius:999px}
details summary::-webkit-details-marker{display:none}
details summary:hover{background:color-mix(in srgb,var(--primary) 8%,transparent)}
details summary svg{transition:transform .2s}details[open] summary svg{transform:rotate(180deg)}
details .apps{margin-top:8px}
.steps{white-space:pre-line;color:var(--muted)}
.foot{text-align:center;color:var(--muted);font-size:13px;margin-top:20px;white-space:pre-line}
@media (prefers-reduced-motion:reduce){*{transition:none!important}}
</style></head><body class="{{.Theme}}"><main>
<div class="card hero"><div class="avatar">{{.Initial}}</div><div><h1>{{.Heading}}</h1><div class="muted">{{.Username}}</div></div></div>
{{if .Description}}<div class="card desc">{{.Description}}</div>{{end}}
{{if .Announce}}{{if .AnnounceURL}}<a class="ann" href="{{.AnnounceURL}}"><svg viewBox="0 0 24 24"><path d="M3 11v2a1 1 0 0 0 1 1h2l5 4V6L6 10H4a1 1 0 0 0-1 1zM15.5 8.5a5 5 0 0 1 0 7"/></svg><span>{{.Announce}}</span></a>{{else}}<div class="ann"><svg viewBox="0 0 24 24"><path d="M3 11v2a1 1 0 0 0 1 1h2l5 4V6L6 10H4a1 1 0 0 0-1 1zM15.5 8.5a5 5 0 0 1 0 7"/></svg><span>{{.Announce}}</span></div>{{end}}{{end}}
{{if .ShowTraffic}}<div class="card">
<div class="stat"><span>Статус</span><span class="chip{{if not .Active}} bad{{end}}">{{.Status}}</span></div>
<div class="stat"><span>Трафик</span><span>{{.Used}} / {{.Limit}}</span></div>{{if .Percent}}<div class="bar"><i style="width:{{.Percent}}%"></i></div>{{end}}
<div class="stat"><span>Действует до</span><span>{{.Expire}}{{if .DaysLeft}} · {{.DaysLeft}}{{end}}</span></div></div>{{end}}
<div class="card"><h2>Подключение</h2>
{{if .QR}}<img class="qr" src="data:image/png;base64,{{.QR}}" alt="QR-код подписки">{{end}}
<div class="copy"><input id="u" readonly value="{{.URL}}" onclick="this.select()"><button id="c" type="button" class="btn tonal"><svg viewBox="0 0 24 24"><rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15V5a2 2 0 0 1 2-2h10"/></svg><span>Копировать</span></button></div></div>
{{if .Apps}}<div class="card"><h2 id="mine-h">Приложения</h2><div class="apps" id="mine"></div>
<details id="rest-box"><summary>Другие приложения <svg viewBox="0 0 24 24"><path d="m6 9 6 6 6-6"/></svg></summary><div class="apps" id="rest">
{{range .Apps}}<div class="app{{if .Recommended}} rec{{end}}" data-p="{{.Platforms}}"><b>{{.Name}}</b>{{if .Note}}<span class="note">{{.Note}}</span>{{end}}
<a class="btn" href="{{.Link}}"><svg viewBox="0 0 24 24"><path d="M12 5v14M5 12h14"/></svg>Добавить</a>{{if .Download}}<a class="btn text dl" href="{{.Download}}" target="_blank" rel="noopener"><svg viewBox="0 0 24 24"><path d="M12 4v11m-5-5 5 5 5-5M5 20h14"/></svg>Скачать</a>{{end}}</div>
{{end}}</div></details></div>{{end}}
{{if .Instructions}}<div class="card"><h2>Как подключиться</h2><div class="steps">{{.Instructions}}</div></div>{{end}}
{{if .SupportURL}}<a class="btn outlined wide" href="{{.SupportURL}}"><svg viewBox="0 0 24 24"><path d="M21 12a8 8 0 0 1-11.6 7.1L4 20l1-4.6A8 8 0 1 1 21 12z"/></svg>Поддержка</a>{{end}}
{{if .Footer}}<div class="foot">{{.Footer}}</div>{{end}}
</main>
<script>
(function(){
  var b=document.getElementById('c');
  if(b){var l=b.querySelector('span');b.onclick=function(){var i=document.getElementById('u');
    (navigator.clipboard?navigator.clipboard.writeText(i.value):Promise.reject()).catch(function(){i.select();document.execCommand('copy')});
    l.textContent='Скопировано';setTimeout(function(){l.textContent='Копировать'},1500)}}
  var ua=navigator.userAgent, p=/android/i.test(ua)?'android':/iphone|ipad|ipod/i.test(ua)?'ios':/mac os/i.test(ua)?'macos':/windows/i.test(ua)?'windows':/linux/i.test(ua)?'linux':'';
  var mine=document.getElementById('mine'), rest=document.getElementById('rest'), box=document.getElementById('rest-box');
  if(!mine)return;
  var tiles=[].slice.call(rest.children), moved=0;
  tiles.forEach(function(t){if(!p||t.getAttribute('data-p').indexOf(p)>=0){mine.appendChild(t);moved++}});
  if(!moved){tiles.forEach(function(t){mine.appendChild(t)})}
  if(!rest.children.length){box.style.display='none'}
  if(p&&moved){document.getElementById('mine-h').textContent='Приложения для вашего устройства'}
})();
</script></body></html>`))

type pageApp struct {
	Name, Note, Platforms string
	Link                  template.URL
	Download              template.URL
	Recommended           bool
}

// PageData is everything the subscription page shows.
type PageData struct {
	Heading, Username, Description, Theme string
	Initial                               string
	Accent                                template.CSS
	Announce                              string
	AnnounceURL                           template.URL
	ShowTraffic                           bool
	Status                                string
	Active                                bool
	Used, Limit, Expire, DaysLeft         string
	Percent                               int
	QR                                    string
	URL                                   string
	Apps                                  []pageApp
	Instructions, Footer                  string
	SupportURL                            template.URL
}

func safeURL(s string) template.URL {
	l := strings.ToLower(strings.TrimSpace(s))
	if l == "" || strings.HasPrefix(l, "javascript:") || strings.HasPrefix(l, "data:") || strings.HasPrefix(l, "vbscript:") {
		return ""
	}
	return template.URL(s) // #nosec G203 -- deep links of app schemes; dangerous schemes are cut above
}

// BuildPage prepares the page for a user with a page config.
func BuildPage(u *store.User, subURL string, b service.SubBasics, p service.SubPage, now time.Time) (PageData, error) {
	v := UserVars(u, subURL, b, now)
	d := PageData{
		Heading: p.Heading, Username: u.Username, Description: v.Expand(p.Description), Theme: p.Theme,
		Accent: template.CSS(p.Accent), Announce: b.Announce, AnnounceURL: safeURL(b.AnnounceURL),
		ShowTraffic: p.ShowTraffic, Status: v["status"], Active: u.Status == store.StatusActive,
		Used: v["used"], Limit: v["limit"], Expire: v["expire_date"], URL: subURL,
		Instructions: v.Expand(p.Instructions), Footer: v.Expand(p.Footer), SupportURL: safeURL(b.SupportURL),
	}
	if d.Heading == "" {
		d.Heading = b.Title
	}
	for _, r := range d.Heading {
		d.Initial = strings.ToUpper(string(r))
		break
	}
	if d.Theme == "" {
		d.Theme = "dark"
	}
	if !accentRe(p.Accent) {
		d.Accent = "#39c5bb"
	}
	if days := v["days_left"]; days != "" {
		d.DaysLeft = "осталось " + days + " дн."
	}
	if u.TrafficLimitBytes != nil && *u.TrafficLimitBytes > 0 {
		d.Percent = int(min(100, 100*u.TrafficUsedBytes / *u.TrafficLimitBytes))
	}
	if p.ShowQR {
		png, err := qrcode.Encode(subURL, qrcode.Medium, 512)
		if err != nil {
			return d, err
		}
		d.QR = base64.StdEncoding.EncodeToString(png)
	}
	first := true
	for _, a := range p.Apps {
		if !a.Enabled {
			continue
		}
		d.Apps = append(d.Apps, pageApp{Name: a.Name, Note: a.Note, Platforms: strings.Join(a.Platforms, " "),
			Link: safeURL(v.Expand(a.Link)), Download: safeURL(a.Download), Recommended: first})
		first = false
	}
	return d, nil
}

func accentRe(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for _, c := range s[1:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}

// RenderPage writes the page.
func RenderPage(w io.Writer, d PageData) error { return pageTmpl.Execute(w, d) }

func (h *Handler) page(ctx context.Context, w http.ResponseWriter, r *http.Request, u *store.User) {
	subURL, err := h.svc.SubscriptionURL(ctx, u)
	if err != nil {
		subURL = "https://" + r.Host + r.URL.Path
	}
	p, err := h.svc.SubPage(ctx)
	if err != nil {
		h.log.Warn("subscription page config", "err", err)
	}
	d, err := BuildPage(u, subURL, h.svc.SubBasicsGet(ctx), p, time.Now())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = RenderPage(w, d)
}

func human(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f ГБ", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.0f МБ", float64(b)/(1<<20))
	}
	return fmt.Sprintf("%d КБ", b>>10)
}
