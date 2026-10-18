package xrayconf

import (
	"encoding/json"
	"fmt"
)

// InboundSpec is everything needed to render one node inbound (docs/INBOUNDS.md §1.4).
type InboundSpec struct {
	TemplateID      string
	Template        *Template      // used instead of TemplateID when set (checking an unsaved template)
	ProfileValues   map[string]any // scope=profile values stored on the profile
	NodeValues      map[string]any // scope=node values stored on the node inbound
	ProfileOverride map[string]any // merge patch stored on the profile
	NodeOverride    map[string]any // merge patch stored on the node inbound
	PortOverride    int            // >0 replaces "port"
	EgressIP        string         // sendThrough address; empty = default route
	Context         NodeContext
}

// RenderedInbound is a ready-to-use inbound plus what BuildConfig and the agent need to know about it.
type RenderedInbound struct {
	Tag      string         `json:"tag"`
	Protocol string         `json:"protocol"`
	Flow     string         `json:"flow,omitempty"`
	EgressIP string         `json:"egress_ip,omitempty"`
	Inbound  map[string]any `json:"inbound"` // without clients
	Values   map[string]any `json:"-"`       // resolved variables (host rendering, checks)
	Template *Template      `json:"-"`
}

// RenderInbound renders template ⊕ profile ⊕ node values ⊕ overrides ⊕ system fields.
func RenderInbound(spec InboundSpec) (*RenderedInbound, error) {
	t := spec.Template
	if t == nil {
		var err error
		if t, err = GetTemplate(spec.TemplateID); err != nil {
			return nil, err
		}
	}
	vals, err := t.ResolveValues(spec.ProfileValues, spec.NodeValues, spec.Context)
	if err != nil {
		return nil, err
	}
	in, err := substitute(deepCopy(t.inbound), vals)
	if err != nil {
		return nil, fmt.Errorf("template %s: %w", t.ID, err)
	}
	inbound := in.(map[string]any)
	if spec.ProfileOverride != nil {
		inbound = MergePatch(inbound, deepCopy(spec.ProfileOverride))
	}
	if spec.NodeOverride != nil {
		inbound = MergePatch(inbound, deepCopy(spec.NodeOverride))
	}
	normalizeTree(inbound)
	tag, _ := vals["TAG"].(string)
	inbound["tag"] = tag
	if spec.PortOverride > 0 {
		inbound["port"] = spec.PortOverride
	}
	if settings, ok := inbound["settings"].(map[string]any); ok {
		settings["clients"] = []any{}
	}
	protocol, _ := inbound["protocol"].(string)
	r := &RenderedInbound{
		Tag:      tag,
		Protocol: protocol,
		Flow:     t.Client.Flow,
		EgressIP: spec.EgressIP,
		Inbound:  inbound,
		Values:   vals,
		Template: t,
	}
	if err := Lint(t, r); err != nil {
		return nil, err
	}
	return r, nil
}

// MarshalIndent is a stable pretty-printer for configs (sorted keys via encoding/json maps).
func MarshalIndent(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}
