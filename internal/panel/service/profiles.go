package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

// ProfileInput creates or edits a profile.
type ProfileInput struct {
	Name          string
	TemplateID    string         // create only
	Values        map[string]any // scope=profile values; missing generated ones are filled in
	Override      map[string]any // merge patch over the template
	TagPattern    string         // optional, defaults to the template's
	RemarkPattern string         // optional
}

// CreateProfile creates a profile from a template.
func (s *Service) CreateProfile(ctx context.Context, actor Actor, in ProfileInput) (*store.Profile, error) {
	tpl, err := xrayconf.GetTemplate(in.TemplateID)
	if err != nil {
		return nil, invalid("%v", err)
	}
	if strings.TrimSpace(in.Name) == "" {
		return nil, invalid("profile name is required")
	}
	values := copyMap(in.Values)
	if err := checkScope(tpl, values, xrayconf.ScopeProfile); err != nil {
		return nil, err
	}
	gen, err := tpl.GenerateValues(xrayconf.ScopeProfile, values)
	if err != nil {
		return nil, err
	}
	for k, v := range gen {
		values[k] = v
	}
	p := &store.Profile{
		Name: strings.TrimSpace(in.Name), TemplateID: tpl.ID, TemplateVersion: tpl.Version,
		Values: values, Override: in.Override,
		TagPattern: firstNonEmpty(in.TagPattern, tpl.TagPattern), RemarkPattern: firstNonEmpty(in.RemarkPattern, tpl.RemarkPattern),
	}
	if p.Override == nil {
		p.Override = map[string]any{}
	}
	err = s.mutate(ctx, change{actor: actor, action: "profile.create", entity: "profile", entityID: idOf(&p.ID), event: EvProfileChanged, diff: map[string]any{"name": p.Name, "template": tpl.ID}},
		func(q store.DBTX) error { return store.CreateProfile(ctx, q, p) })
	return p, err
}

// UpdateProfile changes a profile's values/override. Every node inbound of the profile is
// re-rendered first; any error aborts the change (docs/INBOUNDS.md §1.4).
func (s *Service) UpdateProfile(ctx context.Context, actor Actor, id int64, in ProfileInput) (*store.Profile, error) {
	var p *store.Profile
	err := s.mutate(ctx, change{actor: actor, action: "profile.update", entity: "profile", entityID: func() int64 { return id }, event: EvProfileChanged, diff: in},
		func(q store.DBTX) error {
			var err error
			if p, err = store.GetProfile(ctx, q, id); err != nil {
				return err
			}
			tpl, err := xrayconf.GetTemplate(p.TemplateID)
			if err != nil {
				return err
			}
			if in.Name != "" {
				p.Name = strings.TrimSpace(in.Name)
			}
			if in.Values != nil {
				if err := checkScope(tpl, in.Values, xrayconf.ScopeProfile); err != nil {
					return err
				}
				for k, v := range in.Values {
					if v == nil {
						delete(p.Values, k)
					} else {
						p.Values[k] = v
					}
				}
			}
			if in.Override != nil {
				p.Override = in.Override
			}
			if in.TagPattern != "" {
				p.TagPattern = in.TagPattern
			}
			if in.RemarkPattern != "" {
				p.RemarkPattern = in.RemarkPattern
			}
			if err := store.UpdateProfile(ctx, q, p); err != nil {
				return err
			}
			inbounds, err := store.ListNodeInbounds(ctx, q, 0, p.ID)
			if err != nil {
				return err
			}
			for _, ni := range inbounds {
				if _, err := s.renderNodeInbound(ctx, q, ni); err != nil {
					return fmt.Errorf("inbound %s: %w", ni.Tag, err)
				}
			}
			return nil
		})
	return p, err
}

