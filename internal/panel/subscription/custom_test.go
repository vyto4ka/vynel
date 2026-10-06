package subscription

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

func TestDefaultHeaders(t *testing.T) {
	e := newEnv(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(e.svc.SetSubBasics(e.ctx, actor, service.SubBasics{Title: "Мику VPN", UpdateHours: 6, SupportURL: "https://t.me/help", Announce: "Привет"}))
	sub := "/s/" + e.user.SubToken

	w := e.get(sub, "Happ/4.1.0", "phone-1")
	h := w.Header()
	if got := h.Get("Profile-Title"); got != "base64:"+base64.StdEncoding.EncodeToString([]byte("Мику VPN")) {
		t.Fatalf("profile-title %q", got)
	}
	if !strings.HasPrefix(h.Get("Subscription-Userinfo"), "upload=0; download=0; total=0; expire=") {
		t.Fatalf("userinfo %q", h.Get("Subscription-Userinfo"))
	}
	if h.Get("Profile-Update-Interval") != "6" || h.Get("Support-Url") != "https://t.me/help" {
		t.Fatalf("interval/support: %v", h)
	}
	if !strings.HasPrefix(h.Get("Announce"), "base64:") || h.Get("Announce-Url") != "" {
		t.Fatalf("announce: %q / %q", h.Get("Announce"), h.Get("Announce-Url"))
	}
	if !strings.HasSuffix(h.Get("Profile-Web-Page-Url"), sub) {
		t.Fatalf("page url %q", h.Get("Profile-Web-Page-Url"))
	}
	if h.Get("Content-Disposition") != "" || h.Get("Cache-Control") != "no-store" {
		t.Fatalf("content-disposition for Happ or cache: %v", h)
	}

	// Clash clients also get the profile name as a file name.
	w = e.get(sub, "clash-verge/v2.0", "pc-1")
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "filename*=UTF-8''%D0%9C%D0%B8%D0%BA%D1%83%20VPN") {
		t.Fatalf("content-disposition %q", cd)
	}
}

func TestCustomHeaders(t *testing.T) {
	e := newEnv(t)
	hs := []service.SubHeader{
		{Name: "Hide-Settings", Value: "1", Clients: `(?i)\bhapp\b`, Enabled: true},
		{Name: "X-Who", Value: "{username} до {expire_date}", Base64: true, Enabled: true},
		{Name: "X-Off", Value: "1", Enabled: false},
		{Name: "Routing", Value: "happ://routing/onadd/…", Enabled: true}, // placeholder left: skipped
	}
	if err := e.svc.SetSubHeaders(e.ctx, actor, hs); err != nil {
		t.Fatal(err)
	}
	sub := "/s/" + e.user.SubToken
	h := e.get(sub, "Happ/4.1.0", "phone-1").Header()
	if h.Get("Hide-Settings") != "1" || h.Get("X-Off") != "" || h.Get("Routing") != "" || h.Get("Subscription-Userinfo") != "" {
		t.Fatalf("happ headers: %v", h)
	}
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(h.Get("X-Who"), "base64:"))
	if !strings.HasPrefix(string(raw), "alice до ") {
		t.Fatalf("x-who %q", raw)
	}
	if h := e.get(sub, "v2RayTun/5", "phone-2").Header(); h.Get("Hide-Settings") != "" {
		t.Fatal("Happ-only header went to v2RayTun")
	}

	for _, bad := range [][]service.SubHeader{
		{{Name: "Bad Name", Value: "x", Enabled: true}},
		{{Name: "Content-Type", Value: "x", Enabled: true}},
		{{Name: "X-A", Value: "x", Clients: "(", Enabled: true}},
		{{Name: "X-A", Value: "1", Enabled: true}, {Name: "x-a", Value: "2", Enabled: true}},
	} {
		if err := e.svc.SetSubHeaders(e.ctx, actor, bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	// nil restores the defaults.
	if err := e.svc.SetSubHeaders(e.ctx, actor, nil); err != nil {
		t.Fatal(err)
	}
	if h := e.get(sub, "Happ/4.1.0", "phone-1").Header(); h.Get("Subscription-Userinfo") == "" {
		t.Fatal("defaults not restored")
	}
}

func TestKeqDroid(t *testing.T) {
	e := newEnv(t)
	sub := "/s/" + e.user.SubToken
	w := e.get(sub, "keqdroid/0.25.1", "keq-phone", "x-device-os", "Android", "x-device-model", "Pixel 8")
	links := decodeB64Links(t, w.Body.String())
	if len(links) == 0 || !strings.HasPrefix(links[0], "vless://") {
		t.Fatalf("keqdroid did not get links: %q", w.Body.String())
	}
	ds, _ := e.svc.Devices(e.ctx, e.user.ID)
	if len(ds) != 1 || ds[0].Model != "Pixel 8" {
		t.Fatalf("device %+v", ds)
	}
	if w.Header().Get("Profile-Title") == "" {
		t.Fatal("no profile-title")
	}
}

func TestPageConfig(t *testing.T) {
	e := newEnv(t)
	p := service.DefaultSubPage()
	p.Heading = "Мой VPN"
	p.Accent = "#ff5fa2"
	p.Theme = "light"
	p.Footer = "Пишите {support_url}"
	p.Apps = append(p.Apps, service.SubApp{ID: "evil", Name: "Evil", Link: "javascript:alert(1)", Enabled: true})
	if err := e.svc.SetSubPage(e.ctx, actor, &p); err == nil {
		t.Fatal("javascript: link accepted")
	}
	p.Apps = p.Apps[:len(p.Apps)-1]
	if err := e.svc.SetSubPage(e.ctx, actor, &p); err != nil {
		t.Fatal(err)
	}
	w := e.get("/s/"+e.user.SubToken, "Mozilla/5.0 (Linux; Android 14)", "", "Accept", "text/html")
	body := w.Body.String()
	for _, want := range []string{"Мой VPN", `--accent:#ff5fa2`, `class="light"`, "keqdroid://install-config?url=https%3A%2F%2Fsub.example.com%2Fs%2F", `href="happ://add/https://sub.example.com/s/`} {
		if !strings.Contains(body, want) {
			t.Fatalf("page lacks %q", want)
		}
	}
	if strings.Contains(body, "ZgotmplZ") {
		t.Fatal("a link was rejected by html/template")
	}
	if strings.Index(body, "KeqDroid") > strings.Index(body, "Happ") {
		t.Fatal("KeqDroid is not first")
	}
}

func decodeB64Links(t *testing.T, body string) []string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body))
	if err != nil {
		t.Fatalf("not base64: %q", body)
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}
