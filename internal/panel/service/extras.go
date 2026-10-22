package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

// ---- cascades (docs/USER_GUIDE.md §5.2) ----

// CascadeInput creates or edits a cascade.
type CascadeInput struct {
	Name           string
	EntryInboundID int64 // where clients connect (node 1)
	ExitInboundID  int64 // where traffic leaves (node 2)
	Remark         string
	Enabled        bool
}

// CascadeTag is the outbound of a cascade on its entry node.
func CascadeTag(id int64) string { return "CASCADE_" + strconv.FormatInt(id, 10) }

// CascadeEmail is a user's identity on a cascade's entry inbound: the user's id, so traffic and
// addresses count for the user, and the cascade, so the entry node routes it to the exit.
func CascadeEmail(userID, cascadeID int64) string {
	return UserEmail(userID) + ".c" + strconv.FormatInt(cascadeID, 10)
}

// cascadeServiceEmail is the entry node's user on the exit inbound (not a user of ours).
func cascadeServiceEmail(cascadeID int64) string { return "c" + strconv.FormatInt(cascadeID, 10) }

// CascadeUUID is a user's secret on a cascade: derived from the user's UUID, so a new UUID
// (a leaked key) changes it too.
func CascadeUUID(userUUID string, cascadeID int64) string {
	sum := sha256.Sum256([]byte(userUUID + "/cascade/" + strconv.FormatInt(cascadeID, 10)))
	b := sum[:16]
	b[6] = b[6]&0x0f | 0x50 // version 5 (name-based)
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func (s *Service) checkCascade(ctx context.Context, q store.DBTX, in *CascadeInput) error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return invalid("cascade name is required")
	}
	entry, err := store.GetNodeInbound(ctx, q, in.EntryInboundID)
	if err != nil {
		return fmt.Errorf("entry: %w", err)
	}
	exit, err := store.GetNodeInbound(ctx, q, in.ExitInboundID)
	if err != nil {
		return fmt.Errorf("exit: %w", err)
	}
	if entry.NodeID == exit.NodeID {
		return invalid("the entry and the exit must be on different nodes")
	}
	for _, ni := range []*store.NodeInbound{entry, exit} {
		r, err := s.renderNodeInbound(ctx, q, ni)
		if err != nil {
			return invalid("inbound %s: %v", ni.Tag, err)
		}
		if r.Protocol != "vless" && r.Protocol != "hysteria" {
			return invalid("inbound %s: %s cannot be part of a cascade", ni.Tag, r.Protocol)
		}
	}
	return nil
}

// CreateCascade adds a cascade.
func (s *Service) CreateCascade(ctx context.Context, actor Actor, in CascadeInput) (*store.Cascade, error) {
	id, err := xrayconf.NewUUID()
	if err != nil {
		return nil, err
	}
	c := &store.Cascade{ServiceUUID: id}
	err = s.mutate(ctx, change{actor: actor, action: "cascade.create", entity: "cascade", entityID: idOf(&c.ID), event: EvInboundChanged, diff: in},
		func(q store.DBTX) error {
			if err := s.checkCascade(ctx, q, &in); err != nil {
				return err
			}
			c.Name, c.EntryInboundID, c.ExitInboundID, c.Remark, c.Enabled = in.Name, in.EntryInboundID, in.ExitInboundID, strings.TrimSpace(in.Remark), in.Enabled
			return store.CreateCascade(ctx, q, c)
		})
	return c, err
}

// UpdateCascade changes a cascade.
func (s *Service) UpdateCascade(ctx context.Context, actor Actor, id int64, in CascadeInput) (*store.Cascade, error) {
	var c *store.Cascade
	err := s.mutate(ctx, change{actor: actor, action: "cascade.update", entity: "cascade", entityID: func() int64 { return id }, event: EvInboundChanged, diff: in},
		func(q store.DBTX) error {
			var err error
			if c, err = store.GetCascade(ctx, q, id); err != nil {
				return err
			}
			if err := s.checkCascade(ctx, q, &in); err != nil {
				return err
			}
			c.Name, c.EntryInboundID, c.ExitInboundID, c.Remark, c.Enabled = in.Name, in.EntryInboundID, in.ExitInboundID, strings.TrimSpace(in.Remark), in.Enabled
			return store.UpdateCascade(ctx, q, c)
		})
	return c, err
}

