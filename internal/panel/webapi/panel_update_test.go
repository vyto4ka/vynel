package webapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPanelUpdateDoesNotRequireNodeAgent(t *testing.T) {
	for _, target := range []string{"panel", "all"} {
		t.Run(target, func(t *testing.T) {
			c, svc, pw := newServer(t)
			c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true)
			c.srv.upd.checkedAt = time.Now()
			c.srv.upd.latest = "v9.0.0"
			calls := 0
			var dispatchedAt int64
			c.srv.cfg.PanelUpdate = func(context.Context) error {
				calls++
				dispatchedAt = time.Now().Unix()
				return nil
			}
			if c.srv.cfg.NodeAction != nil {
				t.Fatal("test has a node action")
			}
			if w := c.do("POST", "/api/updates/run", `{"target":"`+target+`"}`, false); w.Code != 403 || calls != 0 {
				t.Fatal("missing update csrf")
			}
			c.lang = "en"
			before := time.Now().Unix()
			w := c.do("POST", "/api/updates/run", `{"target":"`+target+`"}`, true)
			var run struct {
				PanelStartedAt int64 `json:"panelStartedAt"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &run); err != nil {
				t.Fatal(err)
			}
			if w.Code != 200 || run.PanelStartedAt < before || run.PanelStartedAt > dispatchedAt || calls != 1 {
				t.Fatalf("update: %d %s / calls=%d", w.Code, w.Body.String(), calls)
			}
			if nodes, err := svc.Nodes(t.Context()); err != nil || len(nodes) != 0 {
				t.Fatal("update created a node")
			}
			audit, err := svc.Audit(t.Context(), 10)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, a := range audit {
				if a.Action == "panel.update" {
					found = true
				}
			}
			if !found {
				t.Fatal("panel update not audited")
			}
		})
	}
}

func TestPanelUpdateFailureIsReported(t *testing.T) {
	c, _, pw := newServer(t)
	c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true)
	c.srv.upd.checkedAt = time.Now()
	c.srv.cfg.PanelUpdate = func(context.Context) error { return errors.New("updater test failure") }
	w := c.do("POST", "/api/updates/run", `{"target":"panel"}`, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "updater test failure") || !strings.Contains(w.Body.String(), `"panelStartedAt":0`) {
		t.Fatalf("failure: %s", w.Body.String())
	}
}
