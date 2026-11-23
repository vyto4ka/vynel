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

// IPBlockTag is the blackhole outbound of addresses over a user's limit (docs/USER_GUIDE.md
// §6.1): the agent routes them there through the routing API.
const IPBlockTag = "VYNEL_BLOCK"

// WarpTag is the panel-managed Cloudflare WARP WireGuard outbound tag.
const WarpTag = "VYNEL_WARP"

// WarpConfig describes the panel-managed WireGuard/WARP outbound and routing rules for one node.
type WarpConfig struct {
	SecretKey      string
	Addresses      []string
	PeerPublicKey  string
	PresharedKey   string
	Reserved       []int
	Endpoint       string
	MTU            int
	Keepalive      int
	DomainStrategy string
	SendThrough    string
	RouteAll       bool
	Domains        []string
	IPs            []string
}

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
	// Outbounds are added after the base ones (cascades to other nodes); FrontRules go before
	// the base routing rules, which usually end with a catch-all.
	Outbounds  []any
	FrontRules []any
	// Warp configures the optional panel-managed Cloudflare WARP outbound and routing rules.
	Warp *WarpConfig
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
			settings["clients"] = clientsJSON(nc.Clients[r.Tag], r.Protocol, r.Flow)
		}
		inbounds = append(inbounds, in)
	}
	cfg["inbounds"] = inbounds

	// WARP rules go before DIRECT@<ip> egress rules and before the catch-all DIRECT rule,
	// while keeping base BLOCK rules and cascade FrontRules ahead of WARP.
	if err := addWarp(cfg, nc.Warp); err != nil {
		return nil, err
	}
	if err := addEgress(cfg, nc.Inbounds); err != nil {
		return nil, err
	}
	if len(nc.Outbounds) > 0 {
		obs, _ := cfg["outbounds"].([]any)
		cfg["outbounds"] = append(obs, deepCopy(map[string]any{"l": nc.Outbounds})["l"].([]any)...)
	}
	if len(nc.FrontRules) > 0 {
		routing, _ := cfg["routing"].(map[string]any)
		if routing == nil {
			routing = map[string]any{}
			cfg["routing"] = routing
		}
		rules, _ := routing["rules"].([]any)
		routing["rules"] = append(deepCopy(map[string]any{"l": nc.FrontRules})["l"].([]any), rules...)
	}

	obs, _ := cfg["outbounds"].([]any)
	hasBlock := false
	for _, o := range obs {
		if m, ok := o.(map[string]any); ok && m["tag"] == IPBlockTag {
			hasBlock = true
		}
	}
	if !hasBlock {
		// Last: the first outbound stays the default.
		cfg["outbounds"] = append(obs, map[string]any{"tag": IPBlockTag, "protocol": "blackhole"})
	}

	// Without a routing section Xray runs a stub router that refuses rules added through the API
	// (the blocks of addresses over the limit): there is always one.
	if _, ok := cfg["routing"].(map[string]any); !ok {
		cfg["routing"] = map[string]any{"rules": []any{}}
	}

	cfg["api"] = map[string]any{
		"tag":      "api",
		"listen":   nc.APIAddr,
		"services": []any{"HandlerService", "StatsService", "LoggerService", "RoutingService"},
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

// ClientJSON renders one client entry for the inbound's protocol. Hysteria2 users authenticate
// with a password: the user's UUID is used as one, so a user has a single secret everywhere.
func ClientJSON(c Client, protocol, flow string) map[string]any {
	if protocol == "hysteria" {
		return map[string]any{"auth": c.ID, "email": c.Email, "level": 0}
	}
	m := map[string]any{"id": c.ID, "email": c.Email, "level": 0}
	if c.Flow != "" {
		flow = c.Flow
	}
	if flow != "" {
		m["flow"] = flow
	}
	return m
}

func clientsJSON(cs []Client, protocol, flow string) []any {
	sorted := append([]Client(nil), cs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Email < sorted[j].Email })
	out := make([]any, 0, len(sorted))
	for _, c := range sorted {
		out = append(out, ClientJSON(c, protocol, flow))
	}
	return out
}

// listenKey identifies the socket an inbound binds: Hysteria listens on UDP, so it can share a
// port number with a TCP inbound (Reality on TCP 443 and Hysteria2 on UDP 443).
func listenKey(in map[string]any) string {
	listen, _ := in["listen"].(string)
	if listen == "" {
		listen = "0.0.0.0"
	}
	port, _ := toInt(in["port"])
	return Transport(in) + " " + net.JoinHostPort(listen, strconv.Itoa(port))
}

