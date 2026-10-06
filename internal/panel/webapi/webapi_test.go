package webapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
)

type client struct {
	t      *testing.T
	srv    *Server
	base   string
	cookie *http.Cookie
}

func (c *client) do(method, path, body string, csrf bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, c.base+path, strings.NewReader(body))
	if csrf {
		r.Header.Set(csrfHeader, "1")
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
	w = c.do("POST", "/api/subscription/test", `{"userAgent":"clash-verge/2"}`, true)
	if strings.Contains(w.Body.String(), `"Hide-Settings"`) || !strings.Contains(w.Body.String(), `"format":"mihomo"`) {
		t.Fatalf("clash test: %s", w.Body.String())
	}
	if w := c.do("PUT", "/api/subscription", `{"headers":[{"name":"Content-Type","value":"x","enabled":true}]}`, true); w.Code != 400 {
		t.Fatalf("reserved header accepted: %d", w.Code)
	}
	w = c.do("GET", "/api/subscription/preview", "", false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Мику") || w.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Fatalf("preview: %d %q %.100s", w.Code, w.Header().Get("X-Frame-Options"), w.Body.String())
	}
	if w := c.do("POST", "/api/subscription/reset", `{"part":"headers"}`, true); w.Code != 200 || !strings.Contains(w.Body.String(), "Subscription-Userinfo") {
		t.Fatalf("reset: %d", w.Code)
	}
}
