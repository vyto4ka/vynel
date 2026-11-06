package webapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vyto4ka/vynel/internal/panel/bot"
	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/telegramrelay"
)

func TestBotRouteAPIAndConnectionProbe(t *testing.T) {
	c, svc, pw := newServer(t)
	if w := c.do("POST", "/api/bot/test", `{}`, true); w.Code != 401 {
		t.Fatalf("auth: %d", w.Code)
	}
	if w := c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	n, _, err := svc.CreateNode(t.Context(), service.ActorCLI, service.NodeInput{Name: "Overseas", Country: "DE"})
	if err != nil {
		t.Fatal(err)
	}
	if w := c.do("PUT", "/api/bot", `{"nodeId":`+itoa(n.ID)+`}`, true); w.Code != 400 {
		t.Fatalf("old agent: %d %s", w.Code, w.Body.String())
	}
	if err := store.SetNodeFacts(t.Context(), svc.Store().DB, n.ID, store.NodeFacts{Capabilities: []string{telegramrelay.Capability}}); err != nil {
		t.Fatal(err)
	}
	secret := "123456789:secret-only-on-the-panel-telegram"
	if err := svc.SetBotToken(t.Context(), service.ActorCLI, secret); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method == http.MethodHead && r.URL.Path == "/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != "/bot"+secret+"/getMe" {
			t.Errorf("probe called %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"username":"probe_bot"}}`))
	}))
	defer api.Close()
	c.srv.cfg.Bot = bot.New(bot.Config{Service: svc, API: api.URL})
	if w := c.do("PUT", "/api/bot", `{"nodeId":`+itoa(n.ID)+`}`, false); w.Code != 403 {
		t.Fatalf("save csrf: %d", w.Code)
	}
	w := c.do("PUT", "/api/bot", `{"nodeId":`+itoa(n.ID)+`}`, true)
	if w.Code != 200 || strings.Contains(w.Body.String(), secret) {
		t.Fatalf("save leaked token: %d %s", w.Code, w.Body.String())
	}
	var info struct {
		NodeID     int64 `json:"nodeId"`
		RouteNodes []struct {
			ID        int64 `json:"id"`
			Supported bool  `json:"supported"`
		} `json:"routeNodes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil || info.NodeID != n.ID || len(info.RouteNodes) != 1 || !info.RouteNodes[0].Supported {
		t.Fatalf("info: %s %v", w.Body.String(), err)
	}
	if w := c.do("POST", "/api/bot/test", `{"nodeId":0}`, false); w.Code != 403 {
		t.Fatalf("probe csrf: %d", w.Code)
	}
	w = c.do("POST", "/api/bot/test", `{"nodeId":0}`, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) || !strings.Contains(w.Body.String(), "probe_bot") {
		t.Fatalf("probe: %d %s", w.Code, w.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("probe made %d API calls", calls.Load())
	}
	if id, _ := svc.BotNodeID(t.Context()); id != n.ID {
		t.Fatal("test applied a draft route")
	}
	// Unrelated bot form saves preserve the selected route and token.
	if w := c.do("PUT", "/api/bot", `{"alerts":false}`, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if id, _ := svc.BotNodeID(t.Context()); id != n.ID || svc.BotToken(t.Context()) != secret {
		t.Fatal("partial update changed route or token")
	}
	if err := svc.SetBotToken(t.Context(), service.ActorCLI, ""); err != nil {
		t.Fatal(err)
	}
	w = c.do("POST", "/api/bot/test", `{"nodeId":0}`, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"networkOnly":true`) || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("tokenless probe: %d %s", w.Code, w.Body.String())
	}
	if id, _ := svc.BotNodeID(t.Context()); id != n.ID {
		t.Fatal("tokenless probe changed saved route")
	}
}
