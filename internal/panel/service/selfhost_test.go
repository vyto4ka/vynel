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

func TestShowWelcome(t *testing.T) {
	f := newFixture(t)
	// Fresh panel without any server stats: show welcome.
	if ok := must(f.s.ShowWelcome(f.ctx)); !ok {
		t.Fatal("expected ShowWelcome=true when no stats exist")
	}
	n := must(f.s.EnsureLocalNode(f.ctx, NodeInput{}))
	// Stats 12h old: still < 24h -> show welcome.
	must0(t, f.s.st.DB.Ping())
	_, err := f.s.st.DB.ExecContext(f.ctx, `INSERT INTO node_metrics (node_id, ts, cpu, mem_used, mem_total, load1, rx_bps, tx_bps, uptime, online, steal) VALUES (?, ?, 1, 1, 1, 0, 0, 0, 10, 0, 0)`,
		n.ID, f.now.Add(-12*time.Hour).Unix())
	must0(t, err)
	if ok := must(f.s.ShowWelcome(f.ctx)); !ok {
		t.Fatal("expected ShowWelcome=true when stats are 12h old")
	}
	// Stats 25h old: >= 24h -> hide welcome.
	_, err = f.s.st.DB.ExecContext(f.ctx, `INSERT INTO node_metrics_5m (node_id, ts, cpu, cpu_max, steal, mem_used, mem_total, rx_bps, tx_bps, rx_max, tx_max, online) VALUES (?, ?, 1, 1, 0, 1, 1, 0, 0, 0, 0, 0)`,
		n.ID, f.now.Add(-25*time.Hour).Unix())
	must0(t, err)
	if ok := must(f.s.ShowWelcome(f.ctx)); ok {
		t.Fatal("expected ShowWelcome=false when stats are 25h old")
	}
}

func TestNodeCertsMultiDomain(t *testing.T) {
	f := newFixture(t)
	must0(t, f.s.SetSetting(f.ctx, ActorCLI, SettingWebDomain, "panel.example.com"))
	must0(t, f.s.SetSetting(f.ctx, ActorCLI, SettingSubDomain, "sub.example.com"))
	n := must(f.s.EnsureLocalNode(f.ctx, NodeInput{Domain: "node.example.com"}))
	f.s.SelfCertStatus = func(d string) (certs.Status, bool) {
		return certs.Status{Domain: d, Issuer: "Let's Encrypt", NotAfter: f.now.Add(60 * 24 * time.Hour)}, true
	}
	cs := f.s.NodeCerts(f.ctx, n, nil)
	if len(cs) != 2 {
		t.Fatalf("expected 2 certs (panel + sub), got %+v", cs)
	}
	if cs[0].Domain != "panel.example.com" || len(cs[0].Roles) != 1 || cs[0].Roles[0] != "panel" || cs[0].DaysLeft != 60 {
		t.Fatalf("cert 0: %+v", cs[0])
	}
	if cs[1].Domain != "sub.example.com" || len(cs[1].Roles) != 1 || cs[1].Roles[0] != "sub" {
		t.Fatalf("cert 1: %+v", cs[1])
	}
}
