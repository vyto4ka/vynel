package service

import (
	"context"
	"errors"
	"slices"
	"testing"
)

type fakeResolver map[string][]string

func (f fakeResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	if ips, ok := f[host]; ok {
		return ips, nil
	}
	return nil, errors.New("no such host")
}

func TestNetworkRolesAndDNS(t *testing.T) {
	f := newFixture(t)
	n := must(f.s.EnsureLocalNode(f.ctx, NodeInput{Name: "NL", Country: "nl", Domain: "nl.example.com"}))
	ip1 := must(f.s.AddAddress(f.ctx, ActorCLI, n.ID, "203.0.113.10", ""))
	ip2 := must(f.s.AddAddress(f.ctx, ActorCLI, n.ID, "203.0.113.12", ""))
	prof := must(f.s.CreateProfile(f.ctx, ActorCLI, ProfileInput{Name: "Reality", TemplateID: "vless-reality-selfsteal"}))
	must(f.s.AttachProfile(f.ctx, ActorCLI, AttachInput{NodeID: n.ID, ProfileID: prof.ID, ListenAddressID: &ip2.ID}))
	for k, v := range map[string]string{SettingSubDomain: "sub.example.com", SettingSubAddress: ip1.IP, SettingWebDomain: "app.example.com"} {
		if err := f.s.SetSetting(f.ctx, ActorCLI, k, v); err != nil {
			t.Fatal(err)
		}
	}
	old := Resolver
	defer func() { Resolver = old }()
	Resolver = fakeResolver{"sub.example.com": {"203.0.113.10"}, "app.example.com": {"198.51.100.1"}, "nl.example.com": {"203.0.113.12"}}

	net := must(f.s.Network(f.ctx))
	if len(net.Nodes) != 1 || len(net.Nodes[0].Addresses) != 2 {
		t.Fatalf("nodes %+v", net.Nodes)
	}
	kinds := func(a NetAddress) []string {
		var k []string
		for _, r := range a.Roles {
			k = append(k, r.Kind)
		}
		return k
	}
	for _, a := range net.Nodes[0].Addresses {
		switch a.IP {
		case ip1.IP:
			if !slices.Equal(kinds(a), []string{"panel", "sub"}) {
				t.Errorf("%s roles %v", a.IP, kinds(a))
			}
		case ip2.IP:
			if !slices.Equal(kinds(a), []string{"inbound", "egress"}) {
				t.Errorf("%s roles %v", a.IP, kinds(a))
			}
		}
	}
	got := map[string]DomainCheck{}
	for _, d := range net.Domains {
		got[d.Name] = d
	}
	if !got["sub.example.com"].OK || got["app.example.com"].OK || !got["nl.example.com"].OK || len(got) != 3 {
		t.Fatalf("dns %+v", net.Domains)
	}
}
