package service

import (
	"context"
	"testing"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

func TestStatistics(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New(st)
	if err := s.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	node, _, err := s.CreateNode(ctx, ActorCLI, NodeInput{Name: "Нидерланды", Country: "nl"})
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.CreateUser(ctx, ActorCLI, CreateUserInput{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	_ = store.TouchSubscription(ctx, st.DB, u.ID, now, "Happ/3.2.1/android")
	err = st.Tx(ctx, func(q store.DBTX) error {
		for _, x := range []struct{ ago, up, down int64 }{{0, 10, 100}, {2 * 3600, 5, 50}, {3 * 86400, 1, 9}, {10 * 86400, 2, 18}} {
			if _, err := store.AddUserTraffic(ctx, q, u.ID, node.ID, now-x.ago, x.up, x.down); err != nil {
				return err
			}
			if err := store.AddNodeTraffic(ctx, q, node.ID, now-x.ago, x.up, x.down); err != nil {
				return err
			}
		}
		return store.AddMetrics(ctx, q, store.Metrics{NodeID: node.ID, TS: now, // the current hour, whatever the minute
			CPU: 40, MemUsed: 1, MemTotal: 4, Online: 7})
	})
	if err != nil {
		t.Fatal(err)
	}

	day, err := s.Statistics(ctx, "24h")
	if err != nil {
		t.Fatal(err)
	}
	if len(day.Traffic) != 24 || day.Total != 165 || day.Bucket != 3600 {
		t.Fatalf("24h: %d points, total %d", len(day.Traffic), day.Total)
	}
	if day.PeakOnline != 7 || len(day.Nodes) != 1 || day.Nodes[0].Total != 165 {
		t.Fatalf("24h nodes/online: %+v peak %d", day.Nodes, day.PeakOnline)
	}
	last := day.Nodes[0].CPU[len(day.Nodes[0].CPU)-1]
	if !last.Has || last.V != 40 || day.Nodes[0].Mem[len(day.Nodes[0].Mem)-1].V != 25 {
		t.Fatalf("metrics: %+v", last)
	}

	week, err := s.Statistics(ctx, "7d")
	if err != nil {
		t.Fatal(err)
	}
	if week.Total != 175 || week.PrevTotal != 20 {
		t.Fatalf("7d total %d prev %d (the previous week comes from the daily table)", week.Total, week.PrevTotal)
	}
	q, err := s.Statistics(ctx, "90d")
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Traffic) != 90 || q.Total != 195 || q.MetricsBucket != 3600 || len(q.Online) != 7*24 {
		t.Fatalf("90d: %d points total %d metrics bucket %d online %d", len(q.Traffic), q.Total, q.MetricsBucket, len(q.Online))
	}
	if len(q.Apps) != 1 || q.Apps[0].Label != "Happ" || len(q.TopUsers) != 1 {
		t.Fatalf("apps %+v top %+v", q.Apps, q.TopUsers)
	}
	var heat int64
	for _, row := range q.Heatmap {
		for _, v := range row {
			heat += v
		}
	}
	if heat != 175 {
		t.Fatalf("heatmap sums %d, want the last 7 days", heat)
	}
	if _, err := s.Statistics(ctx, "1y"); err == nil {
		t.Fatal("unknown period accepted")
	}
}
