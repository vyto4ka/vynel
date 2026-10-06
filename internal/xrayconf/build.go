package xrayconf

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"
)

// DefaultBaseJSON is the shared base config (docs/PROFILES.md §2): the same log/outbounds/routing
// as the working RemnaSetup profile.
const DefaultBaseJSON = `{
  "log": { "loglevel": "none" },
  "outbounds": [
    { "tag": "DIRECT", "protocol": "freedom", "settings": { "domainStrategy": "UseIPv4" } },
    { "tag": "BLOCK", "protocol": "blackhole" }
  ],
  "routing": {
    "rules": [
      { "ip": ["geoip:private"], "outboundTag": "BLOCK" },
      { "domain": ["geosite:private"], "outboundTag": "BLOCK" },
      { "protocol": ["bittorrent"], "outboundTag": "BLOCK" },
      { "network": "tcp,udp", "outboundTag": "DIRECT" }
    ]
  }
}`

// DirectTag is the default outbound the egress rules clone.
const DirectTag = "DIRECT"

// Client is a user written into an inbound's clients list.
type Client struct {
	Email string `json:"email"` // stable user key for stats, never the username
	ID    string `json:"id"`    // VLESS UUID
	Flow  string `json:"flow,omitempty"`
}

// NodeConfig is the input to BuildConfig.
type NodeConfig struct {
	Base     map[string]any
	Inbounds []*RenderedInbound
	Clients  map[string][]Client // by inbound tag; may be nil (users added later via API)
	APIAddr  string              // e.g. 127.0.0.1:10085
}

// SystemKeys are the top-level sections the panel owns. They are hidden from the editor.
var SystemKeys = []string{"api", "stats", "policy"}

// ParseBase decodes a base config and rejects sections the panel manages itself.
func ParseBase(raw string) (map[string]any, error) {
	var base map[string]any
	if err := json.Unmarshal([]byte(raw), &base); err != nil {
		return nil, fmt.Errorf("base config: %w", err)
	}
	for _, k := range append([]string{"inbounds"}, SystemKeys...) {
		if _, ok := base[k]; ok {
			return nil, fmt.Errorf("base config: %q is managed by the panel and must not be set", k)
		}
	}
	return base, nil
}

// BuildConfig assembles the full Xray config for a node.
func BuildConfig(nc NodeConfig) (map[string]any, error) {
	if nc.APIAddr == "" {
		return nil, fmt.Errorf("api address is required")
	}
	if _, _, err := net.SplitHostPort(nc.APIAddr); err != nil {
		return nil, fmt.Errorf("api address: %w", err)
	}
	cfg := deepCopy(nc.Base)
	if cfg == nil {
		cfg = map[string]any{}
	}

	seenTags := map[string]bool{}
	seenListen := map[string]string{}
	inbounds := make([]any, 0, len(nc.Inbounds))
	for _, r := range nc.Inbounds {
		if seenTags[r.Tag] {
			return nil, fmt.Errorf("duplicate inbound tag %s", r.Tag)
		}
		seenTags[r.Tag] = true
		key := listenKey(r.Inbound)
		if other, ok := seenListen[key]; ok {
			return nil, fmt.Errorf("inbounds %s and %s both listen on %s", other, r.Tag, key)
		}
		seenListen[key] = r.Tag
		in := deepCopy(r.Inbound)
		if settings, ok := in["settings"].(map[string]any); ok {
			settings["clients"] = clientsJSON(nc.Clients[r.Tag], r.Flow)
		}
		inbounds = append(inbounds, in)
	}
	cfg["inbounds"] = inbounds

	if err := addEgress(cfg, nc.Inbounds); err != nil {
		return nil, err
	}

	cfg["api"] = map[string]any{
		"tag":      "api",
		"listen":   nc.APIAddr,
		"services": []any{"HandlerService", "StatsService", "LoggerService"},
	}
	cfg["stats"] = map[string]any{}
	cfg["policy"] = map[string]any{
		"levels": map[string]any{
			"0": map[string]any{
				"statsUserUplink":   true,
				"statsUserDownlink": true,
				"statsUserOnline":   true,
			},
		},
		"system": map[string]any{
			"statsInboundUplink":    true,
			"statsInboundDownlink":  true,
			"statsOutboundUplink":   true,
			"statsOutboundDownlink": true,
		},
	}
	return cfg, nil
}

// ClientJSON renders one VLESS client entry.
func ClientJSON(c Client, flow string) map[string]any {
	m := map[string]any{"id": c.ID, "email": c.Email, "level": 0}
	if c.Flow != "" {
		flow = c.Flow
	}
	if flow != "" {
		m["flow"] = flow
	}
	return m
}

func clientsJSON(cs []Client, flow string) []any {
	sorted := append([]Client(nil), cs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Email < sorted[j].Email })
	out := make([]any, 0, len(sorted))
	for _, c := range sorted {
		out = append(out, ClientJSON(c, flow))
	}
	return out
}

func listenKey(in map[string]any) string {
	listen, _ := in["listen"].(string)
	if listen == "" {
		listen = "0.0.0.0"
	}
	port, _ := toInt(in["port"])
	return net.JoinHostPort(listen, strconv.Itoa(port))
}

// addEgress makes traffic from inbounds with an egress IP leave through that IP
// (docs/INBOUNDS.md §2.4): a DIRECT@ip outbound with sendThrough plus an inboundTag rule
// inserted right before the first rule that routes to DIRECT, so block rules still apply first.
func addEgress(cfg map[string]any, inbounds []*RenderedInbound) error {
	byIP := map[string][]string{}
	var ips []string
	for _, r := range inbounds {
		if r.EgressIP == "" {
			continue
		}
		if net.ParseIP(r.EgressIP) == nil {
			return fmt.Errorf("inbound %s: bad egress IP %q", r.Tag, r.EgressIP)
		}
		if _, ok := byIP[r.EgressIP]; !ok {
			ips = append(ips, r.EgressIP)
		}
		byIP[r.EgressIP] = append(byIP[r.EgressIP], r.Tag)
	}
	if len(ips) == 0 {
		return nil
	}
	outbounds, _ := cfg["outbounds"].([]any)
	var direct map[string]any
	for _, o := range outbounds {
		if m, ok := o.(map[string]any); ok && m["tag"] == DirectTag {
			direct = m
		}
	}
	if direct == nil {
		return fmt.Errorf("base config has no %q outbound to clone for egress", DirectTag)
	}
	var rules []any
	for _, ip := range ips {
		tag := DirectTag + "@" + ip
		o := deepCopy(direct)
		o["tag"] = tag
		o["sendThrough"] = ip
		outbounds = append(outbounds, o)
		tags := make([]any, 0, len(byIP[ip]))
		for _, t := range byIP[ip] {
			tags = append(tags, t)
		}
		rules = append(rules, map[string]any{"inboundTag": tags, "outboundTag": tag})
	}
	cfg["outbounds"] = outbounds

	routing, _ := cfg["routing"].(map[string]any)
	if routing == nil {
		routing = map[string]any{}
		cfg["routing"] = routing
	}
	existing, _ := routing["rules"].([]any)
	at := len(existing)
	for i, r := range existing {
		if m, ok := r.(map[string]any); ok && m["outboundTag"] == DirectTag {
			at = i
			break
		}
	}
	merged := make([]any, 0, len(existing)+len(rules))
	merged = append(merged, existing[:at]...)
	merged = append(merged, rules...)
	merged = append(merged, existing[at:]...)
	routing["rules"] = merged
	return nil
}
