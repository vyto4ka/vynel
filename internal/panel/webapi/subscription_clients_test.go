package webapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSubscriptionClientDraft(t *testing.T) {
	c, svc, pw := newServer(t)
	if w := c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true); w.Code != 200 {
		t.Fatal("login")
	}
	draft := `{"userAgent":"Happ/4.1.0","basics":{"title":"Test","updateHours":3},"routing":{"ruDirect":false,"dnsDirect":["9.9.9.9"],"dnsProxy":[],"torrent":"proxy","direct":["draft.example"],"proxy":[],"block":[]}}`
	w := c.do("POST", "/api/subscription/test", draft, true)
	var data struct {
		RoutingProfile map[string]any `json:"routingProfile"`
		Headers        []struct{ Name, Value string }
		AppLinks       []struct{ Name, Value string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil || w.Code != 200 {
		t.Fatalf("preview: %d %s %v", w.Code, w.Body.String(), err)
	}
	sites, _ := json.Marshal(data.RoutingProfile["DirectSites"])
	if string(sites) != `["domain:draft.example"]` || data.RoutingProfile["DomesticDNSIP"] != "9.9.9.9" {
		t.Fatalf("draft not used: %v", data.RoutingProfile)
	}
	interval := ""
	for _, h := range data.Headers {
		if h.Name == "Profile-Update-Interval" {
			interval = h.Value
		}
	}
	if interval != "3" {
		t.Fatalf("draft interval: %s", interval)
	}
	saved, err := svc.ClientRouting(context.Background())
	if err != nil || !saved.RUDirect || len(saved.Direct) != 0 {
		t.Fatalf("preview changed saved routing: %v %v", saved, err)
	}

	w = c.do("POST", "/api/subscription/test", `{"userAgent":"karing/1.2.24"}`, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"format":"mihomo"`) || !strings.Contains(w.Body.String(), "x-hwid=true") {
		t.Fatalf("Karing preview: %s", w.Body.String())
	}
	// Custom import links are preserved in the draft preview.
	w = c.do("POST", "/api/subscription/test", `{"userAgent":"karing/1.2.24","page":{"apps":[{"id":"karing","name":"Karing","link":"karing://install-config?url={url_enc}&x-hwid=false"}]}}`, true)
	if !strings.Contains(w.Body.String(), "x-hwid=false") {
		t.Fatalf("custom link replaced: %s", w.Body.String())
	}
}
