package service

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

// The «Аналитика» page: each user's traffic by profile, every device, every address users came
// from (docs/USER_GUIDE.md §7.1).

// InsightRanges are the periods of the traffic by profile, in days.
var InsightRanges = map[string]int{"7d": 7, "30d": 30, "90d": 90}

// ProfileSeries is the traffic of one profile (or cascade) per day of the period.
type ProfileSeries struct {
	Key    string // "p<profile id>", "c<cascade id>", "old" (before profiles were told apart), "gone"
	Label  string
	Kind   string // profile, cascade, other
	Total  int64
	Values []int64 // per day, up + down
}

// UserProfiles is a user's traffic in the period by profile key.
type UserProfiles struct {
	ID       int64
	Username string
	Total    int64
	By       map[string]int64
}

// ProfileTraffic is the traffic by profile over a period.
type ProfileTraffic struct {
	Range    string
	Days     []int64
	Profiles []ProfileSeries // the largest first
	Users    []UserProfiles  // the largest first; the one user when asked for one
}

// MaxInsightUsers caps the users listed in the traffic by profile.
const MaxInsightUsers = 500

// TrafficByProfile reads the traffic by profile for a period key (7d, 30d, 90d), of one user
// when userID > 0.
func (s *Service) TrafficByProfile(ctx context.Context, key string, userID int64) (*ProfileTraffic, error) {
	days, ok := InsightRanges[key]
	if !ok {
		return nil, invalid("unknown period %q (7d, 30d, 90d)", key)
	}
	q := s.st.DB
	to := store.Day(s.now().Unix()) + 86400
	from := to - int64(days)*86400
	out := &ProfileTraffic{Range: key}
	for d := from; d < to; d += 86400 {
		out.Days = append(out.Days, d)
	}
	label, err := s.refLabels(ctx)
	if err != nil {
		return nil, err
	}
	byDay, err := store.RefTrafficByDay(ctx, q, from, to, userID)
	if err != nil {
		return nil, err
	}
	series := map[string]*ProfileSeries{}
	for _, r := range byDay {
		l := label(r.Ref)
		p := series[l.Key]
		if p == nil {
			p = &ProfileSeries{Key: l.Key, Label: l.Label, Kind: l.Kind, Values: make([]int64, days)}
			series[l.Key] = p
		}
		if i := int((r.Day - from) / 86400); i >= 0 && i < days {
			p.Values[i] += r.Up + r.Down
			p.Total += r.Up + r.Down
		}
	}
	for _, p := range series {
		out.Profiles = append(out.Profiles, *p)
	}
	sort.Slice(out.Profiles, func(i, j int) bool {
		a, b := out.Profiles[i], out.Profiles[j]
		return a.Total > b.Total || a.Total == b.Total && a.Key < b.Key
	})

	byUser, err := store.RefTrafficByUser(ctx, q, from, to, userID)
	if err != nil {
		return nil, err
	}
	users := map[int64]*UserProfiles{}
	for _, r := range byUser {
		u := users[r.UserID]
		if u == nil {
			u = &UserProfiles{ID: r.UserID, Username: r.Username, By: map[string]int64{}}
			users[r.UserID] = u
		}
		u.By[label(r.Ref).Key] += r.Up + r.Down
		u.Total += r.Up + r.Down
	}
	for _, u := range users {
		out.Users = append(out.Users, *u)
	}
	sort.Slice(out.Users, func(i, j int) bool {
		a, b := out.Users[i], out.Users[j]
		return a.Total > b.Total || a.Total == b.Total && a.Username < b.Username
	})
	if len(out.Users) > MaxInsightUsers {
		out.Users = out.Users[:MaxInsightUsers]
	}
	return out, nil
}

type refLabel struct{ Key, Label, Kind string }

// refLabels names the refs of the traffic: an inbound counts for its profile, a cascade for
// itself.
func (s *Service) refLabels(ctx context.Context) (func(ref string) refLabel, error) {
	q := s.st.DB
	profiles, err := store.ListProfiles(ctx, q)
	if err != nil {
		return nil, err
	}
	pname := map[int64]string{}
	for _, p := range profiles {
		pname[p.ID] = p.Name
	}
	inbounds, err := store.ListNodeInbounds(ctx, q, 0, 0)
	if err != nil {
		return nil, err
	}
	profileOf := map[int64]int64{}
	for _, ni := range inbounds {
		profileOf[ni.ID] = ni.ProfileID
	}
	cascades, err := store.ListCascades(ctx, q)
	if err != nil {
		return nil, err
	}
	cname := map[int64]string{}
	for _, c := range cascades {
		cname[c.ID] = s.CascadeRemark(ctx, q, c)
	}
	gone := refLabel{"gone", "Удалённые профили", "other"}
	return func(ref string) refLabel {
		if ref == "" {
			return refLabel{"old", "До разбивки по профилям", "other"}
		}
		id, err := strconv.ParseInt(ref[1:], 10, 64)
		if err != nil {
			return gone
		}
		switch ref[0] {
		case 'i':
			if pid, ok := profileOf[id]; ok {
				if name, ok := pname[pid]; ok {
					return refLabel{"p" + strconv.FormatInt(pid, 10), name, "profile"}
				}
			}
		case 'c':
			if name, ok := cname[id]; ok {
				return refLabel{"c" + strconv.FormatInt(id, 10), name, "cascade"}
			}
		}
		return gone
	}, nil
}

