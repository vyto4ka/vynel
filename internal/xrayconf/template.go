// Package xrayconf turns profile templates into Xray configs (docs/PROFILES.md, docs/INBOUNDS.md).
//
// Layers: Template (embedded YAML) → Profile (defaults + override) → node inbound
// (node variables + override + listen/egress IP) → full node config with system sections.
package xrayconf

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed templates/*.yaml
var templatesFS embed.FS

// Templates that used to be built in. They are not offered any more; a panel whose profiles still
// use one gets it as a custom template on start (service.EnsureDefaults), so nothing breaks.
//
//go:embed retired/*.yaml
var retiredFS embed.FS

// Template is a profile template: built-in (embedded YAML) or written in the panel.
type Template struct {
	ID            string         `yaml:"id" json:"id"`
	Version       int            `yaml:"version" json:"version"`
	Title         string         `yaml:"title" json:"title"`
	Summary       string         `yaml:"summary" json:"summary"`
	TagPattern    string         `yaml:"tag_pattern" json:"tag_pattern"`
	RemarkPattern string         `yaml:"remark_pattern" json:"remark_pattern"`
	Protocol      string         `yaml:"protocol" json:"protocol"`
	Client        ClientSpec     `yaml:"client" json:"client"`
	Lint          []string       `yaml:"lint" json:"lint"`
	Variables     []Variable     `yaml:"variables" json:"variables"`
	XrayInbound   string         `yaml:"xray_inbound" json:"-"`
	XHTTPExtra    string         `yaml:"xhttp_extra" json:"-"`
	Host          map[string]any `yaml:"host" json:"host,omitempty"`
	Nginx         map[string]any `yaml:"nginx" json:"nginx,omitempty"`
	Caddy         map[string]any `yaml:"caddy" json:"caddy,omitempty"` // the old name of the nginx section
	Requirements  map[string]any `yaml:"node_requirements" json:"node_requirements,omitempty"`

	Custom bool   `yaml:"-" json:"custom"` // written in the panel, editable
	Source string `yaml:"-" json:"source"` // the YAML it came from

	inbound map[string]any // parsed XrayInbound
	extra   map[string]any // parsed XHTTPExtra
}

// ClientSpec describes how users are written into the inbound's clients list.
type ClientSpec struct {
	Flow string `yaml:"flow" json:"flow"`
}

// Variable returns the variable with the given name.
func (t *Template) Variable(name string) (Variable, bool) {
	for _, v := range t.Variables {
		if v.Name == name {
			return v, true
		}
	}
	return Variable{}, false
}

func (t *Template) parse() error {
	if t.ID == "" {
		return fmt.Errorf("template without id")
	}
	if err := json.Unmarshal([]byte(t.XrayInbound), &t.inbound); err != nil {
		return fmt.Errorf("template %s: xray_inbound: %w", t.ID, err)
	}
	if t.XHTTPExtra != "" {
		if err := json.Unmarshal([]byte(t.XHTTPExtra), &t.extra); err != nil {
			return fmt.Errorf("template %s: xhttp_extra: %w", t.ID, err)
		}
	}
	seen := map[string]bool{}
	for i := range t.Variables {
		v := &t.Variables[i]
		if seen[v.Name] {
			return fmt.Errorf("template %s: duplicate variable %s", t.ID, v.Name)
		}
		if _, builtin := builtinVars[v.Name]; builtin {
			return fmt.Errorf("template %s: variable %s shadows a built-in", t.ID, v.Name)
		}
		seen[v.Name] = true
		if err := v.check(); err != nil {
			return fmt.Errorf("template %s: %w", t.ID, err)
		}
	}
	for _, rule := range t.Lint {
		if _, ok := lintRules[rule]; !ok {
			return fmt.Errorf("template %s: unknown lint rule %q", t.ID, rule)
		}
	}
	if t.Nginx != nil && t.Caddy != nil {
		return fmt.Errorf("template %s: both nginx and caddy sections (caddy is the old name of nginx, keep one)", t.ID)
	}
	if err := checkWeb(t.Web()); err != nil {
		return fmt.Errorf("template %s: nginx: %w", t.ID, err)
	}
	return nil
}

// Web is the template's web server section: what nginx serves for the inbound
// (docs/PROFILES.md §1.3). Templates written for Caddy call it "caddy".
func (t *Template) Web() map[string]any {
	if t.Nginx != nil {
		return t.Nginx
	}
	return t.Caddy
}

// WebRoles are the roles of the nginx section.
var WebRoles = []string{"selfsteal", "reverse_proxy_xhttp", "certificate"}

// webKeys are the keys of the nginx section and what they hold.
var webKeys = map[string]string{
	"role": "string", "domain": "string", "local_port": "scalar", "decoy": "string", "upstream": "string",
	"path": "string", "server_names": "list", "location": "string", "server": "string",
}

func checkWeb(w map[string]any) error {
	if w == nil {
		return nil
	}
	role, _ := w["role"].(string)
	known := false
	for _, r := range WebRoles {
		known = known || r == role
	}
	if !known {
		return fmt.Errorf("role %q is not one of %v", role, WebRoles)
	}
	for k, v := range w {
		kind, ok := webKeys[k]
		if !ok {
			return fmt.Errorf("unknown key %q", k)
		}
		switch kind {
		case "string":
			if _, ok := v.(string); !ok {
				return fmt.Errorf("%s must be a string", k)
			}
		case "list":
			l, ok := v.([]any)
			if !ok {
				return fmt.Errorf("%s must be a list", k)
			}
			for _, x := range l {
				if _, ok := x.(string); !ok {
					return fmt.Errorf("%s must list strings", k)
				}
			}
		}
	}
	if w["location"] != nil && role != "reverse_proxy_xhttp" {
		return fmt.Errorf("location is for role reverse_proxy_xhttp")
	}
	return nil
}

var (
	loadOnce  sync.Once
	templates map[string]*Template
	loadErr   error
)

func load() {
	templates = map[string]*Template{}
	loadErr = fs.WalkDir(templatesFS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := templatesFS.ReadFile(path)
		if err != nil {
			return err
		}
		t, err := ParseTemplate(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if _, dup := templates[t.ID]; dup {
			return fmt.Errorf("%s: duplicate template id %s", path, t.ID)
		}
		templates[t.ID] = t
		return nil
	})
}

// ParseTemplate parses and checks a template written in YAML.
func ParseTemplate(raw []byte) (*Template, error) {
	t := &Template{}
	if err := yaml.Unmarshal(raw, t); err != nil {
		return nil, fmt.Errorf("YAML: %w", err)
	}
	if err := t.parse(); err != nil {
		return nil, err
	}
	t.Source = string(raw)
	return t, nil
}

// IsBuiltin reports whether id is a built-in template.
func IsBuiltin(id string) bool {
	loadOnce.Do(load)
	_, ok := templates[id]
	return ok
}

// Templates returns the built-in templates sorted by id. Templates written in the panel live
// in its database (service.ProfileTemplates).
func Templates() ([]*Template, error) {
	loadOnce.Do(load)
	if loadErr != nil {
		return nil, loadErr
	}
	out := make([]*Template, 0, len(templates))
	for _, t := range templates {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// RetiredTemplate returns the YAML source of a template that is no longer built in.
func RetiredTemplate(id string) (string, bool) {
	raw, err := retiredFS.ReadFile("retired/" + id + ".yaml")
	if err != nil {
		return "", false
	}
	return string(raw), true
}

// GetTemplate returns a built-in template by id.
func GetTemplate(id string) (*Template, error) {
	loadOnce.Do(load)
	if loadErr != nil {
		return nil, loadErr
	}
	t, ok := templates[id]
	if !ok {
		return nil, fmt.Errorf("unknown template %q", id)
	}
	return t, nil
}
