package service

import (
	"testing"
	"time"

	"github.com/vyto4ka/vynel/internal/certs"
)

func TestSelfHost(t *testing.T) {
	f := newFixture(t)

	// No settings, no nodes: the panel host is there but without domains or certificates.
	sh := must(f.s.SelfHost(f.ctx))
	if len(sh.Domains) != 0 || len(sh.Certs) != 0 {
		t.Fatalf("empty settings: %+v", sh)
	}
	if sh.WithNode {
		t.Fatal("no local node yet")
	}
	if sh.Version == "" || sh.Uptime < 0 {
		t.Fatalf("version/uptime not filled: %+v", sh)
	}

	// Domains from the settings; the certificate reader (wired from app.go) is consulted.
	must0(t, f.s.SetSetting(f.ctx, ActorCLI, SettingSubDomain, "Sub.Example.com "))
	must0(t, f.s.SetSetting(f.ctx, ActorCLI, SettingWebDomain, "panel.example.com"))
	f.s.SelfCertStatus = func(d string) (certs.Status, bool) {
		if d == "sub.example.com" {
			return certs.Status{Domain: d, Issuer: "internal", NotAfter: f.now.Add(24 * time.Hour)}, true
		}
		return certs.Status{}, false
	}
	sh = must(f.s.SelfHost(f.ctx))
	if len(sh.Domains) != 2 || sh.Domains[0] != "sub.example.com" || sh.Domains[1] != "panel.example.com" {
		t.Fatalf("domains: %v", sh.Domains)
	}
	if len(sh.Certs) != 2 {
		t.Fatalf("certs: %+v", sh.Certs)
	}
	if c := sh.Certs[0]; !c.OK || c.Issuer != "internal" || c.NotAfter != f.now.Add(24*time.Hour).Unix() {
		t.Fatalf("cert 0: %+v", c)
	}
	if c := sh.Certs[1]; c.OK {
		t.Fatalf("cert 1 should be missing: %+v", c)
	}

	// A local node (panel with --with-node) flips WithNode without creating anything else here.
	if _, err := f.s.EnsureLocalNode(f.ctx, NodeInput{}); err != nil {
		t.Fatal(err)
	}
	sh = must(f.s.SelfHost(f.ctx))
	if !sh.WithNode {
		t.Fatal("local node exists, WithNode should be true")
	}
}
