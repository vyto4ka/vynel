package xrayconf

import (
	"fmt"
	"sort"
)

// SampleContext is the made-up node a template is checked against.
var SampleContext = NodeContext{Code: "NL", Name: "Нидерланды", Country: "NL", Domain: "nl.example.com"}

// sampleValue is a plausible value for an input variable without a default.
func sampleValue(v Variable) any {
	switch v.Validate {
	case "domain":
		return "sample.example.com"
	case "port":
		return 443
	case "path":
		return "/sample"
	case "ip":
		return "203.0.113.10"
	}
	if len(v.Options) > 0 {
		return v.Options[0]
	}
	return "sample"
}

// SampleRender checks a template end to end: it generates keys, fills inputs with defaults or
// sample values, renders the inbound (with lint) and expands the host and caddy sections, so
// a typo in a ${VARIABLE} anywhere is found before the template is saved.
func SampleRender(t *Template) (*RenderedInbound, error) {
	profile, node := map[string]any{}, map[string]any{}
	for _, v := range t.Variables {
		if v.Source != SourceInput || v.Default != nil || v.DefaultFrom != "" {
			continue
		}
		if v.Scope == ScopeNode {
			node[v.Name] = sampleValue(v)
		} else {
			profile[v.Name] = sampleValue(v)
		}
	}
	for scope, vals := range map[Scope]map[string]any{ScopeProfile: profile, ScopeNode: node} {
		gen, err := t.GenerateValues(scope, vals)
		if err != nil {
			return nil, err
		}
		for k, v := range gen {
			vals[k] = v
		}
	}
	r, err := RenderInbound(InboundSpec{Template: t, ProfileValues: profile, NodeValues: node, Context: SampleContext})
	if err != nil {
		return nil, err
	}
	vals := map[string]any{}
	for k, v := range r.Values {
		vals[k] = v
	}
	vals["INBOUND_PORT"], _ = r.Inbound["port"].(int)
	for name, tree := range map[string]map[string]any{"host": t.Host, "caddy": t.Caddy} {
		if tree == nil {
			continue
		}
		if _, err := ExpandTree(tree, vals); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
	}
	return r, nil
}

// TemplateReference lists what a template may use, for the editor's help.
type TemplateReference struct {
	Builtins    []string `json:"builtins"`
	Generators  []string `json:"generators"`
	Derivations []string `json:"derivations"`
	Lint        []string `json:"lint"`
	Validations []string `json:"validations"`
	CaddyRoles  []string `json:"caddyRoles"`
}

// Reference returns the names known to the renderer.
func Reference() TemplateReference {
	keys := func(m any) []string {
		var out []string
		switch mm := m.(type) {
		case map[string]struct{}:
			for k := range mm {
				out = append(out, k)
			}
		case map[string]func(Variable) (any, error):
			for k := range mm {
				out = append(out, k)
			}
		case map[string]func(string) (any, error):
			for k := range mm {
				out = append(out, k)
			}
		case map[string]func(map[string]any) []string:
			for k := range mm {
				out = append(out, k)
			}
		}
		sort.Strings(out)
		return out
	}
	return TemplateReference{
		Builtins: keys(builtinVars), Generators: keys(generators), Derivations: keys(derivations), Lint: keys(lintRules),
		Validations: []string{"domain", "port", "path", "ip"}, CaddyRoles: []string{"selfsteal", "reverse_proxy_xhttp", "certificate"},
	}
}
