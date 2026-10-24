package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/store"
	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
)

func TestBillingPeriod(t *testing.T) {
	loc := time.UTC
	day := func(s string) time.Time { v, _ := time.ParseInLocation("2006-01-02 15:04", s, loc); return v }
	for _, c := range []struct {
		now      string
		reset    int
		from, to string
	}{
		{"2026-10-06 12:00", 1, "2026-10-01 00:00", "2026-11-01 00:00"},
		{"2026-10-06 12:00", 15, "2026-09-15 00:00", "2026-10-15 00:00"},
		{"2026-10-15 00:00", 15, "2026-10-15 00:00", "2026-11-15 00:00"},
		{"2026-02-20 10:00", 31, "2026-01-31 00:00", "2026-02-28 00:00"}, // a short month: its last day
		{"2026-03-01 10:00", 31, "2026-02-28 00:00", "2026-03-31 00:00"},
		{"2026-01-10 10:00", 20, "2025-12-20 00:00", "2026-01-20 00:00"},
	} {
		from, to := BillingPeriod(day(c.now), loc, c.reset)
		if !from.Equal(day(c.from)) || !to.Equal(day(c.to)) {
			t.Errorf("%s day %d: %s – %s, want %s – %s", c.now, c.reset, from, to, c.from, c.to)
		}
	}
}

func drainNotices(s *Service) []Notice {
	var out []Notice
	for {
		select {
		case n := <-s.Notices():
			out = append(out, n)
		default:
			return out
		}
	}
}

// A node about to cross its provider's limit is switched off, stays off this period, and
// comes back on its own in the next one; the admin can switch it back on earlier.
func TestTrafficLimitSwitchesNodeOff(t *testing.T) {
	f := newFixture(t)
	_ = f.s.SetSetting(f.ctx, ActorCLI, SettingBotTimezone, "UTC")
	nl, _ := must2(f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Name: "NL", Country: "nl", Domain: "nl.example.com"}))
	limit := int64(100 << 30)
	day := 6
	must(f.s.SetNodeBilling(f.ctx, ActorCLI, nl.ID, NodeBillingInput{TrafficLimit: &limit, ResetDay: &day}))
	db := f.s.Store().DB
	_ = store.AddIfaceTraffic(f.ctx, db, nl.ID, f.now.Unix(), 40<<30, 50<<30) // 90 GB of 100: still on
	if err := f.s.Maintenance(f.ctx, false); err != nil {
		t.Fatal(err)
	}
	if n := must(store.GetNode(f.ctx, db, nl.ID)); !n.Enabled {
		t.Fatal("switched off 10 GB before the limit")
	}
	_ = store.AddIfaceTraffic(f.ctx, db, nl.ID, f.now.Unix(), 0, 8<<30+1) // 98 GB + : within 2 GB
	drainNotices(f.s)
	must0(t, f.s.Maintenance(f.ctx, false))
	if n := must(store.GetNode(f.ctx, db, nl.ID)); n.Enabled {
		t.Fatal("not switched off within 2 GB of the limit")
	}
	if ns := drainNotices(f.s); len(ns) != 1 || ns[0].Kind != "billing" || !strings.Contains(ns[0].Text, "выключена") {
		t.Fatalf("notices %+v", ns)
	}
	bills := must(f.s.BillingOverview(f.ctx))
	if !bills[0].OffByLimit || bills[0].Used < 98<<30 {
		t.Fatalf("%+v", bills[0])
	}

	// The admin switches it on: it stays on for the rest of the period.
	must(f.s.UpdateNode(f.ctx, ActorCLI, nl.ID, NodeInput{Domain: "nl.example.com"}, true))
	must0(t, f.s.Maintenance(f.ctx, false))
	if n := must(store.GetNode(f.ctx, db, nl.ID)); !n.Enabled {
		t.Fatal("switched off again after the admin switched it on")
	}

	// Next period: the limit holds again, from zero.
	f.now = f.now.AddDate(0, 1, 0)
	_ = store.AddIfaceTraffic(f.ctx, db, nl.ID, f.now.Unix(), 0, 99<<30)
	must0(t, f.s.Maintenance(f.ctx, false))
	if n := must(store.GetNode(f.ctx, db, nl.ID)); n.Enabled {
		t.Fatal("the override outlived its period")
	}
	// And the period after that, it comes back by itself.
	drainNotices(f.s)
	f.now = f.now.AddDate(0, 1, 0)
	must0(t, f.s.Maintenance(f.ctx, false))
	if n := must(store.GetNode(f.ctx, db, nl.ID)); !n.Enabled {
		t.Fatal("not back on in the new period")
	}
	if ns := drainNotices(f.s); len(ns) != 1 || !strings.Contains(ns[0].Text, "снова включена") {
		t.Fatalf("notices %+v", ns)
	}

	// Raising the limit past the use brings a switched-off node back at once.
	_ = store.AddIfaceTraffic(f.ctx, db, nl.ID, f.now.Unix(), 0, 99<<30)
	must0(t, f.s.Maintenance(f.ctx, false))
	if n := must(store.GetNode(f.ctx, db, nl.ID)); n.Enabled {
		t.Fatal("not switched off")
	}
	more := int64(500 << 30)
	must(f.s.SetNodeBilling(f.ctx, ActorCLI, nl.ID, NodeBillingInput{TrafficLimit: &more}))
	if n := must(store.GetNode(f.ctx, db, nl.ID)); !n.Enabled {
		t.Fatal("a raised limit did not bring the node back")
	}
}

