package service

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// SettingSubRouting holds how client apps route traffic (JSON ClientRouting; empty = the
// defaults). It goes into the formats that carry routing: Xray JSON, Mihomo and sing-box.
const SettingSubRouting = "sub.routing"

// ClientRouting is the routing a subscription gives client apps (docs/USER_GUIDE.md §5.1).
type ClientRouting struct {
	// RUDirect sends Russian sites (geosite:category-ru, .ru/.su/.рф) and addresses (geoip:ru)
	// past the tunnel.
	RUDirect bool `json:"ruDirect"`
	// DNSDirect resolve the names that go direct: "localhost" is the device's own resolver.
	DNSDirect []string `json:"dnsDirect"`
	// DNSProxy resolve everything else, through the tunnel: IPs, or DoH links (https://…).
	DNSProxy []string `json:"dnsProxy"`
	// Torrent is where BitTorrent goes: proxy, direct or block.
	Torrent  string `json:"torrent"`
	BlockAds bool   `json:"blockAds"` // geosite:category-ads-all
	// Own lists, in Xray syntax: domain:, full:, keyword:, regexp:, geosite:, geoip:, an IP or a
	// CIDR; a bare name means the domain and its subdomains. process: is an app by its file
	// name (FortniteClient-Win64-Shipping.exe) or Android package (com.example.app).
	Direct []string `json:"direct"`
	Proxy  []string `json:"proxy"` // through the tunnel even when RU or the direct list would match
	Block  []string `json:"block"`
}

// DefaultClientRouting is what subscriptions carry until changed: Russian sites direct with
// Yandex DNS, the rest through the tunnel with DoH.
func DefaultClientRouting() ClientRouting {
	return ClientRouting{
		RUDirect:  true,
		DNSDirect: []string{"localhost", "77.88.8.8"},
		DNSProxy:  []string{"https://1.1.1.1/dns-query", "https://8.8.8.8/dns-query"},
		Torrent:   "direct",
		Direct:    []string{}, Proxy: []string{}, Block: []string{},
	}
}

// ClientRouting returns the subscription routing (the defaults until changed).
func (s *Service) ClientRouting(ctx context.Context) (ClientRouting, error) {
	raw, err := s.Setting(ctx, SettingSubRouting, "")
	if err != nil || strings.TrimSpace(raw) == "" {
		return DefaultClientRouting(), err
	}
	r := DefaultClientRouting()
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return DefaultClientRouting(), nil
	}
	return r, nil
}

// SetClientRouting validates and stores the routing; nil restores the defaults.
func (s *Service) SetClientRouting(ctx context.Context, actor Actor, r *ClientRouting) error {
	if r == nil {
		return s.SetSetting(ctx, actor, SettingSubRouting, "")
	}
	if err := CheckClientRouting(r); err != nil {
		return err
	}
	b, _ := json.Marshal(r)
	return s.SetSetting(ctx, actor, SettingSubRouting, string(b))
}

var ruleWordRe = regexp.MustCompile(`^[a-z0-9!@._-]+$`)

// processRe is an app's file or package name: no paths, nothing that breaks a Mihomo rule.
var processRe = regexp.MustCompile(`^[\p{L}\p{N}_.+()-][\p{L}\p{N}_.+() -]{0,127}$`)

// CheckClientRouting validates and tidies a routing (empty lines dropped).
func CheckClientRouting(r *ClientRouting) error {
	switch r.Torrent {
	case "":
		r.Torrent = "proxy"
	case "proxy", "direct", "block":
	default:
		return invalid("torrents: proxy, direct or block")
	}
	var err error
	if r.DNSDirect, err = cleanDNS(r.DNSDirect, true); err != nil {
		return err
	}
	if r.DNSProxy, err = cleanDNS(r.DNSProxy, false); err != nil {
		return err
	}
	for _, l := range []*[]string{&r.Direct, &r.Proxy, &r.Block} {
		out := []string{}
		for _, e := range *l {
			e = strings.TrimSpace(e)
			if e == "" {
				continue
			}
			if err := checkRuleEntry(e); err != nil {
				return err
			}
			out = append(out, e)
		}
		*l = out
	}
	return nil
}

