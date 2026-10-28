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
}
