package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

// ProfileInput creates or edits a profile.
type ProfileInput struct {
	Name       string
	TemplateID string         // create only
	Values     map[string]any // scope=profile values; missing generated ones are filled in
	Override   map[string]any // merge patch over the template (legacy; the editor sends Inbound)
	// Inbound and Host are the profile's own source JSON (${VARIABLES} kept). nil leaves them as
	// they are; a source equal to the template's goes back to following the template.
	Inbound       map[string]any
	Host          map[string]any
	TagPattern    string // optional, defaults to the template's
	RemarkPattern string // optional
	// RegenerateKeys re-creates the profile's generated values (e.g. a random path).
	RegenerateKeys bool
	// Client is the profile's own client config for subscriptions; nil leaves it as it is.
	Client *ProfileClient
}

// CreateProfile creates a profile from a template.
func (s *Service) CreateProfile(ctx context.Context, actor Actor, in ProfileInput) (*store.Profile, error) {
	if err := s.syncTemplates(ctx); err != nil {
		return nil, err
	}
	tpl, err := s.template(ctx, in.TemplateID)
	if err != nil {
		return nil, err
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
	err := s.mutate(ctx, change{actor: actor, action: "profile.update", entity: "profile", entityID: func() int64 { return id }, event: EvProfileChanged,
		diff: map[string]any{"name": in.Name, "values": redactedKeys(in.Values), "override": in.Override != nil, "regenerate": in.RegenerateKeys}},
		func(q store.DBTX) error {
			var err error
			p, err = s.applyProfile(ctx, q, id, in)
			if err != nil {
				return err
			}
			for _, r := range s.renderProfileInbounds(ctx, q, p.ID) {
				if r.Err != nil {
					return fmt.Errorf("inbound %s: %w", r.Inbound.Tag, r.Err)
				}
				if err := checkHost(r.Rendered); err != nil {
					return invalid("inbound %s: connection point: %v", r.Inbound.Tag, err)
				}
			}
			return nil
		})
	return p, err
}

// InboundPreview is a node inbound rendered with unsaved changes.
type InboundPreview struct {
	Inbound  *store.NodeInbound
	Node     *store.Node
	Rendered *xrayconf.RenderedInbound
	Err      error
}

// PreviewProfile shows what every node inbound of the profile becomes with the changes, without
// saving anything.
func (s *Service) PreviewProfile(ctx context.Context, id int64, in ProfileInput) (*store.Profile, []InboundPreview, error) {
	var p *store.Profile
	var out []InboundPreview
	err := s.dryRun(ctx, func(q store.DBTX) error {
		var err error
		if p, err = s.applyProfile(ctx, q, id, in); err != nil {
			return err
		}
		out = s.renderProfileInbounds(ctx, q, p.ID)
		return nil
	})
	return p, out, err
}

func (s *Service) renderProfileInbounds(ctx context.Context, q store.DBTX, profileID int64) []InboundPreview {
	inbounds, err := store.ListNodeInbounds(ctx, q, 0, profileID)
	if err != nil {
		return []InboundPreview{{Err: err, Inbound: &store.NodeInbound{}}}
	}
	out := make([]InboundPreview, 0, len(inbounds))
	for _, ni := range inbounds {
		pv := InboundPreview{Inbound: ni}
		pv.Node, _ = store.GetNode(ctx, q, ni.NodeID)
		pv.Rendered, pv.Err = s.renderNodeInboundWith(ctx, q, ni, nil)
		out = append(out, pv)
	}
	return out
}

// applyProfile changes a profile inside a transaction.
func (s *Service) applyProfile(ctx context.Context, q store.DBTX, id int64, in ProfileInput) (*store.Profile, error) {
	p, err := store.GetProfile(ctx, q, id)
	if err != nil {
		return nil, err
	}
	tpl, err := s.template(ctx, p.TemplateID)
	if err != nil {
		return nil, err
	}
	if name := strings.TrimSpace(in.Name); name != "" {
		p.Name = name
	}
	if in.Values != nil {
		if err := checkScope(tpl, in.Values, xrayconf.ScopeProfile); err != nil {
			return nil, err
		}
		for k, v := range in.Values {
			if v == nil {
				delete(p.Values, k)
			} else {
				p.Values[k] = v
			}
		}
	}
	if in.RegenerateKeys {
		for _, v := range tpl.Variables {
			if v.Scope == xrayconf.ScopeProfile && v.Source == xrayconf.SourceGenerate {
				delete(p.Values, v.Name)
			}
		}
	}
	// Generated values a template added later (or that were just cleared) are filled in.
	gen, err := tpl.GenerateValues(xrayconf.ScopeProfile, p.Values)
	if err != nil {
		return nil, err
	}
	for k, v := range gen {
		p.Values[k] = v
	}
	if in.Override != nil {
		p.Override = in.Override
	}
	if in.Inbound != nil {
		p.Inbound = sameAsTemplate(in.Inbound, tpl.SourceInbound())
		p.Override = map[string]any{} // a full source replaces patches on top of the template
	}
	if in.Host != nil {
		p.Host = sameAsTemplate(in.Host, tpl.SourceHost())
	}
	if _, err := tpl.WithSources(p.Inbound, p.Host); err != nil {
		return nil, invalid("%v", err)
	}
	if in.TagPattern != "" {
		if !strings.Contains(in.TagPattern, "${NODE_CODE}") && !strings.Contains(in.TagPattern, "${NODE_COUNTRY}") {
			return nil, invalid("the tag pattern must contain ${NODE_CODE} or ${NODE_COUNTRY}: every node needs its own tag")
		}
		p.TagPattern = in.TagPattern
	}
	if in.RemarkPattern != "" {
		p.RemarkPattern = in.RemarkPattern
	}
	if in.Client != nil {
		if err := CheckProfileClient(in.Client); err != nil {
			return nil, InField("«Клиент»", err)
		}
		p.Client = ""
		if in.Client.Custom {
			b, _ := json.Marshal(in.Client)
			p.Client = string(b)
		}
	}
	p.TemplateVersion = tpl.Version
	if err := store.UpdateProfile(ctx, q, p); err != nil {
		return nil, err
	}
	return p, nil
}

// checkHost expands the connection point the way subscriptions do, so a typo in a ${VARIABLE}
// is refused on save instead of breaking subscriptions later.
func checkHost(r *xrayconf.RenderedInbound) error {
	if r == nil || r.Template == nil || r.Template.Host == nil {
		return nil
	}
	vals := copyMap(r.Values)
	vals["INBOUND_PORT"] = r.Inbound["port"]
	_, err := xrayconf.ExpandTree(r.Template.Host, vals)
	return err
}

// sameAsTemplate returns nil when src is the template's source (the profile follows the
// template again, including its future versions), else src.
func sameAsTemplate(src, tpl map[string]any) map[string]any {
	a, _ := json.Marshal(src)
	b, _ := json.Marshal(tpl)
	if string(a) == string(b) {
		return nil
	}
	return src
}

// ProfileSources returns what the editor shows: the profile's own source or the template's,
// with a legacy override folded into the inbound.
func ProfileSources(tpl *xrayconf.Template, p *store.Profile) (inbound, host map[string]any) {
	inbound = p.Inbound
	if inbound == nil {
		inbound = xrayconf.MergePatch(tpl.SourceInbound(), copyMap(p.Override))
	}
	host = p.Host
	if host == nil {
		host = tpl.SourceHost()
	}
	return inbound, host
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
			tpl, err := s.template(ctx, p.TemplateID)
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
			ni, _, err = s.applyNodeInbound(ctx, q, id, in)
			return err
		})
	return ni, err
}

