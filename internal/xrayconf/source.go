package xrayconf

import (
	"encoding/json"
	"fmt"
)

// A profile can carry its own copy of the template's source: the inbound JSON and the
// connection point (host) with ${VARIABLES} still in place. The panel shows and edits them as
// plain JSON instead of patches on top of the template.

// SourceInbound is the template's inbound with ${VARIABLES} unexpanded and the xhttp extra
// inlined, so the whole server side is one JSON object.
func (t *Template) SourceInbound() map[string]any {
	in := deepCopy(t.inbound)
	if t.extra == nil {
		return in
	}
	var walk func(v any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			for k, c := range x {
				x[k] = walk(c)
			}
		case []any:
			for i, c := range x {
				x[i] = walk(c)
			}
		case string:
			if x == "${XHTTP_EXTRA}" {
				return deepCopy(t.extra)
			}
		}
		return v
	}
	walk(in)
	return in
}

// SourceHost is the template's connection point with ${VARIABLES} unexpanded.
func (t *Template) SourceHost() map[string]any {
	if t.Host == nil {
		return map[string]any{}
	}
	return deepCopy(t.Host)
}

// WithSources returns a copy of the template whose inbound and/or host are replaced; nil keeps
// the template's own. Variables, Caddy and lint rules stay the template's.
func (t *Template) WithSources(inbound, host map[string]any) (*Template, error) {
	if inbound == nil && host == nil {
		return t, nil
	}
	c := *t
	if inbound != nil {
		if p, _ := inbound["protocol"].(string); p == "" {
			return nil, fmt.Errorf("inbound JSON: \"protocol\" is required")
		}
		if _, ok := inbound["settings"].(map[string]any); !ok {
			return nil, fmt.Errorf("inbound JSON: \"settings\" object is required (clients are added there)")
		}
		c.inbound = deepCopy(inbound)
		raw, err := json.MarshalIndent(inbound, "", "  ")
		if err != nil {
			return nil, err
		}
		c.XrayInbound = string(raw)
	}
	if host != nil {
		c.Host = deepCopy(host)
	}
	return &c, nil
}
