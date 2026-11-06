package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

// Replication: duplicate a node inbound, duplicate a profile and apply a profile to many
// nodes at once (docs/INBOUNDS.md §1.2: every inbound gets its own tag and own keys).

// DuplicateInbound copies a node inbound on the same node: the profile stays, node
// variables are regenerated (new Reality keys, shortId…), typed values (the domain) and
// the node's override are kept, and the tag gets the next free suffix (VLESS_NL_2, ...).
func (s *Service) DuplicateInbound(ctx context.Context, actor Actor, id int64) (*store.NodeInbound, error) {
	var out *store.NodeInbound
	err := s.mutate(ctx, change{actor: actor, action: "inbound.duplicate", entity: "node_inbound", entityID: idOf(&id), event: EvInboundChanged,
		diff: map[string]any{"source": id}},
		func(q store.DBTX) error {
			src, err := store.GetNodeInbound(ctx, q, id)
			if err != nil {
				return err
			}
			node, err := store.GetNode(ctx, q, src.NodeID)
			if err != nil {
				return err
			}
			p, err := store.GetProfile(ctx, q, src.ProfileID)
			if err != nil {
				return err
			}
			tpl, err := s.template(ctx, p.TemplateID)
			if err != nil {
				return err
			}
			if err := s.checkAddresses(ctx, q, node.ID, src.ListenAddressID, src.EgressAddressID); err != nil {
				return err
			}
			// Keep typed node values (the domain), regenerate generated ones: new keys.
			values := copyMap(src.Values)
			for _, v := range tpl.Variables {
				if v.Scope == xrayconf.ScopeNode && v.Source == xrayconf.SourceGenerate {
					delete(values, v.Name)
				}
			}
			gen, err := tpl.GenerateValues(xrayconf.ScopeNode, values)
			if err != nil {
				return err
			}
			for k, v := range gen {
				values[k] = v
			}
			ni := &store.NodeInbound{NodeID: src.NodeID, ProfileID: src.ProfileID, Values: values,
				Override: copyMap(src.Override), Host: copyMap(src.Host), PortOverride: src.PortOverride, Enabled: true,
				ListenAddressID: src.ListenAddressID, EgressAddressID: src.EgressAddressID}
			// pickTag counts from the original's base, so a copy of VLESS_NL is VLESS_NL_2 and a copy of that is VLESS_NL_3.
			if ni.Tag, err = s.pickTag(ctx, q, tagBase(src.Tag), p, node); err != nil {
				return err
			}
			if err := store.CreateNodeInbound(ctx, q, ni); err != nil {
				return err
			}
			if _, err := s.renderNodeInbound(ctx, q, ni); err != nil {
				return err
			}
			out = ni
			return nil
		})
	return out, err
}

// DuplicateProfile copies a profile: the same template, values, override and own sources,
// a fresh name («Reality (копия)») and no nodes — the copy is not attached anywhere.
func (s *Service) DuplicateProfile(ctx context.Context, actor Actor, id int64) (*store.Profile, error) {
	var out *store.Profile
	err := s.mutate(ctx, change{actor: actor, action: "profile.duplicate", entity: "profile", entityID: idOf(&id), event: EvProfileChanged,
		diff: map[string]any{"source": id}},
		func(q store.DBTX) error {
			src, err := store.GetProfile(ctx, q, id)
			if err != nil {
				return err
			}
			name, err := freeProfileName(ctx, q, src.Name)
			if err != nil {
				return err
			}
			p := &store.Profile{Name: name, TemplateID: src.TemplateID, TemplateVersion: src.TemplateVersion,
				Values: copyMap(src.Values), Override: copyMap(src.Override), Inbound: copyMap(src.Inbound), Host: copyMap(src.Host),
				Client: src.Client, TagPattern: src.TagPattern, RemarkPattern: src.RemarkPattern}
			if p.Override == nil {
				p.Override = map[string]any{}
			}
			if err := store.CreateProfile(ctx, q, p); err != nil {
				return err
			}
			out = p
			return nil
		})
	return out, err
}