// DeleteCascade removes a cascade.
func (s *Service) DeleteCascade(ctx context.Context, actor Actor, id int64) error {
	return s.mutate(ctx, change{actor: actor, action: "cascade.delete", entity: "cascade", entityID: func() int64 { return id }, event: EvInboundChanged},
		func(q store.DBTX) error { return store.DeleteCascade(ctx, q, id) })
}

// Cascades lists cascades.
func (s *Service) Cascades(ctx context.Context) ([]*store.Cascade, error) {
	return store.ListCascades(ctx, s.st.DB)
}

// CascadeRemark is a cascade's name in apps.
func (s *Service) CascadeRemark(ctx context.Context, q store.DBTX, c *store.Cascade) string {
	if c.Remark != "" {
		return c.Remark
	}
	name := func(niID int64) string {
		ni, err := store.GetNodeInbound(ctx, q, niID)
		if err != nil {
			return "?"
		}
		n, err := store.GetNode(ctx, q, ni.NodeID)
		if err != nil {
			return "?"
		}
		return strings.TrimSpace(xrayconf.CountryFlag(n.Country) + " " + n.Name)
	}
	return name(c.EntryInboundID) + " → " + name(c.ExitInboundID)
}

// ---- external subscriptions (docs/USER_GUIDE.md §5.3) ----

// ExternalSubInput creates or edits an external subscription.
type ExternalSubInput struct {
	Name    string
	URL     string
	Enabled bool
}

func checkExternal(in *ExternalSubInput) error {
	in.Name, in.URL = strings.TrimSpace(in.Name), strings.TrimSpace(in.URL)
	if in.Name == "" {
		return invalid("name is required")
	}
	if !strings.HasPrefix(in.URL, "https://") && !strings.HasPrefix(in.URL, "http://") {
		return invalid("the link must start with https://")
	}
	return nil
}

// CreateExternalSub adds an external subscription; its servers are fetched right away.
func (s *Service) CreateExternalSub(ctx context.Context, actor Actor, in ExternalSubInput) (*store.ExternalSub, error) {
	if err := checkExternal(&in); err != nil {
		return nil, err
	}
	e := &store.ExternalSub{Name: in.Name, URL: in.URL, Enabled: in.Enabled}
	err := s.mutate(ctx, change{actor: actor, action: "external.create", entity: "external_sub", entityID: idOf(&e.ID), event: EvUserChanged,
		diff: map[string]string{"name": in.Name}},
		func(q store.DBTX) error { return store.CreateExternalSub(ctx, q, e) })
	if err != nil {
		return nil, err
	}
	_ = s.RefreshExternalSub(ctx, e.ID)
	return store.GetExternalSub(ctx, s.st.DB, e.ID)
}

// UpdateExternalSub changes an external subscription (a new link is fetched right away).
func (s *Service) UpdateExternalSub(ctx context.Context, actor Actor, id int64, in ExternalSubInput) (*store.ExternalSub, error) {
	if err := checkExternal(&in); err != nil {
		return nil, err
	}
	refetch := false
	err := s.mutate(ctx, change{actor: actor, action: "external.update", entity: "external_sub", entityID: func() int64 { return id }, event: EvUserChanged,
		diff: map[string]any{"name": in.Name, "enabled": in.Enabled}},
		func(q store.DBTX) error {
			e, err := store.GetExternalSub(ctx, q, id)
			if err != nil {
				return err
			}
			refetch = e.URL != in.URL
			e.Name, e.URL, e.Enabled = in.Name, in.URL, in.Enabled
			return store.UpdateExternalSub(ctx, q, e)
		})
	if err != nil {
		return nil, err
	}
	if refetch {
		_ = s.RefreshExternalSub(ctx, id)
	}
	return store.GetExternalSub(ctx, s.st.DB, id)
}

