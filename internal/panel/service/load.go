package service

import (
	"context"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

// LoadRanges are the periods of the servers' load on the dashboard and their buckets: 5-minute
// rows (kept 90 days) merged so a chart has a few hundred points.
var LoadRanges = map[string]StatsRange{
	"24h": {"24h", 24 * time.Hour, 300},
	"7d":  {"7d", 7 * 24 * time.Hour, 1800},
	"30d": {"30d", 30 * 24 * time.Hour, 3 * 3600},
	"90d": {"90d", 90 * 24 * time.Hour, 12 * 3600},
}

// NodeLoad is one node's load over a period.
type NodeLoad struct {
	ID       int64
	Code     string
	Name     string
	Country  string
	MemTotal int64 // the latest
	Points   []store.LoadPoint
}

// Load is the servers' load over a period: CPU, steal, memory in bytes, bandwidth.
type Load struct {
	Range    string
	Bucket   int64
	From, To int64
	Nodes    []NodeLoad
}

// ServerLoad reads the servers' load for a period key (24h, 7d, 30d, 90d).
func (s *Service) ServerLoad(ctx context.Context, key string) (*Load, error) {
	rg, ok := LoadRanges[key]
	if !ok {
		return nil, invalid("unknown period %q (24h, 7d, 30d, 90d)", key)
	}
	now := s.now().Unix()
	to := now - now%rg.Bucket + rg.Bucket
	from := to - int64(rg.Span/time.Second)
	pts, err := store.LoadSeries(ctx, s.st.DB, from, to, rg.Bucket)
	if err != nil {
		return nil, err
	}
	nodes, err := store.ListNodes(ctx, s.st.DB)
	if err != nil {
		return nil, err
	}
	by := map[int64][]store.LoadPoint{}
	for _, p := range pts {
		by[p.NodeID] = append(by[p.NodeID], p)
	}
	out := &Load{Range: rg.Key, Bucket: rg.Bucket, From: from, To: to}
	for _, n := range nodes {
		nl := NodeLoad{ID: n.ID, Code: n.Code, Name: n.Name, Country: n.Country, Points: by[n.ID]}
		if l := len(nl.Points); l > 0 {
			nl.MemTotal = nl.Points[l-1].MemTotal
		}
		out.Nodes = append(out.Nodes, nl)
	}
	return out, nil
}
