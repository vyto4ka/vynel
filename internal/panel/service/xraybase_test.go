package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

func TestBaseConfigRefusals(t *testing.T) {
	f := newFixture(t)
	for raw, want := range map[string]string{
		``:                           "пустой",
		`{"outbounds": [`:            "unexpected end",
		`[1]`:                        "JSON-объект",
		`{"inbounds": []}`:           "inbounds",
		`{"policy": {"levels": {}}}`: "policy",
	} {
		_, err := f.s.CheckBaseConfig(f.ctx, raw)
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v (want %q)", raw, err, want)
		}
	}
}

// A base config without DIRECT breaks a node whose inbound leaves through its own IP: that is
// refused, and the stored config stays.
func TestBaseConfigKeepsEgressWorking(t *testing.T) {
	f := newFixture(t)
	nl, _ := must2(f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Name: "NL", Country: "nl", Domain: "nl.example.com"}))
	if err := f.s.SyncAddresses(f.ctx, nl.ID, []ReportedAddress{{IP: "203.0.113.10", Interface: "eth0", Primary: true}, {IP: "203.0.113.12", Interface: "eth0"}}); err != nil {
		t.Fatal(err)
	}
	addrs := must(f.s.Addresses(f.ctx, nl.ID))
	prof := must(f.s.CreateProfile(f.ctx, ActorCLI, ProfileInput{Name: "R", TemplateID: "vless-reality-selfsteal"}))
	must(f.s.AttachProfile(f.ctx, ActorCLI, AttachInput{NodeID: nl.ID, ProfileID: prof.ID, ListenAddressID: &addrs[1].ID}))

	noDirect := `{"outbounds": [{"tag": "OUT", "protocol": "freedom"}]}`
	err := f.s.SaveBaseConfig(f.ctx, ActorCLI, noDirect)
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "DIRECT") {
		t.Fatalf("saved a config without DIRECT: %v", err)
	}
	if info := must(f.s.BaseConfig(f.ctx)); info.Customized {
		t.Fatal("refused config was stored")
	}

	ok := `{"outbounds": [{"tag": "DIRECT", "protocol": "freedom"}, {"tag": "WARP", "protocol": "blackhole"}]}`
	if err := f.s.SaveBaseConfig(f.ctx, ActorCLI, ok); err != nil {
		t.Fatal(err)
	}
	info := must(f.s.BaseConfig(f.ctx))
	if !info.Customized || !strings.Contains(info.JSON, "WARP") {
		t.Fatalf("not stored: %+v", info)
	}
	if a := must(store.ListAudit(f.ctx, f.s.Store().DB, 1)); len(a) != 1 || a[0].Action != "xray.base.save" {
		t.Fatalf("audit: %+v", a)
	}

	// Everything the panel adds around the admin's config is listed as its own.
	cfg, _, origins, err := f.s.NodeConfigPreview(f.ctx, nl.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, o := range origins {
		kinds[o.Kind] += o.Path + " "
	}
	for _, want := range []string{"api", "stats", "policy", "log", "inbound", "outbound_block", "outbound_egress", "rule_egress"} {
		if kinds[want] == "" {
			t.Errorf("no %q origin: %v", want, kinds)
		}
	}
	// The admin's own outbounds and rules are not listed.
	if strings.Contains(kinds["outbound_cascade"], "outbounds.0") || strings.Contains(kinds["outbound_block"], "outbounds.1 ") {
		t.Errorf("own outbounds marked as the panel's: %v", kinds)
	}
	if _, ok := cfg["inbounds"].([]any); !ok {
		t.Fatal("no inbounds in the preview")
	}
}

func TestBaseConfigLogOverride(t *testing.T) {
	f := newFixture(t)
	n, _ := must2(f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Name: "NL", Country: "nl", Domain: "nl.example.com"}))

	// DefaultBaseJSON already declares {"log":{"loglevel":"none"}}: must be preserved as-is.
	ds := must(f.s.DesiredState(f.ctx, n.ID))
	if !strings.Contains(string(ds.Config), `"loglevel":"none"`) {
		t.Fatalf("expected DefaultBaseJSON loglevel none to be preserved, got: %s", ds.Config)
	}

	// Base config without "log" section: panel injects XrayLog(DefaultXrayLog).
	noLog := `{"outbounds":[{"tag":"DIRECT","protocol":"freedom"}]}`
	if err := f.s.SaveBaseConfig(f.ctx, ActorCLI, noLog); err != nil {
		t.Fatal(err)
	}
	ds = must(f.s.DesiredState(f.ctx, n.ID))
	if !strings.Contains(string(ds.Config), `"loglevel":"warning"`) {
		t.Fatalf("expected default warning log when log section is absent, got: %s", ds.Config)
	}

	// Explicit non-empty "log" section in base config must NOT be overwritten by XrayLog(logLevel).
	withLog := `{"log":{"loglevel":"error","access":"/var/log/xray/access.log","dnsLog":true},"outbounds":[{"tag":"DIRECT","protocol":"freedom"}]}`
	if err := f.s.SaveBaseConfig(f.ctx, ActorCLI, withLog); err != nil {
		t.Fatal(err)
	}
	ds = must(f.s.DesiredState(f.ctx, n.ID))
	if !strings.Contains(string(ds.Config), `"loglevel":"error"`) || !strings.Contains(string(ds.Config), `"/var/log/xray/access.log"`) || !strings.Contains(string(ds.Config), `"dnsLog":true`) {
		t.Fatalf("explicit log section was overwritten: %s", ds.Config)
	}

	// Empty "log": {} should still be filled by panel's XrayLog.
	emptyLog := `{"log":{},"outbounds":[{"tag":"DIRECT","protocol":"freedom"}]}`
	if err := f.s.SaveBaseConfig(f.ctx, ActorCLI, emptyLog); err != nil {
		t.Fatal(err)
	}
	ds = must(f.s.DesiredState(f.ctx, n.ID))
	if !strings.Contains(string(ds.Config), `"loglevel":"warning"`) {
		t.Fatalf("empty log object was not filled: %s", ds.Config)
	}
}

