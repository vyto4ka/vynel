package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/vyto4ka/vynel/internal/caddyconf"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

// DefaultXrayAPIPort is the Xray API port on nodes (127.0.0.1 only).
const DefaultXrayAPIPort = 10085

// DesiredInbound describes one inbound in the desired state.
type DesiredInbound struct {
	Tag      string `json:"tag"`
	Protocol string `json:"protocol"`
	Flow     string `json:"flow,omitempty"`
}

// DesiredState is what a node should run (docs/ARCHITECTURE.md §6.2 "desired state").
// Config holds the structure without clients; users are listed separately so that user
// changes become hot API operations while structural changes restart Xray.
type DesiredState struct {
	NodeID   int64
	Config   []byte
	Caddy    []byte // Caddy JSON config; nil = Caddy not needed
	Inbounds []DesiredInbound
	Users    map[string][]xrayconf.Client // by inbound tag, sorted by email
	Hash     string
	Problems []string // inbounds skipped because they failed to render
}

// UserEmail is the stable Xray "email" of a user: its id. Stats are keyed by it.
func UserEmail(userID int64) string { return strconv.FormatInt(userID, 10) }

// DesiredStates computes the desired state of every node in one pass.
func (s *Service) DesiredStates(ctx context.Context) (map[int64]*DesiredState, error) {
	q := s.st.DB
	nodes, err := store.ListNodes(ctx, q)
	if err != nil {
		return nil, err
	}
	inbounds, err := store.ListNodeInbounds(ctx, q, 0, 0)
	if err != nil {
		return nil, err
	}
	rules, err := store.ListAccessRules(ctx, q, 0)
	if err != nil {
		return nil, err
	}
	members, err := store.ListActiveMembers(ctx, q)
	if err != nil {
		return nil, err
	}
	apiPort, err := s.Setting(ctx, SettingXrayAPIPort, strconv.Itoa(DefaultXrayAPIPort))
	if err != nil {
		return nil, err
	}

	access := ResolveAccess(rules, inbounds)
	// inbound id -> set of users (dedup when a user is in several groups granting the same inbound)
	usersByInbound := map[int64]map[int64]string{}
	for _, m := range members {
		for niID := range access[m.GroupID] {
			set := usersByInbound[niID]
			if set == nil {
				set = map[int64]string{}
				usersByInbound[niID] = set
			}
			set[m.UserID] = m.UUID
		}
	}

	out := make(map[int64]*DesiredState, len(nodes))
	for _, n := range nodes {
		ds := &DesiredState{NodeID: n.ID, Users: map[string][]xrayconf.Client{}}
		// Per-node override exists for several nodes sharing one host (tests, unusual setups).
		nodePort, err := s.Setting(ctx, SettingXrayAPIPort+"."+n.Code, apiPort)
		if err != nil {
			return nil, err
		}
		apiAddr := "127.0.0.1:" + nodePort
		base, err := store.GetBaseConfig(ctx, q, n.BaseConfigID)
		if errors.Is(err, store.ErrNotFound) {
			base = &store.BaseConfig{JSON: xrayconf.DefaultBaseJSON}
		} else if err != nil {
			return nil, err
		}
		baseMap, err := xrayconf.ParseBase(base.JSON)
		if err != nil {
			return nil, fmt.Errorf("node %s: %w", n.Code, err)
		}
		var rendered []*xrayconf.RenderedInbound
		if n.Enabled {
			for _, ni := range inbounds {
				if ni.NodeID != n.ID || !ni.Enabled {
					continue
				}
				r, err := s.renderNodeInbound(ctx, q, ni)
				if err != nil {
					ds.Problems = append(ds.Problems, fmt.Sprintf("%s: %v", ni.Tag, err))
					continue
				}
				rendered = append(rendered, r)
				ds.Inbounds = append(ds.Inbounds, DesiredInbound{Tag: r.Tag, Protocol: r.Protocol, Flow: r.Flow})
				clients := make([]xrayconf.Client, 0, len(usersByInbound[ni.ID]))
				for uid, uuid := range usersByInbound[ni.ID] {
					clients = append(clients, xrayconf.Client{Email: UserEmail(uid), ID: uuid})
				}
				sort.Slice(clients, func(i, j int) bool { return clients[i].Email < clients[j].Email })
				ds.Users[r.Tag] = clients
			}
		}
		cfg, err := xrayconf.BuildConfig(xrayconf.NodeConfig{Base: baseMap, Inbounds: rendered, APIAddr: apiAddr})
		if err != nil {
			// A structural conflict (e.g. two inbounds on one port) blocks the whole node config.
			ds.Problems = append(ds.Problems, err.Error())
			cfg, err = xrayconf.BuildConfig(xrayconf.NodeConfig{Base: baseMap, APIAddr: apiAddr})
			if err != nil {
				return nil, err
			}
			ds.Inbounds, ds.Users = nil, map[string][]xrayconf.Client{}
		}
		if ds.Config, err = json.Marshal(cfg); err != nil {
			return nil, err
		}
		spec, problems, err := s.caddySpec(ctx, n, rendered)
		if err != nil {
			return nil, err
		}
		ds.Problems = append(ds.Problems, problems...)
		if ds.Caddy, err = caddyconf.Build(spec); err != nil {
			ds.Problems = append(ds.Problems, "caddy: "+err.Error())
			ds.Caddy = nil
		}
		ds.Hash = hashState(ds)
		out[n.ID] = ds
	}
	return out, nil
}

// DesiredState computes one node's desired state.
func (s *Service) DesiredState(ctx context.Context, nodeID int64) (*DesiredState, error) {
	all, err := s.DesiredStates(ctx)
	if err != nil {
		return nil, err
	}
	ds, ok := all[nodeID]
	if !ok {
		return nil, ErrNotFound
	}
	return ds, nil
}

func hashState(ds *DesiredState) string {
	h := sha256.New()
	h.Write(ds.Config)
	h.Write([]byte("\x00caddy"))
	h.Write(ds.Caddy)
	tags := make([]string, 0, len(ds.Users))
	for t := range ds.Users {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	for _, t := range tags {
		h.Write([]byte("\x00" + t))
		for _, c := range ds.Users[t] {
			h.Write([]byte("\x01" + c.Email + "\x02" + c.ID))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// EnsureDefaults creates the default base config, group and user template on a fresh database.
func (s *Service) EnsureDefaults(ctx context.Context) error {
	return s.st.Tx(ctx, func(q store.DBTX) error {
		if _, err := store.GetBaseConfig(ctx, q, nil); errors.Is(err, store.ErrNotFound) {
			if err := store.UpsertBaseConfig(ctx, q, &store.BaseConfig{Name: "default", JSON: xrayconf.DefaultBaseJSON, IsDefault: true}); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		groups, err := store.ListGroups(ctx, q)
		if err != nil {
			return err
		}
		var groupIDs []int64
		if len(groups) == 0 {
			g := &store.Group{Name: "Основная", Description: "Группа по умолчанию"}
			if err := store.CreateGroup(ctx, q, g); err != nil {
				return err
			}
			groupIDs = []int64{g.ID}
		}
		tpls, err := store.ListUserTemplates(ctx, q)
		if err != nil {
			return err
		}
		if len(tpls) == 0 {
			return store.CreateUserTemplate(ctx, q, &store.UserTemplate{
				Name: "Стандарт", IsDefault: true, ExpireMonths: 3, ResetStrategy: "month", ClientType: "auto", GroupIDs: groupIDs,
			})
		}
		return nil
	})
}