// DeleteExternalSub removes an external subscription.
func (s *Service) DeleteExternalSub(ctx context.Context, actor Actor, id int64) error {
	return s.mutate(ctx, change{actor: actor, action: "external.delete", entity: "external_sub", entityID: func() int64 { return id }, event: EvUserChanged},
		func(q store.DBTX) error { return store.DeleteExternalSub(ctx, q, id) })
}

// ExternalSubs lists external subscriptions.
func (s *Service) ExternalSubs(ctx context.Context) ([]*store.ExternalSub, error) {
	return store.ListExternalSubs(ctx, s.st.DB)
}

// ---- grants ----

// GrantInput gives a cascade or an external subscription to a user (with its own end date) or
// to a group.
type GrantInput struct {
	UserID   int64
	GroupID  int64
	Kind     string // cascade | external
	RefID    int64
	ExpireAt *time.Time // nil = for as long as the user has the subscription
}

// PutGrant adds a grant or changes its end date.
func (s *Service) PutGrant(ctx context.Context, actor Actor, in GrantInput) (*store.Grant, error) {
	if (in.UserID == 0) == (in.GroupID == 0) {
		return nil, invalid("give it to a user or to a group")
	}
	g := &store.Grant{Kind: in.Kind, RefID: in.RefID}
	if in.UserID != 0 {
		g.UserID = &in.UserID
	} else {
		g.GroupID = &in.GroupID
	}
	if in.ExpireAt != nil {
		v := in.ExpireAt.Unix()
		g.ExpireAt = &v
	}
	err := s.mutate(ctx, change{actor: actor, action: "grant.put", entity: "grant", entityID: idOf(&g.ID), event: EvUserChanged, diff: in},
		func(q store.DBTX) error {
			var err error
			switch in.Kind {
			case store.GrantCascade:
				_, err = store.GetCascade(ctx, q, in.RefID)
			case store.GrantExternal:
				_, err = store.GetExternalSub(ctx, q, in.RefID)
			default:
				return invalid("kind must be cascade or external")
			}
			if err != nil {
				return err
			}
			if in.UserID != 0 {
				_, err = store.GetUser(ctx, q, in.UserID)
			} else {
				_, err = store.GetGroup(ctx, q, in.GroupID)
			}
			if err != nil {
				return err
			}
			return store.PutGrant(ctx, q, g)
		})
	return g, err
}

// DeleteGrant removes a grant.
func (s *Service) DeleteGrant(ctx context.Context, actor Actor, id int64) error {
	return s.mutate(ctx, change{actor: actor, action: "grant.delete", entity: "grant", entityID: func() int64 { return id }, event: EvUserChanged},
		func(q store.DBTX) error { return store.DeleteGrant(ctx, q, id) })
}

// Grants lists every grant.
func (s *Service) Grants(ctx context.Context) ([]*store.Grant, error) {
	return store.ListGrants(ctx, s.st.DB)
}

// UserGrant is something a user has, from the user's own grant or a group's. The same thing
// from both counts once, with the later end date.
type UserGrant struct {
	Kind     string  `json:"kind"`
	RefID    int64   `json:"refId"`
	ExpireAt *int64  `json:"expireAt"` // nil = no own end date
	GrantID  int64   `json:"grantId"`  // the user's own grant (0 = only through groups)
	Groups   []int64 `json:"groups"`   // groups that give it
	Active   bool    `json:"active"`
	Problem  string  `json:"problem,omitempty"` // why a cascade is not (fully) in the subscription
}

type grantKey struct {
	kind string
	ref  int64
}

// later is the later of two end dates (nil = never ends).
func later(a, b *int64) *int64 {
	if a == nil || b == nil {
		return nil
	}
	if *a > *b {
		return a
	}
	return b
}

