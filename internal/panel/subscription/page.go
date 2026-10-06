package subscription

import (
	"context"
	"encoding/base64"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

// The page shows the subscription URL (QR and deep links), never raw server links: those would
// bypass the HWID limit (docs/ARCHITECTURE.md §8.4).
var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex"><title>{{.Title}}</title>
<style>
:root{--bg:#f6f7f9;--card:#fff;--ink:#1d2330;--muted:#5b6475;--line:#e4e7ec;--accent:#2f6fed}
@media (prefers-color-scheme:dark){:root{--bg:#14161b;--card:#1d2027;--ink:#e8eaee;--muted:#9aa3b2;--line:#2c313b;--accent:#6c9bff}}
body{margin:0;background:var(--bg);color:var(--ink);font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif}
main{max-width:560px;margin:0 auto;padding:24px 16px}
.card{background:var(--card);border:1px solid var(--line);border-radius:12px;padding:20px;margin-bottom:16px}
h1{font-size:22px;margin:0 0 4px}.muted{color:var(--muted);font-size:14px}
.row{display:flex;justify-content:space-between;padding:6px 0;border-bottom:1px solid var(--line)}.row:last-child{border:0}
.status{font-weight:600}.ok{color:#2f9e44}.bad{color:#e03131}
img{display:block;margin:8px auto;width:220px;height:220px;background:#fff;border-radius:8px}
input{width:100%;box-sizing:border-box;padding:10px;border:1px solid var(--line);border-radius:8px;background:var(--bg);color:var(--ink);font-family:ui-monospace,monospace;font-size:12px}
.btns{display:grid;grid-template-columns:1fr 1fr;gap:8px;margin-top:12px}
a.btn,button{display:block;text-align:center;padding:10px;border-radius:8px;background:var(--accent);color:#fff;text-decoration:none;border:0;font-size:14px;cursor:pointer}
</style></head><body><main>
<div class="card"><h1>{{.Title}}</h1><div class="muted">{{.Username}}</div>
<div class="row"><span>Статус</span><span class="status {{if .Active}}ok{{else}}bad{{end}}">{{.Status}}</span></div>
<div class="row"><span>Трафик</span><span>{{.Used}} / {{.Limit}}</span></div>
<div class="row"><span>Действует до</span><span>{{.Expire}}</span></div></div>
<div class="card"><div class="muted">Ссылка на подписку</div>
<img src="data:image/png;base64,{{.QR}}" alt="QR">
<input id="u" readonly value="{{.URL}}" onclick="this.select()">
<div class="btns">
<button onclick="navigator.clipboard.writeText(document.getElementById('u').value);this.textContent='Скопировано'">Копировать</button>
<a class="btn" href="happ://add/{{.URL}}">Happ</a>
<a class="btn" href="v2raytun://import/{{.URL}}">v2RayTun</a>
<a class="btn" href="hiddify://import/{{.URL}}">Hiddify</a>
<a class="btn" href="streisand://import/{{.URL}}">Streisand</a>
<a class="btn" href="clash://install-config?url={{.URLEnc}}">Clash / Mihomo</a>
</div></div>
<div class="card muted">Установите приложение (Happ, v2RayTun, Hiddify…), нажмите кнопку с его названием или отсканируйте QR-код. Подписка обновляется сама.</div>
</main></body></html>`))

func (h *Handler) page(ctx context.Context, w http.ResponseWriter, r *http.Request, u *store.User) {
	subURL, err := h.svc.SubscriptionURL(ctx, u)
	if err != nil {
		subURL = "https://" + r.Host + r.URL.Path
	}
	png, err := qrcode.Encode(subURL, qrcode.Medium, 512)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	title, _ := h.svc.Setting(ctx, "sub.title", "VPN")
	status := map[string]string{store.StatusActive: "активна", store.StatusExpired: "истекла", store.StatusLimited: "трафик исчерпан", store.StatusDisabled: "отключена"}[u.Status]
	limit, expire := "∞", "бессрочно"
	if u.TrafficLimitBytes != nil {
		limit = human(*u.TrafficLimitBytes)
	}
	if u.ExpireAt != nil {
		expire = time.Unix(*u.ExpireAt, 0).Format("02.01.2006")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = pageTmpl.Execute(w, map[string]any{
		"Title": title, "Username": u.Username, "Status": status, "Active": u.Status == store.StatusActive,
		"Used": human(u.TrafficUsedBytes), "Limit": limit, "Expire": expire,
		"URL": subURL, "URLEnc": url.QueryEscape(subURL), "QR": base64.StdEncoding.EncodeToString(png),
	})
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
