package service

import (
	"testing"
)

// TestNodeSetupStep follows a node through the wizard: no profiles → step 2, a profile with
// node input variables unfilled → step 3, the variables filled → step 4, requirements
// confirmed → 0. Local nodes are always done.
func TestNodeSetupStep(t *testing.T) {
	f := newFixture(t)
	n, _ := must2(f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Name: "DE", Country: "de", Domain: "de.example.com"}))
	step := must(f.s.NodeSetupStep(f.ctx, n.ID))
	if step != WizardProfiles {
		t.Fatalf("fresh node: step %d, want %d", step, WizardProfiles)
	}
	// A profile without required node inputs (the domain comes default_from node.domain)
	// jumps straight to requirements.
	prof := must(f.s.CreateProfile(f.ctx, ActorCLI, ProfileInput{Name: "Reality", TemplateID: "vless-reality-selfsteal"}))
	must(f.s.AttachProfile(f.ctx, ActorCLI, AttachInput{NodeID: n.ID, ProfileID: prof.ID, Values: map[string]any{"NODE_DOMAIN": "de.example.com"}}))
	step = must(f.s.NodeSetupStep(f.ctx, n.ID))
	if step != WizardChecks {
		t.Fatalf("with profile: step %d, want %d", step, WizardChecks)
	}
	// Dropping the domain empties the NODE_DOMAIN variable: back to step 3.
	must(f.s.UpdateNode(f.ctx, ActorCLI, n.ID, NodeInput{Domain: ""}, true))
	step = must(f.s.NodeSetupStep(f.ctx, n.ID))
	if step != WizardVars {
		t.Fatalf("without domain: step %d, want %d", step, WizardVars)
	}
	must(f.s.UpdateNode(f.ctx, ActorCLI, n.ID, NodeInput{Domain: "de.example.com"}, true))
	// Requirements confirmed: the wizard is finished.
	if err := f.s.ConfirmNodeRequirements(f.ctx, ActorCLI, n.ID, f.now.Unix()); err != nil {
		t.Fatal(err)
	}
	step = must(f.s.NodeSetupStep(f.ctx, n.ID))
	if step != WizardDone {
		t.Fatalf("confirmed: step %d, want %d", step, WizardDone)
	}
	// The local node needs no wizard at all.
	loc, _ := must2(f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Country: "nl", Domain: "nl.example.com", Local: true}))
	must(f.s.AttachProfile(f.ctx, ActorCLI, AttachInput{NodeID: loc.ID, ProfileID: prof.ID, Values: map[string]any{"NODE_DOMAIN": "nl.example.com"}}))
	step = must(f.s.NodeSetupStep(f.ctx, loc.ID))
	if step != WizardDone {
		t.Fatalf("local node: step %d, want %d", step, WizardDone)
	}
}

// TestNodeWizardSteps checks the requirement checks of a node: DNS is looked up and compared
// with the node's addresses, manual nodes check nothing themselves.
func TestNodeWizardSteps(t *testing.T) {
	f := newFixture(t)
	prof := must(f.s.CreateProfile(f.ctx, ActorCLI, ProfileInput{Name: "Reality", TemplateID: "vless-reality-selfsteal"}))
	n, _ := must2(f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Name: "DE", Country: "de", Domain: "de.example.com"}))
	must(f.s.AttachProfile(f.ctx, ActorCLI, AttachInput{NodeID: n.ID, ProfileID: prof.ID, Values: map[string]any{"NODE_DOMAIN": "de.example.com"}}))

	old := Resolver
	defer func() { Resolver = old }()
	Resolver = fakeResolver{"de.example.com": {"198.51.100.7"}}
	must(f.s.AddAddress(f.ctx, ActorCLI, n.ID, "198.51.100.7", ""))
	step, steps := must2(f.s.NodeWizard(f.ctx, n.ID))
	if step != WizardChecks {
		t.Fatalf("step %d, want %d", step, WizardChecks)
	}
	st := map[string]string{}
	for _, x := range steps {
		st[x.Key] = x.State
	}
	if st["dns de.example.com"] != StepOK {
		t.Fatalf("dns ok: %v", st)
	}
	if st["port 443/tcp"] != StepSkip && st["port 80/tcp"] != StepSkip {
		t.Fatalf("ports: %v", st)
	}
	// Wrong DNS is a fail.
	Resolver = fakeResolver{"de.example.com": {"203.0.113.1"}}
	_, steps = must2(f.s.NodeWizard(f.ctx, n.ID))
	for _, x := range steps {
		if x.Key == "dns de.example.com" && x.State != StepFail {
			t.Fatalf("wrong dns: %v", x)
		}
	}
	// A manual node checks nothing itself.
	m, _ := must2(f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Name: "M", Country: "nl", Domain: "nl.example.com", Manual: true}))
	must(f.s.AttachProfile(f.ctx, ActorCLI, AttachInput{NodeID: m.ID, ProfileID: prof.ID, Values: map[string]any{"NODE_DOMAIN": "nl.example.com"}}))
	step, steps = must2(f.s.NodeWizard(f.ctx, m.ID))
	if step != WizardChecks {
		t.Fatalf("manual step %d, want %d", step, WizardChecks)
	}
	if len(steps) != 1 || steps[0].State != StepSkip {
		t.Fatalf("manual steps: %v", steps)
	}
	// Confirming finishes it, unconfirming reopens.
	if err := f.s.ConfirmNodeRequirements(f.ctx, ActorCLI, m.ID, f.now.Unix()); err != nil {
		t.Fatal(err)
	}
	if step, _ := must2(f.s.NodeWizard(f.ctx, m.ID)); step != WizardDone {
		t.Fatalf("manual confirmed: %d", step)
	}
	if err := f.s.ConfirmNodeRequirements(f.ctx, ActorCLI, m.ID, 0); err != nil {
		t.Fatal(err)
	}
	if step, _ := must2(f.s.NodeWizard(f.ctx, m.ID)); step != WizardChecks {
		t.Fatalf("manual unconfirmed: %d", step)
	}
}
