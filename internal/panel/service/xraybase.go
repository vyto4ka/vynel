package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

// The base config is the part of every node's Xray config that is not an inbound: dns,
// outbounds, routing and the rest. The «Xray» page edits it as JSON, so anything Xray can do is
// possible without a form for it. Inbounds come from profiles, api/stats/policy are the panel's
// own, and the log level is a setting of the node.

// BaseConfigInfo is the stored base config and what it can be reset to.
type BaseConfigInfo struct {
	JSON       string   `json:"json"`
	Default    string   `json:"default"`
	Customized bool     `json:"customized"`
	PanelKeys  []string `json:"panelKeys"` // sections the panel writes itself and refuses here
}

// BaseConfig returns the default base config.
func (s *Service) BaseConfig(ctx context.Context) (*BaseConfigInfo, error) {
	b, err := store.GetBaseConfig(ctx, s.st.DB, nil)
	if errors.Is(err, store.ErrNotFound) {
		b = &store.BaseConfig{JSON: xrayconf.DefaultBaseJSON}
	} else if err != nil {
		return nil, err
	}
	return &BaseConfigInfo{JSON: b.JSON, Default: xrayconf.DefaultBaseJSON, Customized: !sameJSON(b.JSON, xrayconf.DefaultBaseJSON),
		PanelKeys: append([]string{"inbounds"}, xrayconf.SystemKeys...)}, nil
}

func sameJSON(a, b string) bool {
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return false
	}
	xa, _ := json.Marshal(x)
	ya, _ := json.Marshal(y)
	return string(xa) == string(ya)
}

// BaseCheckNode is the config one node would run with the checked base config.
type BaseCheckNode struct {
	ID     int64
	Code   string
	Name   string
	Config []byte
}

// BaseCheck is the result of building every node's config with a base config that is not saved yet.
type BaseCheck struct {
	// Problems are what the new base config breaks on nodes that worked before.
	Problems []string
	Nodes    []BaseCheckNode
}

// parseBase checks a base config the way nodes will read it and words the failure for the editor.
func parseBase(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return invalid("конфиг пустой: нужен JSON-объект { … }")
	}
	var probe any
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return invalid("%v", err)
	}
	if _, ok := probe.(map[string]any); !ok {
		return invalid("нужен JSON-объект { … }")
	}
	if _, err := xrayconf.ParseBase(raw); err != nil {
		var key string
		if _, rest, ok := strings.Cut(err.Error(), `"`); ok {
			key, _, _ = strings.Cut(rest, `"`)
		}
		if key == "inbounds" {
			return invalid("раздел «inbounds» собирается из профилей и нод, здесь его быть не должно")
		}
		if key != "" {
			return invalid("раздел «%s» панель задаёт сама, здесь его быть не должно", key)
		}
		return invalid("%v", err)
	}
	return nil
}

// CheckBaseConfig builds the config of every node with raw as the base config. Nothing is saved.
func (s *Service) CheckBaseConfig(ctx context.Context, raw string) (*BaseCheck, error) {
	if err := parseBase(raw); err != nil {
		return nil, err
	}
	now, err := s.desiredStates(ctx, nil)
	if err != nil {
		return nil, err
	}
	next, err := s.desiredStates(ctx, &raw)
	if err != nil {
		return nil, invalid("%v", err)
	}
	nodes, err := store.ListNodes(ctx, s.st.DB)
	if err != nil {
		return nil, err
	}
	out := &BaseCheck{}
	for _, n := range nodes {
		ds, ok := next[n.ID]
		if !ok {
			continue
		}
		was := map[string]bool{}
		if old := now[n.ID]; old != nil {
			for _, p := range old.Problems {
				was[p] = true
			}
		}
		for _, p := range ds.Problems {
			if !was[p] {
				out.Problems = append(out.Problems, fmt.Sprintf("%s: %s", n.Code, p))
			}
		}
		out.Nodes = append(out.Nodes, BaseCheckNode{ID: n.ID, Code: n.Code, Name: n.Name, Config: ds.Config})
	}
	if len(out.Nodes) == 0 {
		// No node yet: the base config on its own, so Xray still reads it.
		base, err := xrayconf.ParseBase(raw)
		if err != nil {
			return nil, invalid("%v", err)
		}
		cfg, err := xrayconf.BuildConfig(xrayconf.NodeConfig{Base: base, APIAddr: "127.0.0.1:" + strconv.Itoa(DefaultXrayAPIPort)})
		if err != nil {
			return nil, invalid("%v", err)
		}
		b, err := json.Marshal(cfg)
		if err != nil {
			return nil, err
		}
		out.Nodes = append(out.Nodes, BaseCheckNode{Code: "—", Name: "без нод", Config: b})
	}
	return out, nil
}