// The bot reminds once, 5 days before the paid-until date; a new date reminds again.
func TestPaymentReminder(t *testing.T) {
	f := newFixture(t)
	nl, _ := must2(f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Name: "NL", Country: "nl", Domain: "nl.example.com"}))
	p := must(f.s.CreateProvider(f.ctx, ActorCLI, ProviderInput{Name: "Nimbus"}))
	cost, cur := 450.0, "₽"
	until := f.now.Add(6 * 24 * time.Hour).Unix()
	must(f.s.SetNodeBilling(f.ctx, ActorCLI, nl.ID, NodeBillingInput{ProviderID: &p.ID, Cost: &cost, Currency: &cur, PaidUntil: &until}))
	drainNotices(f.s)
	must0(t, f.s.Maintenance(f.ctx, false))
	if ns := drainNotices(f.s); len(ns) != 0 {
		t.Fatalf("reminded 6 days before: %+v", ns)
	}
	f.now = f.now.Add(25 * time.Hour)
	must0(t, f.s.Maintenance(f.ctx, false))
	ns := drainNotices(f.s)
	if len(ns) != 1 || !strings.Contains(ns[0].Text, "через 5 дн.") || !strings.Contains(ns[0].Text, "450 ₽ в месяц") || !strings.Contains(ns[0].Text, "Nimbus") {
		t.Fatalf("notices %+v", ns)
	}
	must0(t, f.s.Maintenance(f.ctx, false))
	if ns := drainNotices(f.s); len(ns) != 0 {
		t.Fatal("reminded twice")
	}
	later := until + 30*86400
	must(f.s.SetNodeBilling(f.ctx, ActorCLI, nl.ID, NodeBillingInput{PaidUntil: &later}))
	f.now = time.Unix(later, 0).Add(-2 * 24 * time.Hour)
	must0(t, f.s.Maintenance(f.ctx, false))
	if ns := drainNotices(f.s); len(ns) != 1 {
		t.Fatalf("a new date: %+v", ns)
	}
	// The provider goes, the node keeps its cost.
	must0(t, f.s.DeleteProvider(f.ctx, ActorCLI, p.ID))
	if b := must(store.GetNodeBilling(f.ctx, f.s.Store().DB, nl.ID)); b.ProviderID != nil || b.Cost != 450 {
		t.Fatalf("%+v", b)
	}
}

// The interface traffic is the metrics' rates times the time between samples.
func TestIfaceTrafficFromMetrics(t *testing.T) {
	f := newFixture(t)
	nl, _ := must2(f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Name: "NL", Country: "nl"}))
	for i, ts := range []int64{f.now.Unix(), f.now.Unix() + 20} {
		b := &nodev1.StatsBatch{Epoch: "e", Seq: uint64(i + 1), Ts: ts, Metrics: &nodev1.Metrics{RxBps: 1000, TxBps: 3000}}
		must0(t, f.s.IngestStats(f.ctx, nl.ID, b))
	}
	rx, tx, err := store.IfaceTraffic(f.ctx, f.s.Store().DB, nl.ID, f.now.Unix()-3600)
	if err != nil || rx != 1000*(10+20) || tx != 3000*(10+20) {
		t.Fatalf("rx %d tx %d %v", rx, tx, err)
	}
}