// DeleteProfile removes an unused profile.
func (s *Service) DeleteProfile(ctx context.Context, actor Actor, id int64) error {
	return s.mutate(ctx, change{actor: actor, action: "profile.delete", entity: "profile", entityID: func() int64 { return id }, event: EvProfileChanged},
		func(q store.DBTX) error {
			used, err := store.ListNodeInbounds(ctx, q, 0, id)
			if err != nil {
				return err
			}
			if len(used) > 0 {
				return invalid("profile is used by %d node inbound(s), detach them first", len(used))
			}
			if err := store.DeleteAccessRulesByRef(ctx, q, store.AccessProfile, id); err != nil {
				return err
			}
			return store.DeleteProfile(ctx, q, id)
		})
}

// Profiles lists profiles.
func (s *Service) Profiles(ctx context.Context) ([]*store.Profile, error) {
	return store.ListProfiles(ctx, s.st.DB)
}

// ProfileByName loads a profile by name.
func (s *Service) ProfileByName(ctx context.Context, name string) (*store.Profile, error) {
	return store.GetProfileByName(ctx, s.st.DB, name)
}

// AttachInput puts a profile on a node.
type AttachInput struct {
	NodeID          int64
	ProfileID       int64
	ListenAddressID *int64         // nil = all interfaces
	EgressAddressID *int64         // nil = same as listen address (when set)
	Values          map[string]any // scope=node values (e.g. CDN_DOMAIN); generated ones are filled in
	PortOverride    int
	Tag             string // optional explicit tag
}

// AttachProfile creates a node inbound from a profile: own tag, own keys (docs/INBOUNDS.md §1.2).
func (s *Service) AttachProfile(ctx context.Context, actor Actor, in AttachInput) (*store.NodeInbound, error) {
	ni := &store.NodeInbound{NodeID: in.NodeID, ProfileID: in.ProfileID, PortOverride: in.PortOverride, Enabled: true,
		ListenAddressID: in.ListenAddressID, EgressAddressID: in.EgressAddressID, Override: map[string]any{}}
	if ni.EgressAddressID == nil {
		ni.EgressAddressID = in.ListenAddressID
	}
	err := s.mutate(ctx, change{actor: actor, action: "inbound.attach", entity: "node_inbound", entityID: idOf(&ni.ID), event: EvInboundChanged,
		diff: map[string]any{"node": in.NodeID, "profile": in.ProfileID}},
		func(q store.DBTX) error {
			node, err := store.GetNode(ctx, q, in.NodeID)
			if err != nil {
				return err
			}
			p, err := store.GetProfile(ctx, q, in.ProfileID)
			if err != nil {
				return err
			}
			tpl, err := xrayconf.GetTemplate(p.TemplateID)
			if err != nil {
				return err
			}
			if err := s.checkAddresses(ctx, q, node.ID, ni.ListenAddressID, ni.EgressAddressID); err != nil {
				return err
			}
			values := copyMap(in.Values)
			if err := checkScope(tpl, values, xrayconf.ScopeNode); err != nil {
				return err
			}
			gen, err := tpl.GenerateValues(xrayconf.ScopeNode, values)
			if err != nil {
				return err
			}
			for k, v := range gen {
				values[k] = v
			}
			ni.Values = values
			if ni.Tag, err = s.pickTag(ctx, q, in.Tag, p, node); err != nil {
				return err
			}
			if err := store.CreateNodeInbound(ctx, q, ni); err != nil {
				return err
			}
			_, err = s.renderNodeInbound(ctx, q, ni)
			return err
		})
	return ni, err
}

// NodeInboundInput edits a node inbound. Nil fields are left unchanged.
type NodeInboundInput struct {
	Values          map[string]any // merged; nil value deletes the key
	Override        map[string]any // replaces the local override
	ListenAddressID *int64
	EgressAddressID *int64
	ClearAddresses  bool // listen on all interfaces, default egress
	PortOverride    *int
	Enabled         *bool
	Tag             string
	RegenerateKeys  bool // re-create generated node values (new Reality keys, shortId)
}

