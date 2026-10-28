package webapi

import (
	"context"
	"net/http"
	"strconv"

	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

// The «Аналитика» page (docs/USER_GUIDE.md §7.1).

func (s *Server) insightRoutes() {
	s.handle("GET /api/insights/traffic", s.insightTraffic)
	s.handle("GET /api/insights/devices", s.insightDevices)
	s.handle("GET /api/insights/ips", s.insightIPs)
	s.handle("GET /api/insights/newcomers", s.insightNewcomers)
}

// insightTraffic serves the traffic by profile: GET /api/insights/traffic?range=7d|30d|90d[&user=<id>].
func (s *Server) insightTraffic(r *http.Request) (any, error) {
	key := r.URL.Query().Get("range")
	if key == "" {
		key = "30d"
	}
	user, _ := strconv.ParseInt(r.URL.Query().Get("user"), 10, 64)
	t, err := s.svc.TrafficByProfile(r.Context(), key, user)
	if err != nil {
		return nil, err
	}
	profiles := make([]map[string]any, 0, len(t.Profiles))
	for _, p := range t.Profiles {
		profiles = append(profiles, map[string]any{"key": p.Key, "label": p.Label, "kind": p.Kind, "total": p.Total, "values": p.Values})
	}
	users := make([]map[string]any, 0, len(t.Users))
	for _, u := range t.Users {
		users = append(users, map[string]any{"id": u.ID, "username": u.Username, "total": u.Total, "by": u.By})
	}
	return localize(r, map[string]any{"range": t.Range, "days": t.Days, "profiles": profiles, "users": users}, "label"), nil
}

func pageArgs(r *http.Request) (search string, limit, offset int) {
	q := r.URL.Query()
	limit, _ = strconv.Atoi(q.Get("limit"))
	offset, _ = strconv.Atoi(q.Get("offset"))
	return q.Get("q"), limit, offset
}

// insightDevices serves every device: GET /api/insights/devices?q=&limit=&offset=.
func (s *Server) insightDevices(r *http.Request) (any, error) {
	search, limit, offset := pageArgs(r)
	p, err := s.svc.AllDevices(r.Context(), search, limit, offset)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(p.Items))
	for _, d := range p.Items {
		items = append(items, map[string]any{"id": d.ID, "userId": d.UserID, "username": d.Username, "hwid": d.HWID,
			"platform": d.Platform, "osVersion": d.OSVersion, "model": d.Model, "userAgent": d.UserAgent,
			"firstSeen": d.FirstSeen, "lastSeen": d.LastSeen, "lastIp": d.LastIP})
	}
	return map[string]any{"items": items, "total": p.Total}, nil
}

// insightIPs serves the addresses users came from: GET /api/insights/ips?q=&limit=&offset=.
func (s *Server) insightIPs(r *http.Request) (any, error) {
	search, limit, offset := pageArgs(r)
	p, err := s.svc.AllIPs(r.Context(), search, limit, offset)
	if err != nil {
		return nil, err
	}
	nodes := s.nodeNames(r.Context())
	items := make([]map[string]any, 0, len(p.Items))
	for _, i := range p.Items {
		items = append(items, map[string]any{"userId": i.UserID, "username": i.Username, "ip": i.IP, "node": nodes[i.NodeID],
			"firstAt": i.FirstAt, "lastAt": i.LastAt})
	}
	return map[string]any{"items": items, "total": p.Total}, nil
}

// nodeNames names nodes by id: the flag and the name.
func (s *Server) nodeNames(ctx context.Context) map[int64]string {
	out := map[int64]string{}
	nodes, err := store.ListNodes(ctx, s.svc.Store().DB)
	if err != nil {
		return out
	}
	for _, n := range nodes {
		out[n.ID] = xrayconf.CountryFlag(n.Country) + " " + n.Name
	}
	return out
}

// insightNewcomers serves the devices and addresses that appeared on each of the last 30 days.
func (s *Server) insightNewcomers(r *http.Request) (any, error) {
	n, err := s.svc.RecentNewcomers(r.Context())
	if err != nil {
		return nil, err
	}
	return map[string]any{"days": n.Days, "devices": n.Devices, "ips": n.IPs, "platforms": countsDTO(n.Platforms)}, nil
}
