package webapi

import (
	"math"
	"net/http"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

// stats serves the statistics page: GET /api/stats?range=24h|7d|30d|90d.
func (s *Server) stats(r *http.Request) (any, error) {
	key := r.URL.Query().Get("range")
	if key == "" {
		key = "7d"
	}
	st, err := s.svc.Statistics(r.Context(), key)
	if err != nil {
		return nil, err
	}
	traffic := make([][3]int64, 0, len(st.Traffic))
	for _, p := range st.Traffic {
		traffic = append(traffic, [3]int64{p.T, p.Down, p.Up})
	}
	nodes := make([]map[string]any, 0, len(st.Nodes))
	for _, n := range st.Nodes {
		nodes = append(nodes, map[string]any{"id": n.ID, "code": n.Code, "name": n.Name, "flag": n.Country, "total": n.Total,
			"traffic": points(n.Traffic), "cpu": points(n.CPU), "mem": points(n.Mem)})
	}
	top := make([]map[string]any, 0, len(st.TopUsers))
	for _, t := range st.TopUsers {
		top = append(top, map[string]any{"id": t.UserID, "username": t.Username, "bytes": t.Bytes})
	}
	return localize(r, map[string]any{
		"range": st.Range, "bucket": st.Bucket, "from": st.From, "to": st.To, "timeZone": st.TimeZone,
		"traffic": traffic, "total": st.Total, "prevTotal": st.PrevTotal,
		"activeUsers": st.ActiveUsers, "prevActive": st.PrevActive, "peakOnline": st.PeakOnline, "onlineNow": st.OnlineNow,
		"online": points(st.Online), "nodes": nodes, "topUsers": top, "heatmap": st.Heatmap,
		"platforms": countsDTO(st.Platforms), "apps": countsDTO(st.Apps), "metricsSince": st.MetricsSince, "metricsBucket": st.MetricsBucket,
		"users": map[string]int{"active": st.UsersBy[store.StatusActive], "limited": st.UsersBy[store.StatusLimited],
			"expired": st.UsersBy[store.StatusExpired], "disabled": st.UsersBy[store.StatusDisabled]},
	}, "label"), nil
}

// points encodes a series compactly: [t, value] or [t, null] where nothing was recorded.
func points(ps []service.ValuePoint) [][2]any {
	out := make([][2]any, 0, len(ps))
	for _, p := range ps {
		if p.Has {
			out = append(out, [2]any{p.T, p.V})
		} else {
			out = append(out, [2]any{p.T, nil})
		}
	}
	return out
}

func countsDTO(cs []store.Count) []map[string]any {
	out := make([]map[string]any, 0, len(cs))
	for _, c := range cs {
		out = append(out, map[string]any{"label": c.Label, "n": c.N})
	}
	return out
}

// serverLoad serves the dashboard's «Серверы»: GET /api/stats/load?range=24h|7d|30d|90d. Each
// point is [t, cpu %, cpu max %, steal %, memory bytes, rx B/s, tx B/s, rx max, tx max].
func (s *Server) serverLoad(r *http.Request) (any, error) {
	key := r.URL.Query().Get("range")
	if key == "" {
		key = "24h"
	}
	l, err := s.svc.ServerLoad(r.Context(), key)
	if err != nil {
		return nil, err
	}
	nodes := make([]map[string]any, 0, len(l.Nodes))
	for _, n := range l.Nodes {
		pts := make([][9]float64, 0, len(n.Points))
		for _, p := range n.Points {
			pts = append(pts, [9]float64{float64(p.T), round1(p.CPU), round1(p.CPUMax), round1(p.Steal), float64(p.MemUsed),
				float64(p.RxBps), float64(p.TxBps), float64(p.RxMax), float64(p.TxMax)})
		}
		nodes = append(nodes, map[string]any{"id": n.ID, "code": n.Code, "name": n.Name, "flag": xrayconf.CountryFlag(n.Country),
			"memTotal": n.MemTotal, "points": pts})
	}
	return map[string]any{"range": l.Range, "bucket": l.Bucket, "from": l.From, "to": l.To, "nodes": nodes}, nil
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
