package webapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

// fakeResolver answers from a map; the wizard's DNS checks use it.
type fakeResolver map[string][]string

func (f fakeResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	if ips, ok := f[host]; ok {
		return ips, nil
	}
	return nil, fakeErr{}
}

type fakeErr struct{}

func (fakeErr) Error() string { return "no such host" }

// The node setup wizard API: the node carries its derived step, the wizard returns the
// requirement checks, and confirming the requirements finishes it (and shows in the list).
func TestNodeWizardAPI(t *testing.T) {
	c, svc, pw := newServer(t)
	if w := c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true); w.Code != 200 {
		t.Fatal("login")
	}
	ctx := context.Background()
	node, _, err := svc.CreateNode(ctx, service.ActorCLI, service.NodeInput{Name: "Германия", Country: "de", Domain: "de.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := svc.CreateProfile(ctx, service.ActorCLI, service.ProfileInput{Name: "Reality", TemplateID: "vless-reality-selfsteal"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AttachProfile(ctx, service.ActorCLI, service.AttachInput{NodeID: node.ID, ProfileID: p.ID,
		Values: map[string]any{"NODE_DOMAIN": "de.example.com"}}); err != nil {
		t.Fatal(err)
	}

	// The list knows the node's step: the requirements are not confirmed yet.
	w := c.do("GET", "/api/nodes", "", false)
	if w.Code != 200 {
		t.Fatalf("nodes: %d %s", w.Code, w.Body.String())
	}
	var list []Node
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range list {
		if n.ID == node.ID {
			found = true
			if n.SetupStep != service.WizardChecks {
				t.Fatalf("setupStep: %d", n.SetupStep)
			}
		}
	}
	if !found {
		t.Fatal("node missing from the list")
	}

	// The wizard's checks: a DNS record pointing at the node's address.
	oldResolver := service.Resolver
	service.Resolver = fakeResolver{"de.example.com": {"198.51.100.9"}}
	defer func() { service.Resolver = oldResolver }()
	if _, err := svc.AddAddress(ctx, service.ActorCLI, node.ID, "198.51.100.9", ""); err != nil {
		t.Fatal(err)
	}
	w = c.do("GET", "/api/nodes/"+itoa(node.ID)+"/wizard", "", false)
	if w.Code != 200 {
		t.Fatalf("wizard: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{`"step":4`, `"steps"`, `"state":"ok"`, "de.example.com"} {
		if !strings.Contains(body, want) {
			t.Fatalf("wizard lacks %s: %s", want, body[:400])
		}
	}

	// Confirming finishes the wizard; the list follows.
	if w := c.do("POST", "/api/nodes/"+itoa(node.ID)+"/confirm-requirements", `{"confirm":true}`, true); w.Code != 200 {
		t.Fatalf("confirm: %d %s", w.Code, w.Body.String())
	}
	w = c.do("GET", "/api/nodes", "", false)
	var list2 []Node
	if err := json.Unmarshal(w.Body.Bytes(), &list2); err != nil {
		t.Fatal(err)
	}
	for _, n := range list2 {
		if n.ID == node.ID && n.SetupStep != service.WizardDone {
			t.Fatalf("setupStep after confirm: %d", n.SetupStep)
		}
	}
}