// userGrants resolves what each user has. groupsOf lists a user's groups.
func userGrants(grants []*store.Grant, users []int64, groupsOf map[int64][]int64, now int64) map[int64][]UserGrant {
	byGroup := map[int64][]*store.Grant{}
	byUser := map[int64][]*store.Grant{}
	for _, g := range grants {
		if g.GroupID != nil {
			byGroup[*g.GroupID] = append(byGroup[*g.GroupID], g)
		} else if g.UserID != nil {
			byUser[*g.UserID] = append(byUser[*g.UserID], g)
		}
	}
	out := map[int64][]UserGrant{}
	for _, u := range users {
		m := map[grantKey]*UserGrant{}
		var order []grantKey
		add := func(g *store.Grant, group int64) {
			k := grantKey{g.Kind, g.RefID}
			ug := m[k]
			if ug == nil {
				ug = &UserGrant{Kind: g.Kind, RefID: g.RefID, ExpireAt: g.ExpireAt, Groups: []int64{}}
				m[k] = ug
				order = append(order, k)
			} else {
				ug.ExpireAt = later(ug.ExpireAt, g.ExpireAt)
			}
			if group != 0 {
				if !slices.Contains(ug.Groups, group) { // a grant and the group's access to everything
					ug.Groups = append(ug.Groups, group)
				}
			} else {
				ug.GrantID = g.ID
			}
		}
		for _, g := range byUser[u] {
			add(g, 0)
		}
		for _, gid := range groupsOf[u] {
			for _, g := range byGroup[gid] {
				add(g, gid)
			}
		}
		for _, k := range order {
			ug := m[k]
			ug.Active = ug.ExpireAt == nil || *ug.ExpireAt > now
			out[u] = append(out[u], *ug)
		}
	}
	return out
}

// UserGrants is what one user has (active or ended).
func (s *Service) UserGrants(ctx context.Context, userID int64) ([]UserGrant, error) {
	groups, err := store.UserGroupIDs(ctx, s.st.DB, userID)
	if err != nil {
		return nil, err
	}
	snap, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return s.grantsOf(snap, userID, groups), nil
}

func (s *Service) grantsOf(snap *configSnapshot, userID int64, groups []int64) []UserGrant {
	relevant := append([]*store.Grant(nil), snap.userGrants[userID]...)
	for _, g := range groups {
		relevant = append(relevant, snap.groupGrants[g]...)
	}
	ug := userGrants(relevant, []int64{userID}, map[int64][]int64{userID: groups}, s.now().Unix())[userID]
	if ug == nil {
		ug = []UserGrant{}
	}
	for i := range ug {
		if cs := snap.cascades[ug[i].RefID]; ug[i].Kind == store.GrantCascade && cs != nil {
			ug[i].Problem = cs.problem
		}
	}
	return ug
}

// CascadeProblem says why a cascade is not (fully) in subscriptions ("" = it is).
func (s *Service) CascadeProblem(ctx context.Context, id int64) string {
	snap, err := s.snapshot(ctx)
	if err != nil {
		return ""
	}
	if cs := snap.cascades[id]; cs != nil {
		return cs.problem
	}
	return ""
}

// grantsExpiredSince reports whether a grant ended in (since, now]: the desired state changes.
func (s *Service) grantsExpiredSince(ctx context.Context, since, now int64) bool {
	var n int
	_ = s.st.DB.QueryRowContext(ctx, `SELECT count(*) FROM grants WHERE expire_at > ? AND expire_at <= ?`, since, now).Scan(&n)
	return n > 0
}

// ---- cascades in the desired state ----

// cascadePlan is what cascades add to nodes.
type cascadePlan struct {
	clients   map[int64][]xrayconf.Client // by node inbound id
	outbounds map[int64][]any             // by node id
	rules     map[int64][]any             // by node id, in front of the base rules
	problems  map[int64][]string          // by node id
}

