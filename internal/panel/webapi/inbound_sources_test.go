package webapi

import (
	"context"
	"strings"
	"testing"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

// The node inbound editor API: the final render, per-level renders and the per-key source
// labels ("template" | "profile" | "node"), plus PATCH of the node override and reset.
func TestInboundSourcesAPI(t *testing.T) {
	c, svc, pw := newServer(t)
	if w := c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true); w.Code != 200 {
		t.Fatal("login")
	}
	ctx := context.Background()
	node, _, err := svc.CreateNode(ctx, service.ActorCLI, service.NodeInput{Name: "Нидерланды", Country: "nl", Domain: "nl.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := svc.CreateProfile(ctx, service.ActorCLI, service.ProfileInput{Name: "XHTTP", TemplateID: "vless-xhttp-reality",
		Override: map[string]any{"sniffing": map[string]any{"routeOnly": false}}})
	if err != nil {
		t.Fatal(err)
	}
	ni, err := svc.AttachProfile(ctx, service.ActorCLI, service.AttachInput{NodeID: node.ID, ProfileID: p.ID, PortOverride: 8443})
	if err != nil {
		t.Fatal(err)
	}
	// The node's own override lands through PATCH (validated on save).
	patch := `{"override":{"streamSettings":{"sockopt":{"tcpFastOpen":false}}}}`
	if w := c.do("PATCH", "/api/inbounds/"+itoa(ni.ID), patch, true); w.Code != 200 {
		t.Fatalf("patch override: %d %s", w.Code, w.Body.String())
	}

	w := c.do("GET", "/api/inbounds/"+itoa(ni.ID), "", false)
	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("get: %d %s", w.Code, body)
	}
	for _, want := range []string{
		`"sources"`,
		`"levels"`,
		`"routeOnly":"profile"`,
		`"tcpFastOpen":"node"`,
		`"override"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("get lacks %s: %s", want, body[:400])
		}
	}
	if strings.Contains(body, "privateKey") && !strings.Contains(body, masked) {
		t.Fatal("secrets not masked")
	}

	// An override that breaks lint is rejected.
	bad := `{"override":{"streamSettings":{"xhttpSettings":{"extra":{"extra":{}}}}}}`
	if w := c.do("PATCH", "/api/inbounds/"+itoa(ni.ID), bad, true); w.Code == 200 || !strings.Contains(w.Body.String(), "extra") {
		t.Fatalf("invalid override accepted: %d %s", w.Code, w.Body.String())
	}

	// Reset to the profile: an empty override, "node" labels gone.
	if w := c.do("PATCH", "/api/inbounds/"+itoa(ni.ID), `{"override":{}}`, true); w.Code != 200 {
		t.Fatalf("reset: %d %s", w.Code, w.Body.String())
	}
	w = c.do("GET", "/api/inbounds/"+itoa(ni.ID), "", false)
	body = w.Body.String()
	if strings.Contains(body, `"tcpFastOpen":"node"`) {
		t.Fatalf("node label survived the reset: %s", body[:400])
	}
	if !strings.Contains(body, `"routeOnly":"profile"`) {
		t.Fatalf("profile label lost after the reset: %s", body[:400])
	}
}
