package service

import (
	"errors"
	"testing"

	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/telegramrelay"
)

func TestBotRouteSelectionAndUnavailableNodes(t *testing.T) {
	f := newFixture(t)
	n, _ := must2(f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Name: "Overseas", Country: "DE"}))
	if err := f.s.SetBotNode(f.ctx, ActorCLI, n.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("old node: %v", err)
	}
	if err := store.SetNodeFacts(f.ctx, f.s.st.DB, n.ID, store.NodeFacts{Capabilities: []string{telegramrelay.Capability}}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.SetBotNode(f.ctx, ActorCLI, n.ID); err != nil {
		t.Fatal(err)
	}
	if id, err := f.s.BotNodeID(f.ctx); err != nil || id != n.ID {
		t.Fatalf("route: %d %v", id, err)
	}
	local := must(f.s.EnsureLocalNode(f.ctx, NodeInput{Name: "Local"}))
	for _, id := range []int64{-1, 999999, local.ID} {
		if err := f.s.SetBotNode(f.ctx, ActorCLI, id); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %d: %v", id, err)
		}
	}
	if id, _ := f.s.BotNodeID(f.ctx); id != n.ID {
		t.Fatal("invalid selection changed the route")
	}
	must(f.s.UpdateNode(f.ctx, ActorCLI, n.ID, NodeInput{}, false))
	if err := f.s.ValidateBotNode(f.ctx, n.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("disabled node: %v", err)
	}
	if id, _ := f.s.BotNodeID(f.ctx); id != n.ID {
		t.Fatal("disabled route silently became direct")
	}
	if err := f.s.DeleteNode(f.ctx, ActorCLI, n.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.ValidateBotNode(f.ctx, n.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("deleted node: %v", err)
	}
	if err := f.s.SetBotNode(f.ctx, ActorCLI, 0); err != nil {
		t.Fatal(err)
	}
	if id, _ := f.s.BotNodeID(f.ctx); id != 0 {
		t.Fatal("could not restore direct route")
	}
}
