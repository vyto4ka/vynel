package service

import (
	"context"
	"strconv"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/store"
	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
)

// OnlineWindow is how recently a user must have been seen to count as online.
const OnlineWindow = 2 * time.Minute

// IngestStats stores one stats batch of a node. It is idempotent per (epoch, seq): duplicates
// (resent after a lost ack) are ignored. Users who cross their traffic limit become "limited"
// in the same transaction, and the reconciler then removes them from every node.
func (s *Service) IngestStats(ctx context.Context, nodeID int64, b *nodev1.StatsBatch) error {
	changed := false
	err := s.st.Tx(ctx, func(q store.DBTX) error {
		n, err := store.GetNode(ctx, q, nodeID)
		if err != nil {
			return err
		}
		if b.Epoch == n.StatsEpoch && int64(b.Seq) <= n.StatsSeq {
			return nil // duplicate
		}
		ts := b.Ts
		if ts <= 0 {
			ts = s.now().Unix()
		}
		touched := map[int64]bool{}
		for _, u := range b.Users {
			id, err := strconv.ParseInt(u.Email, 10, 64)
			if err != nil {
				continue // not one of ours
			}
			ok, err := store.AddUserTraffic(ctx, q, id, nodeID, ts, u.Up, u.Down)
			if err != nil {
				return err
			}
			if ok {
				touched[id] = true
			}
		}
		for _, o := range b.Online {
			if id, err := strconv.ParseInt(o.Email, 10, 64); err == nil {
				if err := store.MarkOnline(ctx, q, id, ts); err != nil {
					return err
				}
			}
		}
		if b.NodeUp != 0 || b.NodeDown != 0 {
			if err := store.AddNodeTraffic(ctx, q, nodeID, ts, b.NodeUp, b.NodeDown); err != nil {
				return err
			}
		}
		if m := b.Metrics; m != nil {
			if err := store.AddMetrics(ctx, q, store.Metrics{
				NodeID: nodeID, TS: ts, CPU: m.Cpu, MemUsed: int64(m.MemUsed), MemTotal: int64(m.MemTotal), Load1: m.Load1,
				RxBps: int64(m.RxBps), TxBps: int64(m.TxBps), Uptime: int64(m.Uptime), Online: int64(len(b.Online)),
			}); err != nil {
				return err
			}
		}
		now := s.now()
		for id := range touched {
			u, err := store.GetUser(ctx, q, id)
			if err != nil {
				return err
			}
			st := computeStatus(u, now)
			if st == u.Status {
				continue
			}
			if err := store.SetUserStatus(ctx, q, id, st); err != nil {
				return err
			}
			if err := store.AddAudit(ctx, q, store.AuditEntry{Actor: "system", Action: "user.status", Entity: "user", EntityID: id,
				Diff: `{"from":"` + u.Status + `","to":"` + st + `"}`}); err != nil {
				return err
			}
			if err := store.AddEvent(ctx, q, EvUserChanged, id, nil); err != nil {
				return err
			}
			changed = true
		}
		if err := store.SetStatsCursor(ctx, q, nodeID, b.Epoch, int64(b.Seq)); err != nil {
			return err
		}
		return store.TouchNode(ctx, q, nodeID)
	})
	if err == nil && changed && s.OnChange != nil {
		s.OnChange()
	}
	return err
}

// periodStart is the start of the current reset period in the server's local time.
func periodStart(now time.Time, strategy string) (time.Time, bool) {
	y, m, d := now.Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	switch strategy {
	case "day":
		return day, true
	case "week":
		offset := (int(day.Weekday()) + 6) % 7 // Monday = 0
		return day.AddDate(0, 0, -offset), true
	case "month":
		return time.Date(y, m, 1, 0, 0, 0, 0, now.Location()), true
	}
	return time.Time{}, false
}