// A provider's name and icon come from its site.
func TestProviderFromSite(t *testing.T) {
	FetchPrivate = true
	t.Cleanup(func() { FetchPrivate = false })
	png := "\x89PNG\r\n\x1a\nfake"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`<html><head><title>Аренда VPS и выделенных серверов | Nimbus</title>
				<link rel="icon" href="/static/fav.png"></head><body>hi</body></html>`))
		case "/static/fav.png":
			_, _ = w.Write([]byte(png))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	f := newFixture(t)
	p := must(f.s.CreateProvider(f.ctx, ActorCLI, ProviderInput{URL: srv.URL, LoginURL: srv.URL + "/login"}))
	if p.Name != "Nimbus" || !strings.HasPrefix(p.Icon, "data:image/png;base64,") {
		t.Fatalf("%+v", p)
	}
	if _, err := f.s.CreateProvider(f.ctx, ActorCLI, ProviderInput{URL: "ftp://x"}); err == nil {
		t.Fatal("a bad link accepted")
	}
	// The panel does not look into its own server's networks.
	FetchPrivate = false
	if info := FetchSiteInfo(context.Background(), srv.URL); info.Name != "" || info.Icon != "" {
		t.Fatalf("fetched a loopback site: %+v", info)
	}
}

func TestCleanTitle(t *testing.T) {
	for _, c := range [][3]string{
		{"Аренда VPS и выделенных серверов | Nimbus", "nimbus.example", "Nimbus"},
		{"Nimbus — Аренда VPS", "nimbus.example", "Nimbus"},
		{"Hetzner Online GmbH - Dedicated Server, Cloud, Storage &amp; Hosting", "hetzner.com", "Hetzner Online GmbH"},
		{"Timeweb Cloud — облачная платформа", "timeweb.cloud", "Timeweb Cloud"},
		{"VPS hosting: fast servers", "example.com", "VPS hosting"}, // a tie: the first
		{"FirstByte", "firstbyte.ru", "FirstByte"},
	} {
		if got := cleanTitle(c[0], c[1]); got != c[2] {
			t.Errorf("%q (%s): %q, want %q", c[0], c[1], got, c[2])
		}
	}
	name, icons := readHead([]byte(`<head><meta property="og:site_name" content="DigitalOcean"><title>Cloud | x</title>
		<link rel="shortcut icon" href="/a.ico"><link rel="apple-touch-icon" sizes="180x180" href="/b.png"></head>`), "digitalocean.com")
	if name != "DigitalOcean" || len(icons) != 2 || icons[0] != "/b.png" {
		t.Fatalf("%q %v", name, icons)
	}
}

func must0(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// Raising the limit of a node the limit switched off does not wait on the database: the time
// zone is read before the transaction, even when the settings are not cached.
func TestRaiseLimitWithColdSettings(t *testing.T) {
	f := newFixture(t)
	nl, _ := must2(f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Name: "NL", Country: "nl", Domain: "nl.example.com"}))
	limit := int64(10 << 30)
	must(f.s.SetNodeBilling(f.ctx, ActorCLI, nl.ID, NodeBillingInput{TrafficLimit: &limit}))
	_ = store.AddIfaceTraffic(f.ctx, f.s.Store().DB, nl.ID, f.now.Unix(), 0, 10<<30)
	must0(t, f.s.Maintenance(f.ctx, false))
	if n := must(store.GetNode(f.ctx, f.s.Store().DB, nl.ID)); n.Enabled {
		t.Fatal("not switched off")
	}
	f.s.Invalidate()
	ctx, cancel := context.WithTimeout(f.ctx, 3*time.Second)
	defer cancel()
	more := int64(100 << 30)
	if _, err := f.s.SetNodeBilling(ctx, ActorCLI, nl.ID, NodeBillingInput{TrafficLimit: &more}); err != nil {
		t.Fatalf("raising the limit: %v", err)
	}
	if n := must(store.GetNode(f.ctx, f.s.Store().DB, nl.ID)); !n.Enabled {
		t.Fatal("not back on")
	}
}