// tagBase strips a trailing _2/_3/… suffix so the copy counts from the same base as the
// original: a copy of VLESS_NL is VLESS_NL_2, and a copy of that is VLESS_NL_3.
func tagBase(tag string) string {
	i := strings.LastIndex(tag, "_")
	if i < 0 {
		return tag
	}
	if n, err := strconv.Atoi(tag[i+1:]); err == nil && n >= 2 {
		return tag[:i]
	}
	return tag
}

// freeProfileName returns «Name (копия)», or «Name (копия 2)», ... if that is taken.
func freeProfileName(ctx context.Context, q store.DBTX, base string) (string, error) {
	name := base + " (копия)"
	for i := 2; ; i++ {
		_, err := store.GetProfileByName(ctx, q, name)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return name, nil
			}
			return "", err
		}
		name = fmt.Sprintf("%s (копия %d)", base, i)
	}
}

// ApplyResult is one node of a mass apply: an inbound created, or why not.
type ApplyResult struct {
	NodeID   int64
	NodeName string
	Tag      string // set when an inbound was created
	Skipped  bool   // the profile is already on the node — nothing to do
	Err      error  // why the node failed (e.g. no domain)
}

// ApplyProfileToNodes attaches a profile to every node in the list. Each node is its own
// transaction: a node without a domain (or one that fails rendering) is reported as an
// error and does not stop the rest; nodes the profile is already on are skipped.
func (s *Service) ApplyProfileToNodes(ctx context.Context, actor Actor, profileID int64, nodeIDs []int64) ([]ApplyResult, error) {
	if _, err := store.GetProfile(ctx, s.st.DB, profileID); err != nil {
		return nil, err
	}
	out := make([]ApplyResult, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		node, err := store.GetNode(ctx, s.st.DB, nodeID)
		if err != nil {
			out = append(out, ApplyResult{NodeID: nodeID, Err: err})
			continue
		}
		attached, err := store.ListNodeInbounds(ctx, s.st.DB, nodeID, profileID)
		if err != nil {
			out = append(out, ApplyResult{NodeID: nodeID, NodeName: node.Name, Err: err})
			continue
		}
		if len(attached) > 0 {
			out = append(out, ApplyResult{NodeID: nodeID, NodeName: node.Name, Skipped: true})
			continue
		}
		res := ApplyResult{NodeID: nodeID, NodeName: node.Name}
		ni, err := s.AttachProfile(ctx, actor, AttachInput{NodeID: nodeID, ProfileID: profileID})
		if err != nil {
			res.Err = err
		} else {
			res.Tag = ni.Tag
		}
		out = append(out, res)
	}
	return out, nil
}

// NeedsNodeDomain reports whether the profile's template needs the node's domain (a
// node-level variable defaulted from node.domain without another default), for the apply
// dialog's preview of which nodes will fail.
func (s *Service) NeedsNodeDomain(ctx context.Context, profileID int64) (bool, error) {
	p, err := store.GetProfile(ctx, s.st.DB, profileID)
	if err != nil {
		return false, err
	}
	tpl, err := s.template(ctx, p.TemplateID)
	if err != nil {
		return false, err
	}
	for _, v := range tpl.Variables {
		if v.Scope == xrayconf.ScopeNode && v.DefaultFrom == "node.domain" && v.Default == nil {
			return true, nil
		}
	}
	return false, nil
}

// NodeNamesByID lists nodes as id → name for the apply result view.
func (s *Service) NodeNamesByID(ctx context.Context) (map[int64]string, error) {
	nodes, err := store.ListNodes(ctx, s.st.DB)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]string, len(nodes))
	for _, n := range nodes {
		out[n.ID] = strings.TrimSpace(n.Name + " (" + n.Code + ")")
	}
	return out, nil
}