// ---- addresses users came from ----

// ipWriteEvery is how often an address still online is written again: its last-seen time is
// as fine as that.
const ipWriteEvery = time.Minute

type ipLog struct {
	mu      sync.Mutex
	written map[string]time.Time // "<user>|<ip>" → when written
	swept   time.Time
}

// RecordIPs keeps the addresses of a node's online users (Xray email → addresses) for the
// «Аналитика» page; an address still online is written once a minute.
func (s *Service) RecordIPs(ctx context.Context, nodeID int64, online map[string][]string) error {
	now := s.now()
	type row struct {
		user int64
		ip   string
	}
	var due []row
	s.ipLog.mu.Lock()
	if s.ipLog.written == nil {
		s.ipLog.written = map[string]time.Time{}
	}
	if now.Sub(s.ipLog.swept) > 10*time.Minute {
		for k, t := range s.ipLog.written {
			if now.Sub(t) > ipWriteEvery {
				delete(s.ipLog.written, k)
			}
		}
		s.ipLog.swept = now
	}
	for email, ips := range online {
		id := EmailUser(email)
		if id == 0 {
			continue
		}
		for _, ip := range ips {
			ip = strings.Trim(ip, "[]")
			if ip == "" || s.IPs().Ours(ip) {
				continue
			}
			k := strconv.FormatInt(id, 10) + "|" + ip
			if t, ok := s.ipLog.written[k]; ok && now.Sub(t) < ipWriteEvery {
				continue
			}
			s.ipLog.written[k] = now
			due = append(due, row{id, ip})
		}
	}
	s.ipLog.mu.Unlock()
	if len(due) == 0 {
		return nil
	}
	return s.st.Tx(ctx, func(q store.DBTX) error {
		for _, r := range due {
			if err := store.RecordIP(ctx, q, r.user, r.ip, nodeID, now.Unix()); err != nil {
				return err
			}
		}
		return nil
	})
}

// ---- lists ----

// Page is one page of a list and how many match in all.
type Page[T any] struct {
	Items []T
	Total int
}

// pageBounds clamps a page request.
func pageBounds(limit, offset int) (int, int) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	return limit, max(offset, 0)
}

// AllDevices lists every user's devices matching a search, most recent first.
func (s *Service) AllDevices(ctx context.Context, search string, limit, offset int) (*Page[*store.UserDevice], error) {
	limit, offset = pageBounds(limit, offset)
	items, total, err := store.AllDevices(ctx, s.st.DB, search, limit, offset)
	if err != nil {
		return nil, err
	}
	return &Page[*store.UserDevice]{Items: items, Total: total}, nil
}

// AllIPs lists the addresses users came from (kept 90 days) matching a search, most recent first.
func (s *Service) AllIPs(ctx context.Context, search string, limit, offset int) (*Page[*store.UserIP], error) {
	limit, offset = pageBounds(limit, offset)
	items, total, err := store.AllIPs(ctx, s.st.DB, search, limit, offset)
	if err != nil {
		return nil, err
	}
	return &Page[*store.UserIP]{Items: items, Total: total}, nil
}

// Newcomers is how many devices and addresses appeared on each of the last 30 days.
type Newcomers struct {
	Days      []int64
	Devices   []int64
	IPs       []int64
	Platforms []store.Count
}

// RecentNewcomers counts the devices and addresses first seen on each of the last 30 days.
func (s *Service) RecentNewcomers(ctx context.Context) (*Newcomers, error) {
	q := s.st.DB
	const days = 30
	to := store.Day(s.now().Unix()) + 86400
	from := to - days*86400
	out := &Newcomers{Devices: make([]int64, days), IPs: make([]int64, days)}
	for d := from; d < to; d += 86400 {
		out.Days = append(out.Days, d)
	}
	fill := func(dst []int64, cs []store.DayCount) {
		for _, c := range cs {
			if i := int((c.Day - from) / 86400); i >= 0 && i < days {
				dst[i] = c.N
			}
		}
	}
	dev, err := store.NewDevicesByDay(ctx, q, from, to)
	if err != nil {
		return nil, err
	}
	fill(out.Devices, dev)
	ips, err := store.NewIPsByDay(ctx, q, from, to)
	if err != nil {
		return nil, err
	}
	fill(out.IPs, ips)
	if out.Platforms, err = store.DevicePlatforms(ctx, q); err != nil {
		return nil, err
	}
	return out, nil
}