func cleanDNS(in []string, direct bool) ([]string, error) {
	out := []string{}
	for _, d := range in {
		d = strings.TrimSpace(d)
		switch {
		case d == "":
			continue
		case d == "localhost":
			if !direct {
				return nil, invalid("DNS through the tunnel cannot be the device's own resolver (localhost)")
			}
		case net.ParseIP(d) != nil:
		case strings.HasPrefix(d, "https://"):
			u, err := url.Parse(d)
			if err != nil || u.Host == "" {
				return nil, invalid("DNS %q is not a valid DoH link", d)
			}
		default:
			return nil, invalid("DNS %q: an IP address, a DoH link (https://…/dns-query) or localhost", d)
		}
		out = append(out, d)
	}
	return out, nil
}

// RuleEntry is one parsed list entry.
type RuleEntry struct {
	Kind  string // domain, full, keyword, regexp, geosite, geoip, ip, process
	Value string
}

// ParseRuleEntry reads one list entry; a bare name is a domain with its subdomains.
func ParseRuleEntry(e string) RuleEntry {
	if _, _, err := net.ParseCIDR(e); err == nil {
		return RuleEntry{"ip", e}
	}
	if net.ParseIP(e) != nil {
		return RuleEntry{"ip", e}
	}
	if k, v, ok := strings.Cut(e, ":"); ok {
		switch k {
		case "domain", "full", "keyword", "regexp", "geosite", "geoip", "process":
			return RuleEntry{k, v}
		}
	}
	return RuleEntry{"domain", strings.TrimPrefix(e, ".")}
}

func checkRuleEntry(e string) error {
	r := ParseRuleEntry(e)
	switch r.Kind {
	case "ip":
		return nil
	case "regexp":
		if _, err := regexp.Compile(r.Value); err != nil {
			return invalid("%q: not a valid regexp: %v", e, err)
		}
		return nil
	case "process":
		if !processRe.MatchString(strings.TrimSpace(r.Value)) {
			return invalid("%q: an app's file name (game.exe) or Android package (com.example.app), no path", e)
		}
		return nil
	}
	if r.Value == "" || !ruleWordRe.MatchString(strings.ToLower(r.Value)) {
		return invalid("%q: use domain:, full:, keyword:, regexp:, geosite:, geoip:, process:, an IP or a CIDR", e)
	}
	return nil
}

// ProfileClient is a profile's own client config (the "custom client profile" switch):
// either its own routing or a whole Xray client config. It applies to the Xray JSON format,
// which has one config per server; Mihomo and sing-box get one file for all servers and use
// the subscription routing.
type ProfileClient struct {
	Custom  bool           `json:"custom"`
	Mode    string         `json:"mode"` // routing | json
	Routing *ClientRouting `json:"routing,omitempty"`
	// Config is an Xray client config. Its outbound tagged "proxy" is replaced with the server's
	// (added first when there is none); "remarks" becomes the server's name.
	Config string `json:"config,omitempty"`
}

// CheckProfileClient validates a profile's client config.
func CheckProfileClient(c *ProfileClient) error {
	if !c.Custom {
		return nil
	}
	switch c.Mode {
	case "", "routing":
		c.Mode = "routing"
		if c.Routing == nil {
			r := DefaultClientRouting()
			c.Routing = &r
		}
		return CheckClientRouting(c.Routing)
	case "json":
		if _, err := ParseClientConfig(c.Config); err != nil {
			return err
		}
		return nil
	}
	return invalid("client config mode: routing or json")
}

// ParseClientConfig reads a custom Xray client config: an object, or an array of one.
func ParseClientConfig(src string) (map[string]any, error) {
	var v any
	if err := json.Unmarshal([]byte(src), &v); err != nil {
		return nil, invalid("client config is not valid JSON: %v", err)
	}
	if l, ok := v.([]any); ok {
		if len(l) != 1 {
			return nil, invalid("client config: one config object, not a list of %d", len(l))
		}
		v = l[0]
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, invalid("client config must be a JSON object")
	}
	if o, ok := m["outbounds"]; ok {
		if _, ok := o.([]any); !ok {
			return nil, invalid("client config: outbounds must be a list")
		}
	}
	if _, ok := m["routing"].(map[string]any); !ok && m["routing"] != nil {
		return nil, invalid("client config: routing must be an object")
	}
	return m, nil
}