// Transport is "udp" for inbounds that listen on UDP (Hysteria, KCP) and "tcp" otherwise.
func Transport(in map[string]any) string {
	ss, _ := in["streamSettings"].(map[string]any)
	switch ss["network"] {
	case "hysteria", "kcp", "mkcp":
		return "udp"
	}
	return "tcp"
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

// addWarp appends the VYNEL_WARP WireGuard outbound (always with noKernelTun=true) and inserts
// WARP routing rules right before the first rule targeting DIRECT.
func addWarp(cfg map[string]any, w *WarpConfig) error {
	if w == nil || w.SecretKey == "" {
		return nil
	}
	if len(w.Addresses) == 0 {
		return fmt.Errorf("warp: at least one interface address is required")
	}
	if w.Endpoint == "" {
		return fmt.Errorf("warp: endpoint is required")
	}
	if !w.RouteAll && len(w.Domains) == 0 && len(w.IPs) == 0 {
		return nil
	}

	outbounds, _ := cfg["outbounds"].([]any)
	for _, o := range outbounds {
		if m, ok := o.(map[string]any); ok && m["tag"] == WarpTag {
			return fmt.Errorf("base config already defines outbound %q", WarpTag)
		}
	}

	peerPub := w.PeerPublicKey
	if peerPub == "" {
		peerPub = "bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo="
	}
	peer := map[string]any{
		"publicKey":  peerPub,
		"endpoint":   w.Endpoint,
		"allowedIPs": []any{"0.0.0.0/0", "::/0"},
	}
	if w.Keepalive > 0 {
		peer["keepAlive"] = w.Keepalive
	}
	if w.PresharedKey != "" {
		peer["preSharedKey"] = w.PresharedKey
	}

	addrs := make([]any, 0, len(w.Addresses))
	for _, a := range w.Addresses {
		addrs = append(addrs, a)
	}
	mtu := w.MTU
	if mtu <= 0 {
		mtu = 1280
	}
	ds := w.DomainStrategy
	if ds == "" {
		ds = "ForceIPv4v6"
	}
	settings := map[string]any{
		"noKernelTun":    true,
		"secretKey":      w.SecretKey,
		"address":        addrs,
		"peers":          []any{peer},
		"mtu":            mtu,
		"domainStrategy": ds,
	}
	if len(w.Reserved) == 3 {
		settings["reserved"] = []any{w.Reserved[0], w.Reserved[1], w.Reserved[2]}
	}
	outbound := map[string]any{
		"tag":      WarpTag,
		"protocol": "wireguard",
		"settings": settings,
	}
	if w.SendThrough != "" {
		if net.ParseIP(w.SendThrough) == nil {
			return fmt.Errorf("warp: bad sendThrough IP %q", w.SendThrough)
		}
		outbound["sendThrough"] = w.SendThrough
	}
	cfg["outbounds"] = append(outbounds, outbound)

	var rules []any
	if w.RouteAll {
		rules = append(rules, map[string]any{
			"ruleTag":     "WARP_ALL",
			"network":     "tcp,udp",
			"outboundTag": WarpTag,
		})
	} else {
		if len(w.Domains) > 0 {
			doms := make([]any, 0, len(w.Domains))
			for _, d := range w.Domains {
				doms = append(doms, d)
			}
			rules = append(rules, map[string]any{
				"ruleTag":     "WARP_DOM",
				"domain":      doms,
				"outboundTag": WarpTag,
			})
		}
		if len(w.IPs) > 0 {
			ips := make([]any, 0, len(w.IPs))
			for _, ip := range w.IPs {
				ips = append(ips, ip)
			}
			rules = append(rules, map[string]any{
				"ruleTag":     "WARP_IP",
				"ip":          ips,
				"outboundTag": WarpTag,
			})
		}
	}

	routing, _ := cfg["routing"].(map[string]any)
	if routing == nil {
		routing = map[string]any{}
		cfg["routing"] = routing
	}
	if len(w.IPs) > 0 {
		if cur, _ := routing["domainStrategy"].(string); cur == "" {
			routing["domainStrategy"] = "IPIfNonMatch"
		}
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