// ResetDueTraffic zeroes period counters of users whose reset period has started since their
// last reset (or creation). It returns how many users were reset.
func (s *Service) ResetDueTraffic(ctx context.Context) (int, error) {
	users, err := store.UsersDueReset(ctx, s.st.DB)
	if err != nil {
		return 0, err
	}
	now := s.now()
	n := 0
	for _, u := range users {
		start, ok := periodStart(now, u.ResetStrategy)
		if !ok {
			continue
		}
		baseline := u.CreatedAt
		if u.LastResetAt != nil && *u.LastResetAt > baseline {
			baseline = *u.LastResetAt
		}
		if baseline >= start.Unix() {
			continue
		}
		if _, err := s.updateUser(ctx, ActorSystem, u.ID, "user.auto_reset", map[string]string{"strategy": u.ResetStrategy}, func(_ store.DBTX, u *store.User) error {
			u.TrafficUsedBytes = 0
			ts := now.Unix()
			u.LastResetAt = &ts
			return nil
		}); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// Maintenance runs the periodic jobs: status refresh (expiry), traffic resets, pruning.
func (s *Service) Maintenance(ctx context.Context, prune bool) error {
	if _, err := s.RefreshStatuses(ctx); err != nil {
		return err
	}
	if _, err := s.ResetDueTraffic(ctx); err != nil {
		return err
	}
	if prune {
		return store.PruneStats(ctx, s.st.DB, s.now().Unix())
	}
	return nil
}

// Overview is the dashboard summary.
type Overview struct {
	UsersByStatus map[string]int
	OnlineNow     int
	TodayBytes    int64
	MonthBytes    int64
	TopUsers      []store.UserTotal
}

// Overview computes the dashboard summary.
func (s *Service) Overview(ctx context.Context) (*Overview, error) {
	q := s.st.DB
	now := s.now()
	o := &Overview{}
	var err error
	if o.UsersByStatus, err = store.CountUsersByStatus(ctx, q); err != nil {
		return nil, err
	}
	if o.OnlineNow, err = store.CountOnlineSince(ctx, q, now.Add(-OnlineWindow).Unix()); err != nil {
		return nil, err
	}
	up, down, err := store.TrafficSince(ctx, q, now.Unix())
	if err != nil {
		return nil, err
	}
	o.TodayBytes = up + down
	up, down, err = store.TrafficSince(ctx, q, now.AddDate(0, 0, -30).Unix())
	if err != nil {
		return nil, err
	}
	o.MonthBytes = up + down
	o.TopUsers, err = store.TopUsersSince(ctx, q, now.AddDate(0, 0, -30).Unix(), 10)
	return o, err
}

// NodeStatus is a node with live numbers.
type NodeStatus struct {
	Node       *store.Node
	Connected  bool
	Metrics    *store.Metrics
	TodayBytes int64
}

// NodeStatuses returns every node with its latest metrics and today's traffic.
// connected may be nil (e.g. from the CLI, which cannot see live sessions).
func (s *Service) NodeStatuses(ctx context.Context, connected func(int64) bool) ([]NodeStatus, error) {
	nodes, err := store.ListNodes(ctx, s.st.DB)
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := make([]NodeStatus, 0, len(nodes))
	for _, n := range nodes {
		ns := NodeStatus{Node: n}
		if connected != nil {
			ns.Connected = connected(n.ID)
		} else {
			ns.Connected = n.LastSeenAt != nil && now.Unix()-*n.LastSeenAt < 90
		}
		if ns.Metrics, err = store.LatestMetrics(ctx, s.st.DB, n.ID); err != nil {
			return nil, err
		}
		day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Unix()
		up, down, err := store.NodeTrafficSince(ctx, s.st.DB, n.ID, day)
		if err != nil {
			return nil, err
		}
		ns.TodayBytes = up + down
		out = append(out, ns)
	}
	return out, nil
}

// UserTrafficByNode returns a user's traffic per node over the last days.
func (s *Service) UserTrafficByNode(ctx context.Context, userID int64, days int) ([]store.NodeTotal, error) {
	return store.UserTrafficByNode(ctx, s.st.DB, userID, s.now().AddDate(0, 0, -days).Unix())
}