// PreviewNodeInbound renders a node inbound with unsaved changes.
func (s *Service) PreviewNodeInbound(ctx context.Context, id int64, in NodeInboundInput) (*store.NodeInbound, *xrayconf.RenderedInbound, error) {
	var ni *store.NodeInbound
	var r *xrayconf.RenderedInbound
	err := s.dryRun(ctx, func(q store.DBTX) error {
		var err error
		ni, r, err = s.applyNodeInbound(ctx, q, id, in)
		return err
	})
	return ni, r, err
}

func (s *Service) applyNodeInbound(ctx context.Context, q store.DBTX, id int64, in NodeInboundInput) (*store.NodeInbound, *xrayconf.RenderedInbound, error) {
	ni, err := store.GetNodeInbound(ctx, q, id)
	if err != nil {
		return nil, nil, err
	}
	p, err := store.GetProfile(ctx, q, ni.ProfileID)
	if err != nil {
		return nil, nil, err
	}
	tpl, err := s.template(ctx, p.TemplateID)
	if err != nil {
		return nil, nil, err
	}
	if in.Values != nil {
		if err := checkScope(tpl, in.Values, xrayconf.ScopeNode); err != nil {
			return nil, nil, err
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
			return nil, nil, err
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
		return nil, nil, err
	}
	if in.PortOverride != nil {
		ni.PortOverride = *in.PortOverride
	}
	if in.Enabled != nil {
		ni.Enabled = *in.Enabled
	}
	if in.Tag != "" && in.Tag != ni.Tag {
		if exists, err := store.TagExists(ctx, q, in.Tag); err != nil {
			return nil, nil, err
		} else if exists {
			return nil, nil, fmt.Errorf("%w: tag %s", ErrConflict, in.Tag)
		}
		ni.Tag = in.Tag
	}
	if err := store.UpdateNodeInbound(ctx, q, ni); err != nil {
		return nil, nil, err
	}
	r, err := s.renderNodeInbound(ctx, q, ni)
	if err != nil {
		return nil, nil, err
	}
	return ni, r, nil
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
	if err := s.syncTemplates(ctx); err != nil {
		return nil, err
	}
	ni, err := store.GetNodeInbound(ctx, s.st.DB, id)
	if err != nil {
		return nil, err
	}
	return s.renderNodeInbound(ctx, s.st.DB, ni)
}

// NodeInboundSources renders the same inbound at each level of docs/INBOUNDS.md §1.4 and
// labels each key of the final render with where it comes from: "system" (tag, clients),
// "node" (the node inbound's merge patch, port included), "profile" (the profile's patch)
// or "template" (everything else). Lint runs only on the full render; level renders skip
// port (nil there), so a level with lint rules is checked without port checks.
func (s *Service) NodeInboundSources(ctx context.Context, id int64) (final, levels, sources map[string]any, err error) {
	if err = s.syncTemplates(ctx); err != nil {
		return nil, nil, nil, err
	}
	ni, err := store.GetNodeInbound(ctx, s.st.DB, id)
	if err != nil {
		return nil, nil, nil, err
	}
	r, err := s.renderNodeInbound(ctx, s.st.DB, ni)
	if err != nil {
		return nil, nil, nil, err
	}
	spec, err := s.inboundSpec(ctx, s.st.DB, ni)
	if err != nil {
		return nil, nil, nil, err
	}
	final = deepCopyAny(r.Inbound).(map[string]any)
	levels = map[string]any{
		"template": renderLevel(spec, func(s2 *xrayconf.InboundSpec) { s2.ProfileOverride, s2.NodeOverride = nil, nil }),
		"profile":  renderLevel(spec, func(s2 *xrayconf.InboundSpec) { s2.NodeOverride = nil }),
		"node":     deepCopyAny(r.Inbound),
	}
	sources = map[string]any{}
	annotateSources(sources, final, asObj(levels["profile"]), asObj(levels["template"]))
	return final, levels, sources, nil
}

// inboundSpec assembles the render spec of a node inbound (NodeOverride, PortOverride, context).
func (s *Service) inboundSpec(ctx context.Context, q store.DBTX, ni *store.NodeInbound) (*xrayconf.InboundSpec, error) {
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
	tpl, err := s.template(ctx, p.TemplateID)
	if err != nil {
		return nil, err
	}
	if tpl, err = tpl.WithSources(p.Inbound, p.Host); err != nil {
		return nil, invalid("profile %s: %v", p.Name, err)
	}
	return &xrayconf.InboundSpec{TemplateID: p.TemplateID, Template: tpl, ProfileValues: p.Values, NodeValues: ni.Values,
		ProfileOverride: p.Override, NodeOverride: ni.Override, PortOverride: ni.PortOverride,
		EgressIP: egress, Context: nctx}, nil
}

// renderLevel renders one inheritance level of the spec; nil when the level does not
// render (the final error is reported separately, levels are advisory).
func renderLevel(spec *xrayconf.InboundSpec, trim func(*xrayconf.InboundSpec)) map[string]any {
	s := *spec
	s.NodeOverride, s.PortOverride = nil, 0
	s.Context.Tag = "" // the level's own tag pattern, not the unique one picked for the node
	trim(&s)
	r, err := xrayconf.RenderInbound(s)
	if err != nil {
		return nil
	}
	m, _ := deepCopyAny(r.Inbound).(map[string]any)
	return m
}

// deepCopyAny clones a decoded JSON tree of any shape.
func deepCopyAny(v any) any {
	b, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

// annotateSources labels every key of "higher": "node" where the final render differs from
// the profile level, "profile" where the profile level differs from the template level, and
// "template" everywhere else. The comparison walks the level trees: a key absent (or set to a
// non-object value) in the lower level means that whole subtree was set by the upper level.
func annotateSources(src, final, profile, template map[string]any) {
	for k, fv := range final {
		pv, pHas := lookupChild(profile, k)
		tv, tHas := lookupChild(template, k)
		if fm, ok := fv.(map[string]any); ok {
			sm, _ := src[k].(map[string]any)
			if sm == nil {
				if _, labeled := src[k].(string); labeled {
					continue
				}
				sm = map[string]any{}
				src[k] = sm
			}
			if pHas && !tHas || !isObj(pv) && !isObj(tv) {
				continue // a whole-object override at the upper level: every key keeps the outer label
			}
			annotateSources(sm, fm, asObj(pv), asObj(tv))
			continue
		}
		if pHas && !equalJSON(fv, pv) {
			src[k] = "node"
		} else if pHas != tHas || !equalJSON(pv, tv) {
			src[k] = "profile"
		} else {
			src[k] = "template"
		}
	}
}

// lookupChild returns k of a possibly nil level; absent and null are the same (an RFC 7386
// merge patch deletes keys with null).
func lookupChild(m map[string]any, k string) (any, bool) {
	if m == nil {
		return nil, false
	}
	v, ok := m[k]
	if v == nil {
		return nil, false
	}
	return v, ok
}

// isObj reports whether v is a JSON object.
func isObj(v any) bool {
	_, ok := v.(map[string]any)
	return ok
}

// asObj casts to a JSON object, nil when it is not one.
func asObj(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// equalJSON compares two decoded JSON values by their marshalled form.
func equalJSON(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

func (s *Service) renderNodeInbound(ctx context.Context, q store.DBTX, ni *store.NodeInbound) (*xrayconf.RenderedInbound, error) {
	return s.renderNodeInboundWith(ctx, q, ni, nil)
}

// renderNodeInboundWith renders with candidate instead of the stored template of the same id.
func (s *Service) renderNodeInboundWith(ctx context.Context, q store.DBTX, ni *store.NodeInbound, candidate *xrayconf.Template) (*xrayconf.RenderedInbound, error) {
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
	tpl := candidate
	if tpl == nil || tpl.ID != p.TemplateID {
		if tpl, err = s.template(ctx, p.TemplateID); err != nil {
			return nil, err
		}
	}
	if tpl, err = tpl.WithSources(p.Inbound, p.Host); err != nil {
		return nil, invalid("profile %s: %v", p.Name, err)
	}
	r, err := xrayconf.RenderInbound(xrayconf.InboundSpec{
		TemplateID: p.TemplateID, Template: tpl, ProfileValues: p.Values, NodeValues: ni.Values,
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

// ClientOf reads a profile's own client config (zero when it has none).
func ClientOf(p *store.Profile) ProfileClient {
	var c ProfileClient
	if p.Client != "" {
		_ = json.Unmarshal([]byte(p.Client), &c)
	}
	return c
}
