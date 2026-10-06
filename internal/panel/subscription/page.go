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
var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex"><title>{{.Heading}}</title>
<style>
:root{--accent:{{.Accent}};--bg:#0f1215;--card:#181d22;--card2:#1f262c;--ink:#e6efee;--muted:#8a9aa0;--line:#2b343c;--on-accent:#06201e}
.light{--bg:#f4f6f8;--card:#fff;--card2:#f0f3f5;--ink:#1b2128;--muted:#5d6874;--line:#e1e6ea;--on-accent:#fff}
@media (prefers-color-scheme:light){.auto{--bg:#f4f6f8;--card:#fff;--card2:#f0f3f5;--ink:#1b2128;--muted:#5d6874;--line:#e1e6ea;--on-accent:#fff}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--ink);font:15px/1.5 system-ui,-apple-system,Segoe UI,Roboto,sans-serif}
main{max-width:600px;margin:0 auto;padding:28px 16px 40px}
.card{background:var(--card);border:1px solid var(--line);border-radius:14px;padding:18px;margin-bottom:14px}
h1{font-size:24px;margin:0}.muted{color:var(--muted);font-size:14px}.desc{margin-top:6px;color:var(--muted);white-space:pre-line}
.ann{display:block;border-radius:12px;padding:12px 14px;margin-bottom:14px;background:color-mix(in srgb,var(--accent) 16%,transparent);border:1px solid color-mix(in srgb,var(--accent) 45%,transparent);color:var(--ink);text-decoration:none;white-space:pre-line}
.row{display:flex;justify-content:space-between;gap:12px;padding:7px 0;border-bottom:1px solid var(--line)}.row:last-child{border:0}
.ok{color:var(--accent);font-weight:600}.bad{color:#ff5f8f;font-weight:600}
.bar{height:6px;border-radius:6px;background:var(--card2);overflow:hidden;margin-top:6px}.bar i{display:block;height:100%;background:var(--accent)}
.qr{display:block;margin:4px auto 12px;width:200px;height:200px;background:#fff;border-radius:12px;padding:8px}
.copy{display:flex;gap:8px}.copy input{flex:1;min-width:0;padding:10px;border:1px solid var(--line);border-radius:10px;background:var(--card2);color:var(--ink);font:12px ui-monospace,monospace}
button,.btn{display:inline-flex;align-items:center;justify-content:center;gap:6px;padding:10px 14px;border-radius:10px;background:var(--accent);color:var(--on-accent);text-decoration:none;border:0;font:600 14px system-ui,sans-serif;cursor:pointer;white-space:nowrap}
.btn.ghost{background:var(--card2);color:var(--ink)}
h2{font-size:15px;margin:0 0 10px}
.apps{display:grid;grid-template-columns:repeat(auto-fill,minmax(160px,1fr));gap:10px}
.app{background:var(--card2);border:1px solid var(--line);border-radius:12px;padding:12px;display:flex;flex-direction:column;gap:8px}
.app b{font-size:15px}.app .note{font-size:12px;color:var(--muted)}.app .dl{font-size:12px;color:var(--muted)}
.app.rec{border-color:var(--accent)}
details summary{cursor:pointer;color:var(--muted);margin:12px 0 8px}
.steps{white-space:pre-line;color:var(--muted)}
.foot{text-align:center;color:var(--muted);font-size:13px;margin-top:18px;white-space:pre-line}
</style></head><body class="{{.Theme}}"><main>
<div class="card"><h1>{{.Heading}}</h1><div class="muted">{{.Username}}</div>{{if .Description}}<div class="desc">{{.Description}}</div>{{end}}</div>
{{if .Announce}}{{if .AnnounceURL}}<a class="ann" href="{{.AnnounceURL}}">{{.Announce}}</a>{{else}}<div class="ann">{{.Announce}}</div>{{end}}{{end}}
{{if .ShowTraffic}}<div class="card">
<div class="row"><span>Статус</span><span class="{{if .Active}}ok{{else}}bad{{end}}">{{.Status}}</span></div>
<div class="row"><span>Трафик</span><span>{{.Used}} / {{.Limit}}</span></div>{{if .Percent}}<div class="bar"><i style="width:{{.Percent}}%"></i></div>{{end}}
<div class="row"><span>Действует до</span><span>{{.Expire}}{{if .DaysLeft}} · {{.DaysLeft}}{{end}}</span></div></div>{{end}}
<div class="card"><h2>Подключение</h2>
{{if .QR}}<img class="qr" src="data:image/png;base64,{{.QR}}" alt="QR-код подписки">{{end}}
<div class="copy"><input id="u" readonly value="{{.URL}}" onclick="this.select()"><button id="c" type="button">Копировать</button></div></div>
{{if .Apps}}<div class="card"><h2 id="mine-h">Приложения</h2><div class="apps" id="mine"></div>
<details id="rest-box"><summary>Другие приложения</summary><div class="apps" id="rest">
{{range .Apps}}<div class="app{{if .Recommended}} rec{{end}}" data-p="{{.Platforms}}"><b>{{.Name}}</b>{{if .Note}}<span class="note">{{.Note}}</span>{{end}}
<a class="btn" href="{{.Link}}">Добавить</a>{{if .Download}}<a class="dl" href="{{.Download}}" target="_blank" rel="noopener">Скачать приложение ↗</a>{{end}}</div>
{{end}}</div></details></div>{{end}}
{{if .Instructions}}<div class="card"><h2>Как подключиться</h2><div class="steps">{{.Instructions}}</div></div>{{end}}
{{if .SupportURL}}<a class="btn ghost" style="width:100%" href="{{.SupportURL}}">Поддержка</a>{{end}}
{{if .Footer}}<div class="foot">{{.Footer}}</div>{{end}}
</main>
<script>
(function(){
  var b=document.getElementById('c');
  if(b){b.onclick=function(){var i=document.getElementById('u');
    (navigator.clipboard?navigator.clipboard.writeText(i.value):Promise.reject()).catch(function(){i.select();document.execCommand('copy')});
    b.textContent='Скопировано';setTimeout(function(){b.textContent='Копировать'},1500)}}
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
