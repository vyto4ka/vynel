package service

import (
	"context"
	"sync"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/store"
)

// The configuration a subscription reads (settings, the servers of every inbound, who may use
// which, cascades and merged subscriptions) changes rarely but is read on every request: it is
// kept in memory. Every change made through the service drops it at once; changes made around
// it (the CLI in another process, reports of nodes) are picked up within cacheTTL, and the
// reconciler drops it too when it sees another process's change.
const cacheTTL = 10 * time.Second

type confCache struct {
	mu  sync.Mutex
	gen int64 // bumped by Invalidate

	settings   map[string]string
	settingsAt time.Time
	settingsG  int64

	snap   *configSnapshot
	snapAt time.Time
	snapG  int64

	// Repeat requests of the same app write nothing: the last subscription fetch per user and
	// the devices known to be registered.
	touched map[int64]time.Time
	devices map[string]deviceSeen
	devG    int64
}

type deviceSeen struct {
	at    time.Time
	count int
}

// configSnapshot is what every subscription needs, user aside.
type configSnapshot struct {
	inbounds    []*store.NodeInbound     // enabled ones on enabled nodes
	access      map[int64]map[int64]bool // group → node inbound ids
	hosts       map[int64]Host           // node inbound id → its connection point (failed ones missing)
	userGrants  map[int64][]*store.Grant // by user
	groupGrants map[int64][]*store.Grant // by group
	cascades    map[int64]*cascadeSnap   // by id
	external    map[int64][]Host         // merged subscription id → its servers (enabled ones)
}

type cascadeSnap struct {
	c       *store.Cascade
	entry   Host // the entry's connection point; ok = it is usable
	ok      bool
	problem string // why it is not usable, for the panel
}

// Invalidate drops the cached configuration (after a change, or one made by another process).
func (s *Service) Invalidate() {
	s.cache.mu.Lock()
	s.cache.gen++
	s.cache.mu.Unlock()
}

// settingsMap returns all settings, from memory when fresh.
func (s *Service) settingsMap(ctx context.Context) (map[string]string, error) {
	c := &s.cache
	c.mu.Lock()
	if c.settings != nil && c.settingsG == c.gen && time.Since(c.settingsAt) < cacheTTL {
		m := c.settings
		c.mu.Unlock()
		return m, nil
	}
	gen := c.gen
	c.mu.Unlock()
	m, err := store.AllSettings(ctx, s.st.DB)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.gen == gen {
		c.settings, c.settingsAt, c.settingsG = m, time.Now(), gen
	}
	c.mu.Unlock()
	return m, nil
}

// snapshot returns the cached configuration snapshot, building it when stale (one builder at
// a time; the others wait for it).
func (s *Service) snapshot(ctx context.Context) (*configSnapshot, error) {
	s.snapMu.Lock()
	defer s.snapMu.Unlock()
	c := &s.cache
	c.mu.Lock()
	if c.snap != nil && c.snapG == c.gen && time.Since(c.snapAt) < cacheTTL {
		snap := c.snap
		c.mu.Unlock()
		return snap, nil
	}
	gen := c.gen
	c.mu.Unlock()
	snap, err := s.buildSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.gen == gen {
		c.snap, c.snapAt, c.snapG = snap, time.Now(), gen
	}
	c.mu.Unlock()
	return snap, nil
}

