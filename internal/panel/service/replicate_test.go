package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

// TestDuplicateInbound copies a node inbound: same node and profile, regenerated node
// keys, kept domain and override, suffixed tag.
func TestDuplicateInbound(t *testing.T) {
	f := newFixture(t)
	nl, _, prof, _ := f.setup(t)
	ni := must(f.s.NodeInbounds(f.ctx, nl.ID))[0] // VLESS_NL
	port := 8443
	if _, err := f.s.UpdateNodeInbound(f.ctx, ActorCLI, ni.ID, NodeInboundInput{PortOverride: &port}); err != nil {
		t.Fatal(err)
	}
	orig := must(f.s.NodeInbounds(f.ctx, nl.ID))[0]
	// Point at one of the node's values to compare keys later.
	oldKey := orig.Values["REALITY_PRIVATE_KEY"]
	if oldKey == nil || oldKey == "" {
		t.Fatalf("no generated key: %v", orig.Values)
	}

	dup := must(f.s.DuplicateInbound(f.ctx, ActorCLI, ni.ID))
	if dup.ID == ni.ID || dup.NodeID != nl.ID || dup.ProfileID != prof.ID {
		t.Fatalf("copy points elsewhere: %+v", dup)
	}
	if dup.PortOverride != 8443 || !dup.Enabled {
		t.Fatalf("port/enabled not kept: %+v", dup)
	}
	if newKey := dup.Values["REALITY_PRIVATE_KEY"]; newKey == oldKey {
		t.Fatalf("keys not regenerated: %v", newKey)
	}
	if dup.Values["NODE_DOMAIN"] != orig.Values["NODE_DOMAIN"] {
		t.Fatalf("domain not kept: %v", dup.Values["NODE_DOMAIN"])
	}
	if !strings.HasSuffix(dup.Tag, "_2") || strings.TrimSuffix(dup.Tag, "_2") != orig.Tag {
		t.Fatalf("tag %q is not %q_2", dup.Tag, orig.Tag)
	}
	// The copy renders, and a second copy takes _3 (copies count from the same base tag).
	dup2 := must(f.s.DuplicateInbound(f.ctx, ActorCLI, ni.ID))
	if !strings.HasSuffix(dup2.Tag, "_3") {
		t.Fatalf("second copy tag: %q", dup2.Tag)
	}
	inbounds := must(f.s.NodeInbounds(f.ctx, nl.ID))
	if len(inbounds) != 3 {
		t.Fatalf("want 3 inbounds on the node, got %d", len(inbounds))
	}
}

// TestDuplicateProfile copies a profile with its values, sources and name, unattached.
func TestDuplicateProfile(t *testing.T) {
	f := newFixture(t)
	_, _, prof, _ := f.setup(t)
	prof = must(f.s.UpdateProfile(f.ctx, ActorCLI, prof.ID, ProfileInput{Name: prof.Name}))

	// The copy copies values, own sources and the name gets «(копия)».
	if len(prof.Inbound) != 0 { // the base profile follows the template
		t.Fatalf("unexpected own source: %v", prof.Inbound)
	}
	copy := must(f.s.DuplicateProfile(f.ctx, ActorCLI, prof.ID))
	if copy.ID == prof.ID {
		t.Fatal("copy is the original")
	}
	if copy.Name != prof.Name+" (копия)" {
		t.Fatalf("copy name: %q", copy.Name)
	}
	if copy.TemplateID != prof.TemplateID || copy.TagPattern != prof.TagPattern {
		t.Fatalf("template/pattern not kept: %+v", copy)
	}
	if len(copy.Values) != len(prof.Values) {
		t.Fatalf("values not copied: %v vs %v", copy.Values, prof.Values)
	}
	if len(copy.Inbound) != len(prof.Inbound) {
		t.Fatalf("own source not copied: %v", copy.Inbound)
	}
	if copy.Client != prof.Client {
		t.Fatalf("client config not copied: %q", copy.Client)
	}
	// No node bindings.
	n := 0
	for _, ni := range must(f.s.NodeInbounds(f.ctx, 0)) {
		if ni.ProfileID == copy.ID {
			n++
		}
	}
	if n != 0 {
		t.Fatalf("copy is attached to %d inbound(s)", n)
	}
	// A second copy of the same profile gets «(копия 2)».
	copy2 := must(f.s.DuplicateProfile(f.ctx, ActorCLI, prof.ID))
	if copy2.Name != prof.Name+" (копия 2)" {
		t.Fatalf("second copy name: %q", copy2.Name)
	}
}

// TestApplyProfileToNodes attaches a profile to many nodes at once; nodes without a
// domain fail (reported per node), already-attached nodes are skipped.
func TestApplyProfileToNodes(t *testing.T) {
	f := newFixture(t)
	nl, _, prof, _ := f.setup(t)
	fi, _ := must2(f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Name: "Финляндия", Country: "fi", Domain: "fi.example.com"}))
	bare, _ := must2(f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Name: "Без домена", Country: "ee"}))

	res := must(f.s.ApplyProfileToNodes(f.ctx, ActorCLI, prof.ID, []int64{nl.ID, fi.ID, bare.ID, 9999}))
	if len(res) != 4 {
		t.Fatalf("want 4 results, got %d", len(res))
	}
	// nl already has the profile: skipped, not an error.
	if !res[0].Skipped || res[0].Err != nil {
		t.Fatalf("attached node: %+v", res[0])
	}
	// fi gets an inbound with generated node keys and the node's domain.
	if res[1].Err != nil || res[1].Tag == "" {
		t.Fatalf("fi: %+v", res[1])
	}
	if !strings.Contains(res[1].Tag, "FI") {
		t.Fatalf("tag not rendered for the node: %q", res[1].Tag)
	}
	fiIn := must(f.s.NodeInbounds(f.ctx, fi.ID))
	if len(fiIn) != 1 || fiIn[0].Tag != res[1].Tag {
		t.Fatalf("fi inbounds: %+v", fiIn)
	}
	r := must(f.s.RenderNodeInbound(f.ctx, fiIn[0].ID))
	ss, _ := r.Inbound["streamSettings"].(map[string]any)
	rs, _ := ss["realitySettings"].(map[string]any)
	if rs["privateKey"] == "" || rs["privateKey"] == nil {
		t.Fatalf("no generated key: %v", rs)
	}
	// The node without a domain is an error in the result, the rest still applied.
	if res[2].Err == nil || !strings.Contains(res[2].Err.Error(), "NODE_DOMAIN") {
		t.Fatalf("node without domain: %+v", res[2])
	}
	if !errors.Is(res[3].Err, store.ErrNotFound) {
		t.Fatalf("missing node: %+v", res[3])
	}
}
