package xrayconf

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// Scope says whether a variable is shared by the profile or set per node inbound.
type Scope string

const (
	ScopeProfile Scope = "profile"
	ScopeNode    Scope = "node"
)

// Source says where a variable's value comes from.
type Source string

const (
	SourceInput    Source = "input"    // typed by the admin (optionally prefilled via default_from)
	SourceGenerate Source = "generate" // generated once and stored
	SourceConst    Source = "const"    // default value, can be overridden in the profile
	SourceDerived  Source = "derived"  // computed from other variables, never stored
)

// Variable is a template variable (docs/PROFILES.md §1.2).
type Variable struct {
	Name        string   `yaml:"name" json:"name"`
	Scope       Scope    `yaml:"scope" json:"scope"`
	Source      Source   `yaml:"source" json:"source"`
	Generator   string   `yaml:"generator" json:"generator,omitempty"`
	Options     []string `yaml:"options" json:"options,omitempty"`
	Default     any      `yaml:"default" json:"default,omitempty"`
	DefaultFrom string   `yaml:"default_from" json:"default_from,omitempty"`
	From        string   `yaml:"from" json:"from,omitempty"`
	Secret      bool     `yaml:"secret" json:"secret,omitempty"`
	Optional    bool     `yaml:"optional" json:"optional,omitempty"`
	Validate    string   `yaml:"validate" json:"validate,omitempty"`
	Description string   `yaml:"description" json:"description,omitempty"`
}

var derivedRe = regexp.MustCompile(`^(\w+)\((\w+)\)$`)

func (v *Variable) check() error {
	switch v.Scope {
	case ScopeProfile, ScopeNode:
	default:
		return fmt.Errorf("variable %s: bad scope %q", v.Name, v.Scope)
	}
	switch v.Source {
	case SourceInput, SourceConst:
	case SourceGenerate:
		if _, ok := generators[v.Generator]; !ok {
			return fmt.Errorf("variable %s: unknown generator %q", v.Name, v.Generator)
		}
	case SourceDerived:
		m := derivedRe.FindStringSubmatch(v.From)
		if m == nil {
			return fmt.Errorf("variable %s: bad derived expression %q", v.Name, v.From)
		}
		if _, ok := derivations[m[1]]; !ok {
			return fmt.Errorf("variable %s: unknown derivation %q", v.Name, m[1])
		}
	default:
		return fmt.Errorf("variable %s: bad source %q", v.Name, v.Source)
	}
	if v.DefaultFrom != "" && v.DefaultFrom != "node.domain" {
		return fmt.Errorf("variable %s: unsupported default_from %q", v.Name, v.DefaultFrom)
	}
	return nil
}

// NodeContext is what the renderer knows about the node and the inbound being rendered.
type NodeContext struct {
	Code     string // NODE_CODE, e.g. "NL"
	Name     string // NODE_NAME, e.g. "Нидерланды"
	Country  string // ISO 3166-1 alpha-2, e.g. "NL"
	Domain   string // domain bound to the inbound's address (default for default_from: node.domain)
	ListenIP string // LISTEN_IP; "0.0.0.0" when empty
	Tag      string // TAG; when empty it is rendered from the template's tag_pattern
}

// builtinVars are always available and cannot be declared by templates.
var builtinVars = map[string]struct{}{
	"TAG": {}, "LISTEN_IP": {}, "NODE_CODE": {}, "NODE_NAME": {}, "NODE_FLAG": {},
	"NODE_COUNTRY": {}, "XHTTP_EXTRA": {}, "INBOUND_PORT": {},
}

func (c NodeContext) builtins() map[string]any {
	listen := c.ListenIP
	if listen == "" {
		listen = "0.0.0.0"
	}
	return map[string]any{
		"LISTEN_IP":    listen,
		"NODE_CODE":    c.Code,
		"NODE_NAME":    c.Name,
		"NODE_FLAG":    CountryFlag(c.Country),
		"NODE_COUNTRY": strings.ToUpper(c.Country),
	}
}

// CountryFlag converts "NL" into the 🇳🇱 emoji. Unknown input yields "".
func CountryFlag(cc string) string {
	cc = strings.ToUpper(cc)
	if len(cc) != 2 || cc[0] < 'A' || cc[0] > 'Z' || cc[1] < 'A' || cc[1] > 'Z' {
		return ""
	}
	return string([]rune{rune(cc[0]) - 'A' + 0x1F1E6, rune(cc[1]) - 'A' + 0x1F1E6})
}

