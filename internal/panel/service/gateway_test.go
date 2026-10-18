package service

import (
	"testing"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

func TestGatewayAccess(t *testing.T) {
	f := newFixture(t)
	// No remote nodes: closed (nothing to let in).
	if acc := must(f.s.GatewayAccess(f.ctx)); acc.Open || len(acc.IPs) != 0 {
		t.Fatalf("empty panel: %+v", acc)
	}
	// A new node waits with an install token: open for it to join.
	n, token, err := f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Name: "DE", Country: "de", Domain: "de.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if acc := must(f.s.GatewayAccess(f.ctx)); !acc.Open {
		t.Fatal("must be open while an install token waits")
	}
	// Joined (token used, certificate issued) but never connected since: still open.
	if _, err := f.s.ConsumeInstallToken(f.ctx, token, func(*store.Node) (string, error) { return "01ab", nil }); err != nil {
		t.Fatal(err)
	}
	if acc := must(f.s.GatewayAccess(f.ctx)); !acc.Open {
		t.Fatal("must stay open until the node's address is known")
	}
	// Connected from an address: only that address.
	if err := store.AddNodePeerIP(f.ctx, f.s.st.DB, n.ID, "203.0.113.5"); err != nil {
		t.Fatal(err)
	}
	acc := must(f.s.GatewayAccess(f.ctx))
	if acc.Open || len(acc.IPs) != 1 || acc.IPs[0] != "203.0.113.5" {
		t.Fatalf("known node: %+v", acc)
	}
	// Addresses keep the newest four.
	for _, ip := range []string{"203.0.113.6", "203.0.113.7", "203.0.113.8", "203.0.113.9", "203.0.113.9"} {
		_ = store.AddNodePeerIP(f.ctx, f.s.st.DB, n.ID, ip)
	}
	got := must(store.GetNode(f.ctx, f.s.st.DB, n.ID)).PeerIPs
	if len(got) != 4 || got[0] != "203.0.113.9" {
		t.Fatalf("peers %v", got)
	}
}
