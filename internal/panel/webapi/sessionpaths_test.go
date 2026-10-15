package webapi

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

// Link mode with a random path per session: the link opens its own path, the session works
// there only, and anything unknown on the domain is the decoy.
func TestSessionPaths(t *testing.T) {
	c, svc, _ := newServer(t)
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(svc.SetSetting(ctx, service.ActorCLI, service.SettingSubDomain, "panel.example.com"))
	if err := svc.SetSessionPaths(ctx, service.ActorCLI, true); err == nil {
		t.Fatal("session paths need link mode")
	}
	must(svc.SetLoginMode(ctx, service.ActorCLI, service.ModeLink))
	must(svc.SetSessionPaths(ctx, service.ActorCLI, true))

	link, _, err := svc.LoginLink(ctx, service.Actor{Kind: "cli", ID: "root"})
	must(err)
	u, _ := url.Parse(link)
	fixed := c.base + "/"
	if u.Path == fixed || len(u.Path) != 18 || !strings.HasPrefix(u.Fragment, "/magic/") {
		t.Fatalf("link %s", link)
	}
	own := &client{t: t, srv: c.srv, base: strings.TrimSuffix(u.Path, "/")}
	tok := strings.TrimPrefix(u.Fragment, "/magic/")

	// The link's path serves the app before login; unknown paths are the decoy.
	if w := own.do("GET", "/", "", false); w.Code != 200 || !strings.Contains(w.Body.String(), "app") {
		t.Fatalf("own path before login: %d", w.Code)
	}
	stranger := &client{t: t, srv: c.srv, base: "/AbCdEfGhJkMnPqRs"}
	if w := stranger.do("GET", "/", "", false); w.Code != 404 || !strings.Contains(w.Body.String(), `class="brand"`) {
		t.Fatalf("unknown path must be the decoy's 404: %d %.60q", w.Code, w.Body.String())
	}
	root := &client{t: t, srv: c.srv, base: ""}
	if w := root.do("GET", "/pricing", "", false); w.Code != 200 && w.Code != 404 {
		t.Fatalf("decoy pages: %d", w.Code)
	}
	if w := root.do("GET", "/", "", false); w.Code != 200 || !strings.Contains(w.Body.String(), `class="brand"`) {
		t.Fatalf("domain root must be the decoy: %d", w.Code)
	}
	// The token opens only its own path.
	if w := c.do("POST", "/api/login/magic", `{"token":"`+tok+`"}`, true); w.Code != 401 {
		t.Fatalf("token used on the fixed path: %d", w.Code)
	}
	link2, _, _ := svc.LoginLink(ctx, service.Actor{Kind: "cli", ID: "root"})
	u2, _ := url.Parse(link2)
	tok2 := strings.TrimPrefix(u2.Fragment, "/magic/")
	if w := own.do("POST", "/api/login/magic", `{"token":"`+tok2+`"}`, true); w.Code != 401 {
		t.Fatalf("token of another path accepted: %d", w.Code)
	}
	if w := own.do("POST", "/api/login/magic", `{"token":"`+tok+`"}`, true); w.Code != 200 || own.cookie == nil || own.cookie.Path != u.Path {
		t.Fatalf("magic login: %d cookie %+v", w.Code, own.cookie)
	}
	if w := own.do("GET", "/api/users", "", false); w.Code != 200 {
		t.Fatalf("session on its path: %d", w.Code)
	}
	// The same cookie on the fixed path is not a session.
	c.cookie = own.cookie
	if w := c.do("GET", "/api/users", "", false); w.Code != 401 {
		t.Fatalf("session used outside its path: %d", w.Code)
	}
	// Password login is off here.
	if w := own.do("POST", "/api/login", `{"login":"boss","password":"x"}`, true); w.Code != 403 {
		t.Fatalf("password login on a session path: %d", w.Code)
	}
	// After logout the path is the decoy again.
	if w := own.do("POST", "/api/logout", "{}", true); w.Code != 200 {
		t.Fatalf("logout: %d", w.Code)
	}
	own.cookie = nil
	if w := own.do("GET", "/", "", false); w.Code != 404 {
		t.Fatalf("ended session's path: %d", w.Code)
	}

	// Mini App only: no links at all.
	bindAdmin(t, svc, 77)
	must(svc.SetLoginMode(ctx, service.ActorCLI, service.ModeMiniApp))
	if _, _, err := svc.LoginLink(ctx, service.Actor{Kind: "cli", ID: "root"}); err == nil {
		t.Fatal("links in miniapp mode")
	}
	if svc.SessionPaths(ctx) {
		t.Fatal("session paths outside link mode")
	}
	if w := c.do("GET", "/api/login/options", "", false); !strings.Contains(w.Body.String(), `"mode":"miniapp"`) || !strings.Contains(w.Body.String(), `"password":false`) {
		t.Fatalf("options: %s", w.Body.String())
	}
}
