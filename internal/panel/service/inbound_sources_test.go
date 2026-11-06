package service

import (
	"context"
	"testing"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

// The node inbound's inheritance levels and per-key sources (docs/INBOUNDS.md §1.4): the
// final render with each key labelled "template", "profile" or "node".
func TestNodeInboundSources(t *testing.T) {
	ctx := context.Background()
	st, err := newStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New(st)
	node, _, err := s.CreateNode(ctx, ActorCLI, NodeInput{Name: "Нидерланды", Country: "nl", Domain: "nl.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProfile(ctx, ActorCLI, ProfileInput{Name: "XHTTP", TemplateID: "vless-xhttp-reality",
		Override: map[string]any{"sniffing": map[string]any{"routeOnly": false}}})
	if err != nil {
		t.Fatal(err)
	}
	ni, err := s.AttachProfile(ctx, ActorCLI, AttachInput{NodeID: node.ID, ProfileID: p.ID, PortOverride: 8443})
	if err != nil {
		t.Fatal(err)
	}
	// The node override comes through UpdateNodeInbound, with validation on save.
	if _, err := s.UpdateNodeInbound(ctx, ActorCLI, ni.ID, NodeInboundInput{Override: map[string]any{
		"streamSettings": map[string]any{"sockopt": map[string]any{"tcpFastOpen": false}}}}); err != nil {
		t.Fatal(err)
	}

	final, levels, sources, err := s.NodeInboundSources(ctx, ni.ID)
	if err != nil {
		t.Fatal(err)
	}
	ss, _ := sources["sniffing"].(map[string]any)
	if ss == nil || ss["routeOnly"] != "profile" {
		t.Fatalf("sniffing.routeOnly not from profile: %v", ss)
	}
	stt, _ := sources["streamSettings"].(map[string]any)
	so, _ := stt["sockopt"].(map[string]any)
	if so == nil || so["tcpFastOpen"] != "node" {
		t.Fatalf("sockopt.tcpFastOpen not from node: %v", so)
	}
	if sources["port"] != "node" || sources["tag"] != "template" { // tag: the panel's own, not an override
		t.Fatalf("system fields mislabeled: %v", sources)
	}
	if sources["protocol"] != "template" {
		t.Fatalf("protocol not from template: %v", sources["protocol"])
	}
	prof, _ := levels["profile"].(map[string]any)
	templ, _ := levels["template"].(map[string]any)
	if prof == nil || templ == nil {
		t.Fatal("missing levels")
	}
	if !equalJSON(final["port"], 8443) || !equalJSON(prof["port"], 443) { // levels carry the template's own port
		t.Fatalf("port: final %v profile level %v", final["port"], prof["port"])
	}
	sn, _ := prof["sniffing"].(map[string]any)
	if sn == nil || sn["routeOnly"] != false {
		t.Fatalf("profile level lacks the override: %v", sn)
	}
	tn, _ := templ["sniffing"].(map[string]any)
	if tn == nil || tn["routeOnly"] != true {
		t.Fatalf("template level's routeOnly: %v", tn)
	}

	// An override that breaks lint (a nested "extra" in xhttpSettings) is refused on save.
	nested := map[string]any{"streamSettings": map[string]any{"xhttpSettings": map[string]any{"extra": map[string]any{"extra": map[string]any{}}}}}
	if _, err := s.UpdateNodeInbound(ctx, ActorCLI, ni.ID, NodeInboundInput{Override: nested}); err == nil {
		t.Fatal("invalid override accepted")
	}

	// Reset to the profile: no keys left in the node override, nothing marked "node".
	if _, err := s.UpdateNodeInbound(ctx, ActorCLI, ni.ID, NodeInboundInput{Override: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	ni2, err := s.NodeInboundByTag(ctx, ni.Tag)
	if err != nil {
		t.Fatal(err)
	}
	if len(ni2.Override) != 0 {
		t.Fatalf("reset left keys: %v", ni2.Override)
	}
	_, _, sources, err = s.NodeInboundSources(ctx, ni.ID)
	if err != nil {
		t.Fatal(err)
	}
	ss, _ = sources["sniffing"].(map[string]any)
	if ss == nil || ss["routeOnly"] != "profile" {
		t.Fatalf("after reset routeOnly not from profile: %v", ss)
	}
	stt, _ = sources["streamSettings"].(map[string]any)
	so, _ = stt["sockopt"].(map[string]any)
	if so == nil || so["tcpFastOpen"] != "template" {
		t.Fatalf("after reset sockopt.tcpFastOpen not from template: %v", so)
	}
}

// newStore opens an in-memory store.
func newStore(t *testing.T) (*store.Store, error) {
	t.Helper()
	return store.Open(context.Background(), ":memory:")
}