func (s *Service) buildSnapshot(ctx context.Context) (*configSnapshot, error) {
	q := s.st.DB
	inbounds, err := store.ListNodeInbounds(ctx, q, 0, 0)
	if err != nil {
		return nil, err
	}
	rules, err := store.ListAccessRules(ctx, q, 0)
	if err != nil {
		return nil, err
	}
	nodes, err := store.ListNodes(ctx, q)
	if err != nil {
		return nil, err
	}
	on := map[int64]bool{}
	for _, n := range nodes {
		on[n.ID] = n.Enabled
	}
	snap := &configSnapshot{hosts: map[int64]Host{}, cascades: map[int64]*cascadeSnap{}, external: map[int64][]Host{}}
	byID := map[int64]*store.NodeInbound{}
	for _, ni := range inbounds {
		byID[ni.ID] = ni
		if !ni.Enabled || !on[ni.NodeID] {
			continue
		}
		snap.inbounds = append(snap.inbounds, ni)
		if h, err := s.HostFor(ctx, ni); err == nil {
			snap.hosts[ni.ID] = h
		}
	}
	snap.access = ResolveAccess(rules, inbounds)
	grants, err := store.ListGrants(ctx, q)
	if err != nil {
		return nil, err
	}
	cascades, err := store.ListCascades(ctx, q)
	if err != nil {
		return nil, err
	}
	grants = withAllGrants(grants, rules, cascades)
	snap.userGrants, snap.groupGrants = map[int64][]*store.Grant{}, map[int64][]*store.Grant{}
	for _, g := range grants {
		if g.UserID != nil {
			snap.userGrants[*g.UserID] = append(snap.userGrants[*g.UserID], g)
		} else if g.GroupID != nil {
			snap.groupGrants[*g.GroupID] = append(snap.groupGrants[*g.GroupID], g)
		}
	}
	for _, c := range cascades {
		cs := &cascadeSnap{c: c}
		cs.c.Remark = s.CascadeRemark(ctx, q, c)
		// The same conditions as planCascades: a server in the subscription that the nodes do not
		// carry would not connect. Both ends must be on and give a connection point.
		entry, exit := byID[c.EntryInboundID], byID[c.ExitInboundID]
		_, exitOK := snap.hosts[c.ExitInboundID]
		h, entryOK := snap.hosts[c.EntryInboundID]
		switch {
		case !c.Enabled:
			cs.problem = "каскад выключен"
		case entry == nil || exit == nil:
			cs.problem = "инбаунд входа или выхода удалён"
		case !entry.Enabled || !on[entry.NodeID]:
			cs.problem = "вход выключен (инбаунд или нода)"
		case !exit.Enabled || !on[exit.NodeID]:
			cs.problem = "выход выключен (инбаунд или нода)"
		case !entryOK:
			cs.problem = "у инбаунда входа нет адреса для подписки — проверьте ноду"
		case !exitOK:
			cs.problem = "у инбаунда выхода нет адреса — проверьте ноду"
		default:
			cs.entry, cs.ok = h, true
			if h.Network == "xhttp" {
				cs.problem = "вход по XHTTP: в sing-box его нет, в Mihomo — только если профиль это разрешает"
			}
		}
		snap.cascades[c.ID] = cs
	}
	externals, err := store.ListExternalSubs(ctx, q)
	if err != nil {
		return nil, err
	}
	for _, e := range externals {
		if e.Enabled {
			snap.external[e.ID] = ParseLinks(e.Links)
		}
	}
	return snap, nil
}

// subscriptionTouched reports whether a fetch by this user was recorded less than a minute ago
// (the record is only for the panel: "last updated").
func (s *Service) subscriptionTouched(userID int64, now time.Time) bool {
	c := &s.cache
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.touched == nil || len(c.touched) > 200000 {
		c.touched = map[int64]time.Time{}
	}
	if t, ok := c.touched[userID]; ok && now.Sub(t) < time.Minute {
		return true
	}
	c.touched[userID] = now
	return false
}

// knownDevice reports a device registered for the user within the last 2 minutes (and no change
// since): its fetch needs no database work.
func (s *Service) knownDevice(userID int64, hwid string) (int, bool) {
	c := &s.cache
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.devG != c.gen {
		c.devices, c.devG = nil, c.gen
	}
	d, ok := c.devices[deviceKey(userID, hwid)]
	if !ok || time.Since(d.at) > 2*time.Minute {
		return 0, false
	}
	return d.count, true
}

func (s *Service) rememberDevice(userID int64, hwid string, count int) {
	c := &s.cache
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.devG != c.gen || c.devices == nil || len(c.devices) > 500000 {
		c.devices, c.devG = map[string]deviceSeen{}, c.gen
	}
	c.devices[deviceKey(userID, hwid)] = deviceSeen{at: time.Now(), count: count}
}

func deviceKey(userID int64, hwid string) string { return UserEmail(userID) + "|" + hwid }

// userInbounds are the inbounds the groups give, in node and inbound order.
func (snap *configSnapshot) userInbounds(groups []int64) []*store.NodeInbound {
	var out []*store.NodeInbound
	for _, ni := range snap.inbounds {
		for _, g := range groups {
			if snap.access[g][ni.ID] {
				out = append(out, ni)
				break
			}
		}
	}
	return out
}

// userHosts are the connection points of those inbounds (failed ones skipped).
func (snap *configSnapshot) userHosts(groups []int64) []Host {
	inbounds := snap.userInbounds(groups)
	out := make([]Host, 0, len(inbounds))
	for _, ni := range inbounds {
		if h, ok := snap.hosts[ni.ID]; ok {
			out = append(out, h)
		}
	}
	return out
}
