package service

import (
	"strings"
	"testing"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

// A cascade that goes with its inbound takes its grants along, and a new cascade never gets an
// old one's id, so nobody inherits access to it.
func TestCascadeGoneWithInboundTakesGrants(t *testing.T) {
	f := newFixture(t)
	nl, de, prof, _ := f.setup(t)
	nlIn := must(f.s.NodeInbounds(f.ctx, nl.ID))[0]
	deIn := must(f.s.NodeInbounds(f.ctx, de.ID))[0]
	alice := must(f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: "alice"}))
	old := must(f.s.CreateCascade(f.ctx, ActorCLI, CascadeInput{Name: "old", EntryInboundID: nlIn.ID, ExitInboundID: deIn.ID, Enabled: true}))
	must(f.s.PutGrant(f.ctx, ActorCLI, GrantInput{UserID: alice.ID, Kind: store.GrantCascade, RefID: old.ID}))

	must0(t, f.s.DetachInbound(f.ctx, ActorCLI, nlIn.ID))
	if cs := must(f.s.Cascades(f.ctx)); len(cs) != 0 {
		t.Fatalf("the cascade outlived its inbound: %+v", cs)
	}
	if gs := must(f.s.Grants(f.ctx)); len(gs) != 0 {
		t.Fatalf("grants left behind: %+v", gs[0])
	}
	again := must(f.s.AttachProfile(f.ctx, ActorCLI, AttachInput{NodeID: nl.ID, ProfileID: prof.ID}))
	fresh := must(f.s.CreateCascade(f.ctx, ActorCLI, CascadeInput{Name: "new", EntryInboundID: again.ID, ExitInboundID: deIn.ID, Enabled: true}))
	if fresh.ID == old.ID {
		t.Fatalf("a new cascade got the old id %d", old.ID)
	}
	for _, g := range must(f.s.UserGrants(f.ctx, alice.ID)) {
		if g.Kind == store.GrantCascade && g.RefID == fresh.ID && g.GrantID != 0 {
			t.Fatal("alice inherited the new cascade")
		}
	}
}

// A subscription of this panel merged into another user's keeps each server's own key: a
// cascade's derived key stays, the user's key goes only to the rest.
func TestOwnSubscriptionKeepsServerKeys(t *testing.T) {
	f := newFixture(t)
	nl, de, _, _ := f.setup(t)
	must0(t, f.s.SetSetting(f.ctx, ActorCLI, SettingSubDomain, "sub.example.com"))
	nlIn := must(f.s.NodeInbounds(f.ctx, nl.ID))[0]
	deIn := must(f.s.NodeInbounds(f.ctx, de.ID))[0]
	alice := must(f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: "alice"}))
	c := must(f.s.CreateCascade(f.ctx, ActorCLI, CascadeInput{Name: "nl-de", EntryInboundID: nlIn.ID, ExitInboundID: deIn.ID, Enabled: true}))
	link := must(f.s.SubscriptionURL(f.ctx, alice))
	lines := must(f.s.fetchExternal(f.ctx, link, 0))
	if !strings.Contains(lines, CascadeUUID(alice.UUID, c.ID)) {
		t.Fatalf("the cascade lost its key:\n%s", lines)
	}
	if !strings.Contains(lines, "vless://"+alice.UUID+"@") {
		t.Fatalf("the plain servers lost the user's key:\n%s", lines)
	}
}
