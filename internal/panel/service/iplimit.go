package service

import (
	"context"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

// SettingIPLimit is how many addresses a user may connect from at once, when the user has no
// own limit; 0 = unlimited (docs/USER_GUIDE.md §6.1).
const SettingIPLimit = "ip.default_limit"

// DefaultIPLimit is the limit until the setting is changed.
const DefaultIPLimit = 3

// IPTracker enforces the limit of addresses a user connects from at once
// (docs/USER_GUIDE.md §6.1). Nodes report who is online and from where every few seconds; the
// tracker adds up all nodes and decides which addresses are over the limit.
//
// Stability rules:
//   - An address counts once per user, however many servers it reaches: an app pinging every
//     server (some of which may be unreachable for it) is one address.
//   - An address takes a slot only once it stays connected for Confirm (two reports in a row):
//     the short connections of a ping test never take a slot and are never blocked.
//   - A slot is held for Grace after the address goes away, so a phone that drops its
//     connections for a moment keeps its place; the first addresses keep their slots, the
//     latecomers wait.
//   - IPv4 addresses and IPv6 /64 networks are counted apart: a phone with both is not two
//     devices.
//   - Addresses of our own nodes are ignored (an app checking servers through the tunnel).
type IPTracker struct {
	Confirm  time.Duration // how long an address stays before it takes a slot
	Grace    time.Duration // how long a slot is held after the address goes away
	Stale    time.Duration // a node's report older than this no longer counts (node gone)
	NotifyIn time.Duration // at most one notice per user in this time

	now func() time.Time

	mu       sync.Mutex
	reports  map[int64]nodeReport     // by node
	users    map[int64]*userAddresses // by user
	limits   map[int64]int            // users with their own limit
	defLimit int
	ignore   map[string]bool // our nodes' addresses
	blocked  map[int64][]string
	notified map[int64]time.Time
	// OnBlock is called (outside the lock) when a user gets an address blocked.
	OnBlock func(userID int64, addrs []string, count, limit int)
}

type nodeReport struct {
	at    time.Time
	users map[int64][]string
}

type userAddresses struct {
	keys map[string]*addrState
}

type addrState struct {
	first, last time.Time
	admitted    bool
	v6          bool
}

func newIPTracker(now func() time.Time) *IPTracker {
	return &IPTracker{
		Confirm: 8 * time.Second, Grace: 30 * time.Second, Stale: 30 * time.Second, NotifyIn: 6 * time.Hour,
		now: now, reports: map[int64]nodeReport{}, users: map[int64]*userAddresses{}, limits: map[int64]int{},
		defLimit: DefaultIPLimit, ignore: map[string]bool{}, blocked: map[int64][]string{}, notified: map[int64]time.Time{},
	}
}

// IPs is the panel's IP tracker.
func (s *Service) IPs() *IPTracker {
	s.ipOnce.Do(func() {
		s.ips = newIPTracker(func() time.Time { return s.now() })
		s.ips.OnBlock = s.onIPLimit
	})
	return s.ips
}

// onIPLimit records that a user went over the limit and tells the admins (at most once in
// NotifyIn per user).
func (s *Service) onIPLimit(userID int64, addrs []string, count, limit int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = store.AddEvent(ctx, s.st.DB, "user.ip_limit", userID, map[string]any{"addrs": addrs, "count": count, "limit": limit})
	name := "#" + strconv.FormatInt(userID, 10)
	if u, err := store.GetUser(ctx, s.st.DB, userID); err == nil {
		name = u.Username
	}
	s.notify(Notice{Kind: "ip-limit", User: name, Addrs: addrs, Count: count, Limit: limit})
}

// AddrKey is what an address counts as: an IPv4 address or an IPv6 /64 network ("" = not an
// address).
func AddrKey(ip string) (key string, v6 bool) {
	p := net.ParseIP(strings.Trim(ip, "[]"))
	if p == nil {
		return "", false
	}
	if v4 := p.To4(); v4 != nil {
		return v4.String(), false
	}
	n := &net.IPNet{IP: p.Mask(net.CIDRMask(64, 128)), Mask: net.CIDRMask(64, 128)}
	return n.String(), true
}

// EmailUser is the user behind an Xray email: "<id>" or "<id>.<suffix>" (0 = not a user).
func EmailUser(email string) int64 {
	head, _, _ := strings.Cut(email, ".")
	id, err := strconv.ParseInt(head, 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

// SetLimits replaces the limits: def for everyone, own for users with their own (0 = none).
func (t *IPTracker) SetLimits(def int, own map[int64]int) {
	t.mu.Lock()
	t.defLimit, t.limits = def, own
	t.mu.Unlock()
}

// SetIgnored replaces the addresses that never count (our nodes).
func (t *IPTracker) SetIgnored(ips []string) {
	m := make(map[string]bool, len(ips))
	for _, ip := range ips {
		if k, _ := AddrKey(ip); k != "" {
			m[k] = true
		}
	}
	t.mu.Lock()
	t.ignore = m
	t.mu.Unlock()
}

func (t *IPTracker) limitOf(userID int64) int {
	if l, ok := t.limits[userID]; ok {
		return l
	}
	return t.defLimit
}

// Report takes a node's list of online users (Xray email → addresses) and returns the blocks
// when they changed.
func (t *IPTracker) Report(nodeID int64, online map[string][]string) (blocks map[int64][]string, changed bool) {
	users := map[int64][]string{}
	for email, ips := range online {
		if id := EmailUser(email); id > 0 {
			users[id] = append(users[id], ips...)
		}
	}
	t.mu.Lock()
	t.reports[nodeID] = nodeReport{at: t.now(), users: users}
	t.mu.Unlock()
	return t.Evaluate()
}

// Forget drops a node's report (its session ended).
func (t *IPTracker) Forget(nodeID int64) {
	t.mu.Lock()
	delete(t.reports, nodeID)
	t.mu.Unlock()
}

// Evaluate recomputes every user's slots and returns the blocks, reporting whether they changed.
func (t *IPTracker) Evaluate() (map[int64][]string, bool) {
	now := t.now()
	type notice struct {
		user         int64
		addrs        []string
		count, limit int
	}
	var notices []notice

	t.mu.Lock()
	live := map[int64]map[string]bool{}
	for node, r := range t.reports {
		if now.Sub(r.at) > t.Stale {
			delete(t.reports, node)
			continue
		}
		for u, ips := range r.users {
			for _, ip := range ips {
				k, _ := AddrKey(ip)
				if k == "" || t.ignore[k] {
					continue
				}
				if live[u] == nil {
					live[u] = map[string]bool{}
				}
				live[u][k] = true
			}
		}
	}
	for u, keys := range live {
		ua := t.users[u]
		if ua == nil {
			ua = &userAddresses{keys: map[string]*addrState{}}
			t.users[u] = ua
		}
		for k := range keys {
			st := ua.keys[k]
			if st == nil {
				st = &addrState{first: now, v6: strings.Contains(k, ":")}
				ua.keys[k] = st
			}
			st.last = now
		}
	}
	blocked := map[int64][]string{}
	for u, ua := range t.users {
		for k, st := range ua.keys {
			if now.Sub(st.last) > t.Grace {
				delete(ua.keys, k)
			}
		}
		if len(ua.keys) == 0 {
			delete(t.users, u)
			continue
		}
		limit := t.limitOf(u)
		if limit <= 0 {
			for _, st := range ua.keys {
				st.admitted = true
			}
			continue
		}
		var over []string
		total := 0
		for _, v6 := range []bool{false, true} {
			var held int
			var waiting []string
			for k, st := range ua.keys {
				if st.v6 != v6 {
					continue
				}
				switch {
				case st.admitted:
					held++
				case live[u][k] && now.Sub(st.first) >= t.Confirm:
					waiting = append(waiting, k)
				}
			}
			// The first to come get the free slots.
			sort.Slice(waiting, func(i, j int) bool {
				a, b := ua.keys[waiting[i]], ua.keys[waiting[j]]
				if !a.first.Equal(b.first) {
					return a.first.Before(b.first)
				}
				return waiting[i] < waiting[j]
			})
			for _, k := range waiting {
				if held < limit {
					ua.keys[k].admitted = true
					held++
					continue
				}
				over = append(over, k)
			}
			if held > total {
				total = held
			}
		}
		if len(over) > 0 {
			sort.Strings(over)
			blocked[u] = over
			if !sameStrings(t.blocked[u], over) && now.Sub(t.notified[u]) >= t.NotifyIn {
				t.notified[u] = now
				notices = append(notices, notice{u, over, total + len(over), limit})
			}
		}
	}
	changed := !sameBlocks(t.blocked, blocked)
	t.blocked = blocked
	onBlock := t.OnBlock
	t.mu.Unlock()
	if onBlock != nil {
		for _, n := range notices {
			onBlock(n.user, n.addrs, n.count, n.limit)
		}
	}
	return copyBlocks(blocked), changed
}

// Blocks returns the current blocks.
func (t *IPTracker) Blocks() map[int64][]string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return copyBlocks(t.blocked)
}

// Verdict tells whether a user's address is over the limit: the subscription then shows the
// limit instead of the servers. count is how many addresses the user has (held and waiting).
func (t *IPTracker) Verdict(userID int64, ip string) (blocked bool, count, limit int) {
	k, _ := AddrKey(ip)
	t.mu.Lock()
	defer t.mu.Unlock()
	limit = t.limitOf(userID)
	if ua := t.users[userID]; ua != nil {
		count = len(ua.keys)
	}
	for _, b := range t.blocked[userID] {
		if b == k {
			return true, count, limit
		}
	}
	return false, count, limit
}

// Online returns a user's addresses with their state, for the panel.
func (t *IPTracker) Online(userID int64) []OnlineAddr {
	t.mu.Lock()
	defer t.mu.Unlock()
	ua := t.users[userID]
	if ua == nil {
		return nil
	}
	blocked := map[string]bool{}
	for _, b := range t.blocked[userID] {
		blocked[b] = true
	}
	out := make([]OnlineAddr, 0, len(ua.keys))
	for k, st := range ua.keys {
		out = append(out, OnlineAddr{Addr: k, Since: st.first.Unix(), Seen: st.last.Unix(), Admitted: st.admitted, Blocked: blocked[k]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Since < out[j].Since })
	return out
}

// OnlineAddr is one address of a user.
type OnlineAddr struct {
	Addr     string `json:"addr"`
	Since    int64  `json:"since"`
	Seen     int64  `json:"seen"`
	Admitted bool   `json:"admitted"`
	Blocked  bool   `json:"blocked"`
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameBlocks(a, b map[int64][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for u, x := range a {
		if !sameStrings(x, b[u]) {
			return false
		}
	}
	return true
}

func copyBlocks(m map[int64][]string) map[int64][]string {
	out := make(map[int64][]string, len(m))
	for u, x := range m {
		out[u] = append([]string(nil), x...)
	}
	return out
}

// RefreshIPLimits loads the limits and our nodes' addresses into the tracker (on every change).
func (s *Service) RefreshIPLimits(ctx context.Context) error {
	def := DefaultIPLimit
	if v, _ := s.Setting(ctx, SettingIPLimit, strconv.Itoa(DefaultIPLimit)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			def = n
		}
	}
	own, err := store.UserIPLimits(ctx, s.st.DB)
	if err != nil {
		return err
	}
	addrs, err := store.ListAddresses(ctx, s.st.DB, 0)
	if err != nil {
		return err
	}
	ips := make([]string, 0, len(addrs))
	for _, a := range addrs {
		ips = append(ips, a.IP)
	}
	t := s.IPs()
	t.SetLimits(def, own)
	t.SetIgnored(ips)
	return nil
}

// IPLimitOf is a user's effective limit (0 = unlimited).
func (s *Service) IPLimitOf(ctx context.Context, u *store.User) int {
	if u.IPLimit != nil {
		return int(*u.IPLimit)
	}
	v, _ := s.Setting(ctx, SettingIPLimit, strconv.Itoa(DefaultIPLimit))
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return DefaultIPLimit
	}
	return n
}
