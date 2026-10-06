package service

import (
	"testing"
	"time"

	"github.com/vyto4ka/vynnel/internal/panel/store"
	nodev1 "github.com/vyto4ka/vynnel/internal/proto/vynnel/node/v1"
)

func TestIngestStatsIdempotentAndLimits(t *testing.T) {
	f := newFixture(t)
	nl, _ := must2(f.s.CreateNode(f.ctx, ActorCLI, NodeInput{Name: "NL", Country: "nl"}))
	limit := int64(1000)
	u := must(f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: "u", TrafficLimitBytes: &limit}))
	changes := 0
	f.s.OnChange = func() { changes++ }

	batch := &nodev1.StatsBatch{Epoch: "e1", Seq: 1, Ts: f.now.Unix(),
		Users:  []*nodev1.UserTraffic{{Email: UserEmail(u.ID), Up: 100, Down: 300}, {Email: "999", Down: 5}, {Email: "not-ours", Down: 5}},
		Online: []*nodev1.OnlineUser{{Email: UserEmail(u.ID), Ips: 2}},
		NodeUp: 100, NodeDown: 310,
		Metrics: &nodev1.Metrics{Cpu: 12.5, MemTotal: 1 << 30, MemUsed: 1 << 29},
	}
	for i := 0; i < 2; i++ { // the second delivery is a duplicate
		if err := f.s.IngestStats(f.ctx, nl.ID, batch); err != nil {
			t.Fatal(err)
		}
	}
	u = must(f.s.User(f.ctx, u.ID))
	if u.TrafficUsedBytes != 400 || u.LifetimeUsedBytes != 400 || u.OnlineAt == nil {
		t.Fatalf("used %d lifetime %d online %v", u.TrafficUsedBytes, u.LifetimeUsedBytes, u.OnlineAt)
	}
	if changes != 0 {
		t.Fatal("no status change yet")
	}

	// Crossing the limit makes the user limited and notifies the reconciler.
	batch2 := &nodev1.StatsBatch{Epoch: "e1", Seq: 2, Ts: f.now.Unix(), Users: []*nodev1.UserTraffic{{Email: UserEmail(u.ID), Down: 700}}}
	if err := f.s.IngestStats(f.ctx, nl.ID, batch2); err != nil {
		t.Fatal(err)
	}
	if u = must(f.s.User(f.ctx, u.ID)); u.Status != store.StatusLimited {
		t.Fatalf("want limited, got %s (%d)", u.Status, u.TrafficUsedBytes)
	}
	if changes != 1 {
		t.Fatalf("OnChange calls %d", changes)
	}

	// A new epoch (node state reset) starts over at seq 1 and is accepted.
	batch3 := &nodev1.StatsBatch{Epoch: "e2", Seq: 1, Ts: f.now.Unix(), Users: []*nodev1.UserTraffic{{Email: UserEmail(u.ID), Down: 1}}}
	if err := f.s.IngestStats(f.ctx, nl.ID, batch3); err != nil {
		t.Fatal(err)
	}
	if u = must(f.s.User(f.ctx, u.ID)); u.TrafficUsedBytes != 1101 {
		t.Fatalf("new epoch not ingested: %d", u.TrafficUsedBytes)
	}

	o := must(f.s.Overview(f.ctx))
	if o.TodayBytes != 1101 || o.OnlineNow != 1 || len(o.TopUsers) != 1 {
		t.Fatalf("overview %+v", o)
	}
	ns := must(f.s.NodeStatuses(f.ctx, nil))
	if ns[0].TodayBytes != 410 || ns[0].Metrics == nil || ns[0].Metrics.CPU != 12.5 {
		t.Fatalf("node status %+v %+v", ns[0], ns[0].Metrics)
	}
	byNode := must(f.s.UserTrafficByNode(f.ctx, u.ID, 30))
	if len(byNode) != 1 || byNode[0].Code != "NL" || byNode[0].Bytes != 1101 {
		t.Fatalf("by node %+v", byNode)
	}
}

func TestResetDueTraffic(t *testing.T) {
	f := newFixture(t)
	f.now = time.Date(2026, 10, 14, 12, 0, 0, 0, time.Local) // Wednesday
	limit := int64(100)
	mk := func(name, strategy string) *store.User {
		u := must(f.s.CreateUser(f.ctx, ActorCLI, CreateUserInput{Username: name, TrafficLimitBytes: &limit}))
		u.ResetStrategy, u.TrafficUsedBytes = strategy, 500
		if err := store.UpdateUser(f.ctx, f.s.Store().DB, u); err != nil {
			t.Fatal(err)
		}
		return u
	}
	day, week, month, never := mk("d", "day"), mk("w", "week"), mk("m", "month"), mk("n", "no")
	used := func(u *store.User) int64 { return must(f.s.User(f.ctx, u.ID)).TrafficUsedBytes }

	// Same day as creation: nothing is due.
	if n := must(f.s.ResetDueTraffic(f.ctx)); n != 0 {
		t.Fatalf("reset %d on creation day", n)
	}
	f.now = f.now.AddDate(0, 0, 1) // Thursday
	must(f.s.ResetDueTraffic(f.ctx))
	if used(day) != 0 || used(week) != 500 || used(month) != 500 || used(never) != 500 {
		t.Fatal("only the daily user resets on the next day")
	}
	if st := must(f.s.User(f.ctx, day.ID)).Status; st != store.StatusActive {
		t.Fatalf("reset must lift the limit, status %s", st)
	}
	f.now = time.Date(2026, 10, 19, 0, 30, 0, 0, time.Local) // next Monday
	must(f.s.ResetDueTraffic(f.ctx))
	if used(week) != 0 || used(month) != 500 {
		t.Fatal("weekly resets on Monday")
	}
	f.now = time.Date(2026, 11, 1, 0, 30, 0, 0, time.Local)
	must(f.s.ResetDueTraffic(f.ctx))
	if used(month) != 0 || used(never) != 500 {
		t.Fatal("monthly resets on the 1st, 'no' never")
	}
	// Running again in the same period does nothing.
	if n := must(f.s.ResetDueTraffic(f.ctx)); n != 0 {
		t.Fatalf("double reset %d", n)
	}
}
