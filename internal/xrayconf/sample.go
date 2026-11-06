package xrayconf

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// unknownVarRe matches the "unknown variable ${NAME}" error of the resolver.
var unknownVarRe = regexp.MustCompile(`unknown variable \$\{(\w+)\}`)

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
// sample values, renders the inbound (with lint) and expands the host and nginx sections, so
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
	for name, tree := range map[string]map[string]any{"host": t.Host, "nginx": t.Web()} {
		if tree == nil {
			continue
		}
		if _, err := ExpandTree(tree, vals); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
	}
	return r, nil
}

// PreviewRender is SampleRender for the editor's live preview: it parses the YAML, resolves
// variables the same way (input variables get their defaults or sample values, generate
// variables are generated once in memory), renders the inbound and expands the host section,
// but it does not fail on an unusable id or tag_pattern, and it reports ${VAR} placeholders it
// could not resolve instead of erroring, so the editor can highlight them.
func PreviewRender(raw []byte) (*Template, *RenderedInbound, map[string]any, []string, error) {
	t, err := ParseTemplate(raw)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("YAML: %w", err)
	}
	if t.TagPattern == "" {
		t.TagPattern = "PREVIEW_${NODE_CODE}"
	}
	profile, node := map[string]any{}, map[string]any{}
	unknown := map[string]bool{}
	for _, v := range t.Variables {
		if v.Default != nil || v.DefaultFrom != "" {
			continue
		}
		switch v.Source {
		case SourceInput, SourceConst:
			if v.Scope == ScopeNode {
				node[v.Name] = sampleValue(v)
			} else {
				profile[v.Name] = sampleValue(v)
			}
		}
	}
	for scope, vals := range map[Scope]map[string]any{ScopeProfile: profile, ScopeNode: node} {
		gen, err := t.GenerateValues(scope, vals)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("generate: %w", err)
		}
		for k, v := range gen {
			vals[k] = v
		}
	}
	r, err := RenderInbound(InboundSpec{Template: t, ProfileValues: profile, NodeValues: node, Context: SampleContext})
	if err != nil {
		// An unknown ${VAR} is a template being half-written, not a dead end: render the rest
		// with every declared variable filled (placeholders stay where a value is still unknown)
		// and report the names the editor highlights.
		if !strings.Contains(err.Error(), "unknown variable") {
			return nil, nil, nil, nil, err
		}
		for _, name := range unknownVars(err.Error()) {
			unknown[name] = true
		}
		vals, _ := t.ResolveValuesLoose(profile, node, SampleContext)
		for _, v := range t.Variables {
			if _, ok := vals[v.Name]; !ok {
				vals[v.Name] = "${" + v.Name + "}"
			}
		}
		if vals == nil {
			vals = map[string]any{}
		}
		if in, inErr := substitute(t.inbound, vals); inErr != nil {
			for _, name := range unknownVars(inErr.Error()) {
				unknown[name] = true
			}
		} else if m, ok := in.(map[string]any); ok {
			normalizeTree(m)
		}
		tag, _ := vals["TAG"].(string)
		r = &RenderedInbound{Tag: tag, Protocol: "vless", Inbound: t.inbound, Values: vals, Template: t}
	}
	vals := map[string]any{}
	for k, v := range r.Values {
		vals[k] = v
	}
	vals["INBOUND_PORT"], _ = r.Inbound["port"].(int)
	var host map[string]any
	if t.Host != nil {
		h, err := ExpandTree(t.Host, vals)
		if err != nil {
			for _, name := range unknownVars(err.Error()) {
				unknown[name] = true
			}
		} else {
			host = h
		}
	}
	names := make([]string, 0, len(unknown))
	for name := range unknown {
		names = append(names, name)
	}
	sort.Strings(names)
	return t, r, host, names, nil
}

// unknownVars pulls ${NAME} names out of "unknown variable ${NAME}" messages.
func unknownVars(msg string) []string {
	out := []string{}
	for _, m := range unknownVarRe.FindAllStringSubmatch(msg, -1) {
		out = append(out, m[1])
	}
	return out
}

type TemplateReference struct {
	Builtins    []string `json:"builtins"`
	Generators  []string `json:"generators"`
	Derivations []string `json:"derivations"`
	Lint        []string `json:"lint"`
	Validations []string `json:"validations"`
	WebRoles    []string `json:"webRoles"`
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
		Validations: []string{"domain", "port", "path", "ip"}, WebRoles: WebRoles,
	}
}
