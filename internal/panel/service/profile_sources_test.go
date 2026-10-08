package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

func TestProfileSources(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New(st)
	if err := s.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	node, _, err := s.CreateNode(ctx, ActorCLI, NodeInput{Name: "Нидерланды", Country: "nl", Domain: "nl.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProfile(ctx, ActorCLI, ProfileInput{Name: "XHTTP", TemplateID: "vless-xhttp-reality", Override: map[string]any{"sniffing": map[string]any{"enabled": false}}})
	if err != nil {
		t.Fatal(err)
	}
	ni, err := s.AttachProfile(ctx, ActorCLI, AttachInput{NodeID: node.ID, ProfileID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	tpl, _ := xrayconf.GetTemplate("vless-xhttp-reality")

	// The editor gets the full source: legacy override folded in, extra inlined, variables kept.
	in, host := ProfileSources(tpl, p)
	raw, _ := json.Marshal(in)
	for _, want := range []string{`"enabled":false`, `"sessionPlacement":"${XHTTP_SESSION_PLACEMENT}"`, `"privateKey":"${REALITY_PRIVATE_KEY}"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("inbound source lacks %s: %s", want, raw)
		}
	}

	// Edit the client xmux and the server JSON as plain JSON.
	extra := host["xhttp_client_extra"].(map[string]any)
	extra["xmux"] = map[string]any{"maxConcurrency": "1", "cMaxReuseTimes": "64-128", "hMaxRequestTimes": "600-900", "hMaxReusableSecs": "1800-3000"}
	in["sniffing"] = map[string]any{"enabled": true, "routeOnly": false, "destOverride": []any{"http", "tls"}}
	if p, err = s.UpdateProfile(ctx, ActorCLI, p.ID, ProfileInput{Inbound: in, Host: host}); err != nil {
		t.Fatal(err)
	}
	if p.Inbound == nil || p.Host == nil || len(p.Override) != 0 {
		t.Fatalf("sources not stored: %+v", p)
	}
	h, err := s.HostFor(ctx, ni)
	if err != nil {
		t.Fatal(err)
	}
	if xm, _ := h.Extra["xmux"].(map[string]any); xm["maxConcurrency"] != "1" || xm["cMaxReuseTimes"] != "64-128" {
		t.Fatalf("client xmux: %v", h.Extra["xmux"])
	}
	r, err := s.RenderNodeInbound(ctx, ni.ID)
	if err != nil {
		t.Fatal(err)
	}
	sn := r.Inbound["sniffing"].(map[string]any)
	if sn["routeOnly"] != false || r.Inbound["streamSettings"].(map[string]any)["realitySettings"].(map[string]any)["privateKey"] == "${REALITY_PRIVATE_KEY}" {
		t.Fatalf("rendered inbound: %v", r.Inbound)
	}

	// A typo in a variable is refused on save, for the server and for the subscription.
	bad := copyMap(host)
	bad["sni"] = "${NODE_DOMIAN}"
	if _, err := s.UpdateProfile(ctx, ActorCLI, p.ID, ProfileInput{Host: bad}); err == nil || !strings.Contains(err.Error(), "NODE_DOMIAN") {
		t.Fatalf("typo in host accepted: %v", err)
	}
	noProto := copyMap(in)
	delete(noProto, "protocol")
	if _, err := s.UpdateProfile(ctx, ActorCLI, p.ID, ProfileInput{Inbound: noProto}); err == nil {
		t.Fatal("inbound without protocol accepted")
	}

	// Saving the template's own source makes the profile follow the template again.
	if p, err = s.UpdateProfile(ctx, ActorCLI, p.ID, ProfileInput{Inbound: tpl.SourceInbound(), Host: tpl.SourceHost()}); err != nil {
		t.Fatal(err)
	}
	if p.Inbound != nil || p.Host != nil {
		t.Fatal("a source equal to the template must be dropped")
	}
}