// SaveBaseConfig stores the default base config; nodes get it at once.
func (s *Service) SaveBaseConfig(ctx context.Context, actor Actor, raw string) error {
	chk, err := s.CheckBaseConfig(ctx, raw)
	if err != nil {
		return err
	}
	if len(chk.Problems) > 0 {
		return invalid("с таким конфигом сломаются ноды: %s", strings.Join(chk.Problems, "; "))
	}
	return s.mutate(ctx, change{actor: actor, action: "xray.base.save", entity: "base_config", event: EvSettingChanged, diff: map[string]int{"bytes": len(raw)}},
		func(q store.DBTX) error {
			b, err := store.GetBaseConfig(ctx, q, nil)
			if errors.Is(err, store.ErrNotFound) {
				b = &store.BaseConfig{Name: "default", IsDefault: true}
			} else if err != nil {
				return err
			}
			b.JSON = strings.TrimSpace(raw)
			return store.UpsertBaseConfig(ctx, q, b)
		})
}

// ConfigOrigin says who put a part of a node's config there. Paths are JSON paths of the result
// ("inbounds.0", "outbounds.2"); Kind is a code the page words itself.
type ConfigOrigin struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	Note string `json:"note,omitempty"` // the inbound's tag, the cascade's address…
}

// NodeConfigPreview is the whole Xray config a node would run, with raw as the base config (empty
// = the stored one). Secrets are masked and the users left out: it is for reading. Origins lists
// what the panel added to the base config, so nothing in the result is a surprise.
func (s *Service) NodeConfigPreview(ctx context.Context, nodeID int64, raw string) (map[string]any, []string, []ConfigOrigin, error) {
	var base *string
	if strings.TrimSpace(raw) != "" {
		if err := parseBase(raw); err != nil {
			return nil, nil, nil, err
		}
		base = &raw
	} else {
		info, err := s.BaseConfig(ctx)
		if err != nil {
			return nil, nil, nil, err
		}
		raw = info.JSON
	}
	all, err := s.desiredStates(ctx, base)
	if err != nil {
		return nil, nil, nil, err
	}
	ds, ok := all[nodeID]
	if !ok {
		return nil, nil, nil, ErrNotFound
	}
	var cfg map[string]any
	if err := json.Unmarshal(ds.Config, &cfg); err != nil {
		return nil, nil, nil, err
	}
	baseMap, err := xrayconf.ParseBase(raw)
	if err != nil {
		return nil, nil, nil, invalid("%v", err)
	}
	origins := configOrigins(cfg, baseMap)
	maskConfig(cfg)
	return cfg, ds.Problems, origins, nil
}

// configOrigins finds the parts of the built config that the base config does not have.
func configOrigins(cfg, base map[string]any) []ConfigOrigin {
	var out []ConfigOrigin
	for _, k := range xrayconf.SystemKeys {
		if _, ok := cfg[k]; ok {
			out = append(out, ConfigOrigin{Path: k, Kind: k})
		}
	}
	if _, ok := cfg["log"]; ok {
		out = append(out, ConfigOrigin{Path: "log", Kind: "log"})
	}
	if ins, ok := cfg["inbounds"].([]any); ok {
		for i, in := range ins {
			tag, _ := in.(map[string]any)["tag"].(string)
			out = append(out, ConfigOrigin{Path: fmt.Sprintf("inbounds.%d", i), Kind: "inbound", Note: tag})
		}
	}
	if _, own := base["routing"]; !own {
		if _, ok := cfg["routing"]; ok {
			out = append(out, ConfigOrigin{Path: "routing", Kind: "routing"}) // an empty one: without it the API refuses rules
		}
	}
	baseOut, _ := base["outbounds"].([]any)
	if obs, ok := cfg["outbounds"].([]any); ok {
		for i := len(baseOut); i < len(obs); i++ {
			tag, _ := obs[i].(map[string]any)["tag"].(string)
			kind := "outbound_cascade"
			switch {
			case tag == xrayconf.IPBlockTag:
				kind = "outbound_block"
			case strings.HasPrefix(tag, xrayconf.DirectTag+"@"):
				kind = "outbound_egress"
			}
			out = append(out, ConfigOrigin{Path: fmt.Sprintf("outbounds.%d", i), Kind: kind, Note: tag})
		}
	}
	// A rule is the panel's when the base config has no identical one: cascades go in front, egress
	// rules before the first rule to DIRECT.
	ownRules := map[string]int{}
	if rt, ok := base["routing"].(map[string]any); ok {
		rules, _ := rt["rules"].([]any)
		for _, r := range rules {
			b, _ := json.Marshal(r)
			ownRules[string(b)]++
		}
	}
	if rt, ok := cfg["routing"].(map[string]any); ok {
		rules, _ := rt["rules"].([]any)
		for i, r := range rules {
			b, _ := json.Marshal(r)
			if ownRules[string(b)] > 0 {
				ownRules[string(b)]--
				continue
			}
			m, _ := r.(map[string]any)
			tag, _ := m["outboundTag"].(string)
			kind := "rule_cascade"
			if strings.HasPrefix(tag, xrayconf.DirectTag+"@") {
				kind = "rule_egress"
			}
			out = append(out, ConfigOrigin{Path: fmt.Sprintf("routing.rules.%d", i), Kind: kind, Note: tag})
		}
	}
	return out
}

// maskConfig hides the Reality private keys and the password of Hysteria in place.
func maskConfig(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if k == "privateKey" || k == "password" {
				if _, ok := x.(string); ok {
					t[k] = "•••"
					continue
				}
			}
			maskConfig(x)
		}
	case []any:
		for _, x := range t {
			maskConfig(x)
		}
	}
}
