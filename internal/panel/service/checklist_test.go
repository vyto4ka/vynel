package service

import (
	"context"
	"errors"
	"testing"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

func TestNodeChecklist(t *testing.T) {
	f := newFixture(t)
	n, _ := must2(f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Name: "DE", Country: "de", Domain: "de.example.com"}))
	states := func(connected bool) map[string]string {
		t.Helper()
		steps := must(f.s.NodeChecklist(f.ctx, n.ID, func(int64) bool { return connected }))
		out := map[string]string{}
		for _, s := range steps {
			out[s.Key] = s.State
		}
		return out
	}
	if st := states(false); st["joined"] != StepWait || st["online"] != StepWait || len(st) != 8 {
		t.Fatalf("fresh node: %v", st)
	}

	old, oldSite := Resolver, SiteCheck
	defer func() { Resolver, SiteCheck = old, oldSite }()
	Resolver = fakeResolver{"de.example.com": {"198.51.100.7"}}
	siteErr := errors.New("refused")
	SiteCheck = func(context.Context, string) error { return siteErr }

	// The node joined, connected and applied its config.
	if err := store.SetNodeCert(f.ctx, f.s.st.DB, n.ID, "serial"); err != nil {
		t.Fatal(err)
	}
	must(f.s.AddAddress(f.ctx, ActorCLI, n.ID, "198.51.100.7", ""))
	if err := store.SetNodeDesired(f.ctx, f.s.st.DB, n.ID, 1, "h1"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetNodeRuntime(f.ctx, f.s.st.DB, n.ID, store.NodeRuntime{AppliedHash: "h1", XrayVersion: "26.1.1", AgentVersion: "dev"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetNodeFacts(f.ctx, f.s.st.DB, n.ID, "2.10.0", nil); err != nil {
		t.Fatal(err)
	}
	st := states(true)
	if st["joined"] != StepOK || st["online"] != StepOK || st["applied"] != StepOK || st["dns"] != StepOK || st["site"] != StepFail || st["inbounds"] != StepFail {
		t.Fatalf("joined node: %v", st)
	}
	siteErr = nil
	if st := states(true); st["site"] != StepOK {
		t.Fatalf("site: %v", st)
	}
	Resolver = fakeResolver{"de.example.com": {"203.0.113.1"}}
	if st := states(true); st["dns"] != StepFail || st["site"] != StepWait {
		t.Fatalf("wrong DNS: %v", st)
	}
}