// UpdateNodeInbound edits a node inbound and validates the result before saving.
func (s *Service) UpdateNodeInbound(ctx context.Context, actor Actor, id int64, in NodeInboundInput) (*store.NodeInbound, error) {
	var ni *store.NodeInbound
	err := s.mutate(ctx, change{actor: actor, action: "inbound.update", entity: "node_inbound", entityID: func() int64 { return id }, event: EvInboundChanged,
		diff: map[string]any{"values": redactedKeys(in.Values), "override": in.Override != nil, "regenerate": in.RegenerateKeys}},
		func(q store.DBTX) error {
			var err error
			if ni, err = store.GetNodeInbound(ctx, q, id); err != nil {
				return err
			}
			p, err := store.GetProfile(ctx, q, ni.ProfileID)
			if err != nil {
				return err
			}
			tpl, err := xrayconf.GetTemplate(p.TemplateID)
			if err != nil {
				return err
			}
			if in.Values != nil {
				if err := checkScope(tpl, in.Values, xrayconf.ScopeNode); err != nil {
					return err
				}
				for k, v := range in.Values {
					if v == nil {
						delete(ni.Values, k)
					} else {
						ni.Values[k] = v
					}
				}
			}
			if in.RegenerateKeys {
				for _, v := range tpl.Variables {
					if v.Scope == xrayconf.ScopeNode && v.Source == xrayconf.SourceGenerate {
						delete(ni.Values, v.Name)
					}
				}
				gen, err := tpl.GenerateValues(xrayconf.ScopeNode, ni.Values)
				if err != nil {
					return err
				}
				for k, v := range gen {
					ni.Values[k] = v
				}
			}
			if in.Override != nil {
				ni.Override = in.Override
			}
			if in.ClearAddresses {
				ni.ListenAddressID, ni.EgressAddressID = nil, nil
			}
			if in.ListenAddressID != nil {
				ni.ListenAddressID = in.ListenAddressID
			}
			if in.EgressAddressID != nil {
				ni.EgressAddressID = in.EgressAddressID
			}
			if err := s.checkAddresses(ctx, q, ni.NodeID, ni.ListenAddressID, ni.EgressAddressID); err != nil {
				return err
			}
			if in.PortOverride != nil {
				ni.PortOverride = *in.PortOverride
			}
			if in.Enabled != nil {
				ni.Enabled = *in.Enabled
			}
			if in.Tag != "" && in.Tag != ni.Tag {
				if exists, err := store.TagExists(ctx, q, in.Tag); err != nil {
					return err
				} else if exists {
					return fmt.Errorf("%w: tag %s", ErrConflict, in.Tag)
				}
				ni.Tag = in.Tag
			}
			if err := store.UpdateNodeInbound(ctx, q, ni); err != nil {
				return err
			}
			_, err = s.renderNodeInbound(ctx, q, ni)
			return err
		})
	return ni, err
}

// DetachInbound removes a node inbound.
func (s *Service) DetachInbound(ctx context.Context, actor Actor, id int64) error {
	return s.mutate(ctx, change{actor: actor, action: "inbound.detach", entity: "node_inbound", entityID: func() int64 { return id }, event: EvInboundChanged},
		func(q store.DBTX) error { return store.DeleteNodeInbound(ctx, q, id) })
}

// NodeInbounds lists inbounds of a node (0 = all).
func (s *Service) NodeInbounds(ctx context.Context, nodeID int64) ([]*store.NodeInbound, error) {
	return store.ListNodeInbounds(ctx, s.st.DB, nodeID, 0)
}

// NodeInboundByTag loads a node inbound by tag.
func (s *Service) NodeInboundByTag(ctx context.Context, tag string) (*store.NodeInbound, error) {
	return store.GetNodeInboundByTag(ctx, s.st.DB, tag)
}