func TestNodeBaseConfigAndCompiled(t *testing.T) {
	f := newFixture(t)
	n1, _ := must2(f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Name: "NL", Country: "nl", Domain: "nl.example.com"}))
	n2, _ := must2(f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Name: "DE", Country: "de", Domain: "de.example.com"}))
	if err := store.SetNodeFacts(f.ctx, f.s.Store().DB, n1.ID, store.NodeFacts{Capabilities: []string{CapNginx}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetNodeRuntime(f.ctx, f.s.Store().DB, n1.ID, store.NodeRuntime{AppliedHash: "applied-123", LastError: "warn-sample"}); err != nil {
		t.Fatal(err)
	}
	prof := must(f.s.CreateProfile(f.ctx, ActorCLI, ProfileInput{Name: "R", TemplateID: "vless-reality-selfsteal"}))
	must(f.s.AttachProfile(f.ctx, ActorCLI, AttachInput{NodeID: n1.ID, ProfileID: prof.ID}))
	u := must(f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: "alice"}))

	// Initially inherit.
	info := must(f.s.NodeBaseConfig(f.ctx, n1.ID))
	if info.Mode != "inherit" || info.NodeJSON != "" || info.GlobalJSON == "" {
		t.Fatalf("initial node base: %+v", info)
	}

	// Set custom per-node base config with explicit log and WARP outbound.
	custom := `{"log":{"loglevel":"debug","access":"none"},"outbounds":[{"tag":"DIRECT","protocol":"freedom"},{"tag":"WARP_N1","protocol":"blackhole"}]}`
	if err := f.s.SaveNodeBaseConfig(f.ctx, ActorCLI, n1.ID, "custom", custom); err != nil {
		t.Fatal(err)
	}
	info = must(f.s.NodeBaseConfig(f.ctx, n1.ID))
	if info.Mode != "custom" || !strings.Contains(info.NodeJSON, "WARP_N1") {
		t.Fatalf("custom node base: %+v", info)
	}
	ds1 := must(f.s.DesiredState(f.ctx, n1.ID))
	ds2 := must(f.s.DesiredState(f.ctx, n2.ID))
	if !strings.Contains(string(ds1.Config), `"WARP_N1"`) || !strings.Contains(string(ds1.Config), `"loglevel":"debug"`) {
		t.Fatalf("n1 did not get custom base: %s", ds1.Config)
	}
	if strings.Contains(string(ds2.Config), `"WARP_N1"`) {
		t.Fatalf("n2 should still inherit global base: %s", ds2.Config)
	}

	// Compiled config without secrets: masks privateKey and client UUID, includes nginx mutations (xver: 2) and nginx.conf.
	masked := must(f.s.CompiledNodeConfig(f.ctx, n1.ID, false))
	if masked.Revision != ds1.Hash || masked.AppliedRevision != "applied-123" || masked.LastError != "warn-sample" {
		t.Fatalf("compiled metadata: %+v", masked)
	}
	if !strings.Contains(masked.XrayConfig, "\n  ") {
		t.Fatalf("expected pretty-printed xrayConfig")
	}
	if !strings.Contains(masked.XrayConfig, `"xver": 2`) {
		t.Fatalf("expected nginxSpec mutation (xver: 2) in xrayConfig: %s", masked.XrayConfig)
	}
	if strings.Contains(masked.XrayConfig, u.UUID) {
		t.Fatalf("client UUID leaked in masked compiled config")
	}
	if !strings.Contains(masked.XrayConfig, `"privateKey": "•••"`) || !strings.Contains(masked.XrayConfig, `"id": "•••"`) {
		t.Fatalf("expected masked privateKey and client id: %s", masked.XrayConfig)
	}
	if !strings.Contains(masked.NginxConfig, "nl.example.com") {
		t.Fatalf("expected rendered nginx.conf for n1, got: %s", masked.NginxConfig)
	}

	// Compiled config with secrets: exposes real client UUID and unmasked privateKey.
	full := must(f.s.CompiledNodeConfig(f.ctx, n1.ID, true))
	if !strings.Contains(full.XrayConfig, u.UUID) {
		t.Fatalf("expected real client UUID in unmasked compiled config")
	}
	if strings.Contains(full.XrayConfig, `"privateKey": "•••"`) {
		t.Fatalf("privateKey should not be masked when secrets=true")
	}

	// Node without nginx capability gets the fallback comment.
	compiledN2 := must(f.s.CompiledNodeConfig(f.ctx, n2.ID, false))
	if compiledN2.NginxConfig != "# nginx is not configured for this node" {
		t.Fatalf("expected fallback nginx comment for n2, got: %q", compiledN2.NginxConfig)
	}

	// Switch back to inherit.
	if err := f.s.SaveNodeBaseConfig(f.ctx, ActorCLI, n1.ID, "inherit", ""); err != nil {
		t.Fatal(err)
	}
	info = must(f.s.NodeBaseConfig(f.ctx, n1.ID))
	if info.Mode != "inherit" || info.NodeJSON != "" {
		t.Fatalf("after inherit: %+v", info)
	}
	ds1 = must(f.s.DesiredState(f.ctx, n1.ID))
	if strings.Contains(string(ds1.Config), `"WARP_N1"`) {
		t.Fatalf("n1 still uses custom base after inherit: %s", ds1.Config)
	}
}