// planCascades works out the cascade users, outbounds and rules.
func (s *Service) planCascades(ctx context.Context, q store.DBTX, groupsOf map[int64][]int64,
	inbounds map[int64]*store.NodeInbound, nodeOn map[int64]bool) (*cascadePlan, error) {
	p := &cascadePlan{clients: map[int64][]xrayconf.Client{}, outbounds: map[int64][]any{}, rules: map[int64][]any{}, problems: map[int64][]string{}}
	cascades, err := store.ListCascades(ctx, q)
	if err != nil || len(cascades) == 0 {
		return p, err
	}
	active, err := store.ActiveUUIDs(ctx, q)
	if err != nil {
		return nil, err
	}
	grants, err := store.ListGrants(ctx, q)
	if err != nil {
		return nil, err
	}
	rules, err := store.ListAccessRules(ctx, q, 0)
	if err != nil {
		return nil, err
	}
	grants = withAllGrants(grants, rules, cascades)
	users := make([]int64, 0, len(active))
	for u := range active {
		users = append(users, u)
	}
	sort.Slice(users, func(i, j int) bool { return users[i] < users[j] })
	has := userGrants(grants, users, groupsOf, s.now().Unix())
	for _, c := range cascades {
		entry, exit := inbounds[c.EntryInboundID], inbounds[c.ExitInboundID]
		if !c.Enabled || entry == nil || exit == nil || !entry.Enabled || !exit.Enabled || !nodeOn[entry.NodeID] || !nodeOn[exit.NodeID] {
			continue
		}
		h, err := s.HostFor(ctx, exit)
		if err != nil {
			p.problems[entry.NodeID] = append(p.problems[entry.NodeID], fmt.Sprintf("cascade %s: exit %s: %v", c.Name, exit.Tag, err))
			continue
		}
		tag := CascadeTag(c.ID)
		p.outbounds[entry.NodeID] = append(p.outbounds[entry.NodeID], HostOutbound(h, c.ServiceUUID, tag))
		p.rules[entry.NodeID] = append(p.rules[entry.NodeID], map[string]any{
			"type": "field", "ruleTag": tag, "user": []string{`regexp:^[0-9]+\.c` + strconv.FormatInt(c.ID, 10) + `$`}, "outboundTag": tag,
		})
		p.clients[exit.ID] = append(p.clients[exit.ID], xrayconf.Client{Email: cascadeServiceEmail(c.ID), ID: c.ServiceUUID})
		for _, u := range users {
			for _, g := range has[u] {
				if g.Kind == store.GrantCascade && g.RefID == c.ID && g.Active {
					p.clients[entry.ID] = append(p.clients[entry.ID], xrayconf.Client{Email: CascadeEmail(u, c.ID), ID: CascadeUUID(active[u], c.ID)})
				}
			}
		}
	}
	return p, nil
}

// extraHosts are a user's cascades and servers from other subscriptions, after the own ones.
func (s *Service) extraHosts(snap *configSnapshot, u *store.User, groups []int64, own []Host, withExternal bool) []Host {
	grants := s.grantsOf(snap, u.ID, groups)
	if len(grants) == 0 {
		return nil
	}
	seen := map[string]bool{}
	for _, h := range own {
		seen[hostKey(h, u.UUID)] = true
	}
	var out []Host
	add := func(h Host) {
		k := hostKey(h, u.UUID)
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, h)
	}
	for _, g := range grants {
		if !g.Active || g.Kind != store.GrantCascade {
			continue
		}
		cs := snap.cascades[g.RefID]
		if cs == nil || !cs.ok {
			continue
		}
		h := cs.entry
		h.Tag, h.Remark, h.UUID, h.Hidden = "cascade-"+strconv.FormatInt(cs.c.ID, 10), cs.c.Remark, CascadeUUID(u.UUID, cs.c.ID), false
		add(h)
	}
	for _, g := range grants {
		if !withExternal || !g.Active || g.Kind != store.GrantExternal {
			continue
		}
		for _, h := range snap.external[g.RefID] {
			h.Tag = "external-" + strconv.FormatInt(g.RefID, 10)
			add(h)
		}
	}
	return out
}

// hostKey identifies a server for dedup: the same server with the same secret is listed once.
func hostKey(h Host, userUUID string) string {
	if h.Raw != "" {
		return "raw|" + h.Raw
	}
	id := h.UUID
	if id == "" {
		id = userUUID
	}
	return strings.Join([]string{h.Protocol, strings.ToLower(h.Address), strconv.Itoa(h.Port), id, h.Network, h.Path, h.SNI}, "|")
}