// RenderNodeInbound renders one node inbound (preview, host generation).
func (s *Service) RenderNodeInbound(ctx context.Context, id int64) (*xrayconf.RenderedInbound, error) {
	ni, err := store.GetNodeInbound(ctx, s.st.DB, id)
	if err != nil {
		return nil, err
	}
	return s.renderNodeInbound(ctx, s.st.DB, ni)
}

func (s *Service) renderNodeInbound(ctx context.Context, q store.DBTX, ni *store.NodeInbound) (*xrayconf.RenderedInbound, error) {
	node, err := store.GetNode(ctx, q, ni.NodeID)
	if err != nil {
		return nil, err
	}
	p, err := store.GetProfile(ctx, q, ni.ProfileID)
	if err != nil {
		return nil, err
	}
	nctx := xrayconf.NodeContext{Code: node.Code, Name: node.Name, Country: node.Country, Domain: node.Domain, Tag: ni.Tag}
	var egress string
	if ni.ListenAddressID != nil {
		a, err := store.GetAddress(ctx, q, *ni.ListenAddressID)
		if err != nil {
			return nil, fmt.Errorf("listen address: %w", err)
		}
		nctx.ListenIP = a.IP
	}
	if ni.EgressAddressID != nil {
		a, err := store.GetAddress(ctx, q, *ni.EgressAddressID)
		if err != nil {
			return nil, fmt.Errorf("egress address: %w", err)
		}
		egress = a.IP
	}
	r, err := xrayconf.RenderInbound(xrayconf.InboundSpec{
		TemplateID: p.TemplateID, ProfileValues: p.Values, NodeValues: ni.Values,
		ProfileOverride: p.Override, NodeOverride: ni.Override, PortOverride: ni.PortOverride,
		EgressIP: egress, Context: nctx,
	})
	if err != nil {
		if xrayconf.IsLintError(err) {
			return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		return nil, invalid("%v", err)
	}
	return r, nil
}

func (s *Service) checkAddresses(ctx context.Context, q store.DBTX, nodeID int64, ids ...*int64) error {
	for _, id := range ids {
		if id == nil {
			continue
		}
		a, err := store.GetAddress(ctx, q, *id)
		if errors.Is(err, store.ErrNotFound) {
			return invalid("address %d does not exist", *id)
		} else if err != nil {
			return err
		}
		if a.NodeID != nodeID {
			return invalid("address %s belongs to another node", a.IP)
		}
	}
	return nil
}

// pickTag renders the profile's tag pattern for the node and makes it unique (VLESS_NL, VLESS_NL_2, ...).
func (s *Service) pickTag(ctx context.Context, q store.DBTX, want string, p *store.Profile, node *store.Node) (string, error) {
	if want == "" {
		want = strings.NewReplacer("${NODE_CODE}", node.Code, "${NODE_COUNTRY}", node.Country).Replace(p.TagPattern)
	}
	if strings.Contains(want, "${") || want == "" {
		return "", invalid("tag pattern %q can only use ${NODE_CODE} and ${NODE_COUNTRY}", p.TagPattern)
	}
	for i := 1; i < 100; i++ {
		tag := want
		if i > 1 {
			tag = fmt.Sprintf("%s_%d", want, i)
		}
		exists, err := store.TagExists(ctx, q, tag)
		if err != nil {
			return "", err
		}
		if !exists {
			return tag, nil
		}
	}
	return "", fmt.Errorf("no free tag for %s", want)
}

// checkScope rejects values for unknown variables or for the wrong scope.
func checkScope(tpl *xrayconf.Template, values map[string]any, scope xrayconf.Scope) error {
	for k := range values {
		v, ok := tpl.Variable(k)
		if !ok {
			return invalid("template %s has no variable %s", tpl.ID, k)
		}
		if v.Scope != scope {
			return invalid("variable %s is set per %s, not per %s", k, v.Scope, scope)
		}
		if v.Source == xrayconf.SourceDerived {
			return invalid("variable %s is derived and cannot be set", k)
		}
	}
	return nil
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func redactedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