// GenerateValues fills in generated variables of the given scope that are missing from existing.
// It returns only the newly generated values; callers persist them next to existing ones.
func (t *Template) GenerateValues(scope Scope, existing map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for _, v := range t.Variables {
		if v.Scope != scope || v.Source != SourceGenerate {
			continue
		}
		if _, ok := existing[v.Name]; ok {
			continue
		}
		val, err := generators[v.Generator](v)
		if err != nil {
			return nil, fmt.Errorf("generate %s: %w", v.Name, err)
		}
		out[v.Name] = val
	}
	return out, nil
}

// ResolveValues computes the final value of every variable for one node inbound,
// plus built-ins. Missing required values are reported together.
func (t *Template) ResolveValues(profileVals, nodeVals map[string]any, ctx NodeContext) (map[string]any, error) {
	vals := ctx.builtins()
	var missing []string
	for _, v := range t.Variables {
		if v.Source == SourceDerived {
			continue
		}
		src := profileVals
		if v.Scope == ScopeNode {
			src = nodeVals
		}
		val, ok := src[v.Name]
		if !ok || isEmpty(val) {
			switch {
			case v.DefaultFrom == "node.domain" && ctx.Domain != "":
				val, ok = ctx.Domain, true
			case v.Default != nil:
				val, ok = v.Default, true
			default:
				ok = false
			}
		}
		if !ok {
			if !v.Optional {
				missing = append(missing, v.Name)
			}
			vals[v.Name] = ""
			continue
		}
		if err := validateValue(v, val); err != nil {
			return nil, err
		}
		vals[v.Name] = normalize(val)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("template %s: missing values for %s", t.ID, strings.Join(missing, ", "))
	}
	for _, v := range t.Variables {
		if v.Source != SourceDerived {
			continue
		}
		m := derivedRe.FindStringSubmatch(v.From)
		arg, _ := vals[m[2]].(string)
		val, err := derivations[m[1]](arg)
		if err != nil {
			return nil, fmt.Errorf("derive %s: %w", v.Name, err)
		}
		vals[v.Name] = val
	}
	tag := ctx.Tag
	if tag == "" {
		s, err := substString(t.TagPattern, vals)
		if err != nil {
			return nil, fmt.Errorf("tag_pattern: %w", err)
		}
		tag = s
	}
	vals["TAG"] = tag
	if t.extra != nil {
		extra, err := substitute(deepCopy(t.extra), vals)
		if err != nil {
			return nil, fmt.Errorf("xhttp_extra: %w", err)
		}
		vals["XHTTP_EXTRA"] = extra
	}
	return vals, nil
}

func isEmpty(v any) bool {
	s, ok := v.(string)
	return v == nil || (ok && s == "")
}

// normalize turns YAML/JSON numbers into int when they are whole, so ports render as 443, not 443.0.
func normalize(v any) any {
	switch n := v.(type) {
	case float64:
		if n == float64(int64(n)) {
			return int(n)
		}
	case int64:
		return int(n)
	}
	return v
}

var (
	domainRe = regexp.MustCompile(`^(?i)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
	pathRe   = regexp.MustCompile(`^/[A-Za-z0-9._~\-/]*[A-Za-z0-9._~\-]$`)
)

func validateValue(v Variable, val any) error {
	bad := func(why string) error { return fmt.Errorf("variable %s: %v %s", v.Name, val, why) }
	switch v.Validate {
	case "":
	case "domain":
		s, _ := val.(string)
		if !domainRe.MatchString(s) {
			return bad("is not a valid domain")
		}
	case "port":
		p, err := toInt(val)
		if err != nil || p < 1 || p > 65535 {
			return bad("is not a valid port")
		}
	case "path":
		s, _ := val.(string)
		if !pathRe.MatchString(s) {
			return bad("must start with / and must not end with /")
		}
	case "ip":
		s, _ := val.(string)
		if net.ParseIP(s) == nil {
			return bad("is not an IP address")
		}
	default:
		return fmt.Errorf("variable %s: unknown validation %q", v.Name, v.Validate)
	}
	return nil
}

func toInt(v any) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		if n != float64(int(n)) {
			return 0, fmt.Errorf("not an integer")
		}
		return int(n), nil
	case string:
		return strconv.Atoi(n)
	}
	return 0, fmt.Errorf("not a number")
}
