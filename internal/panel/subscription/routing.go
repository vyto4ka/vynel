package subscription

import (
	"net"
	"net/url"
	"strings"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

// ruDomains are the Russian sites RUDirect sends past the tunnel (Xray syntax); names come in
// punycode, so .рф is xn--p1ai.
var ruDomains = []string{"geosite:category-ru", "domain:ru", "domain:su", "domain:xn--p1ai"}

// split sorts list entries into domain and IP matchers (Xray syntax); apps are left out.
func split(entries []string) (domains, ips []string) {
	for _, e := range entries {
		r := service.ParseRuleEntry(e)
		switch r.Kind {
		case "process":
		case "ip", "geoip":
			ips = append(ips, xrayEntry(r))
		default:
			domains = append(domains, xrayEntry(r))
		}
	}
	return domains, ips
}

// processes are the apps of a list (process: entries).
func processes(entries []string) []string {
	var out []string
	for _, e := range entries {
		if r := service.ParseRuleEntry(e); r.Kind == "process" {
			out = append(out, r.Value)
		}
	}
	return out
}

func xrayEntry(r service.RuleEntry) string {
	if r.Kind == "ip" {
		return r.Value
	}
	return r.Kind + ":" + r.Value
}

func field(outbound string, kv ...any) map[string]any {
	m := map[string]any{"type": "field", "outboundTag": outbound}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

// directNames are the names resolved by the direct DNS: Russian sites and the direct list.
func directNames(r service.ClientRouting) []string {
	var names []string
	if r.RUDirect {
		names = append(names, ruDomains...)
	}
	d, _ := split(r.Direct)
	return append(names, d...)
}

// xrayDNS builds the "dns" section: direct names on the direct servers, the rest through the
// tunnel.
func xrayDNS(r service.ClientRouting) map[string]any {
	var servers []any
	names := directNames(r)
	for _, d := range r.DNSDirect {
		addr := d
		if strings.HasPrefix(d, "https://") {
			addr = "https+local://" + strings.TrimPrefix(d, "https://")
		}
		if len(names) == 0 || len(r.DNSProxy) == 0 {
			servers = append(servers, addr)
			continue
		}
		servers = append(servers, map[string]any{"address": addr, "domains": names, "skipFallback": true})
	}
	for _, d := range r.DNSProxy {
		servers = append(servers, d)
	}
	if len(servers) == 0 {
		return nil
	}
	return map[string]any{"servers": servers, "queryStrategy": "UseIPv4"}
}

// xrayRules builds the routing rules; the last one sends everything else to the proxy.
func xrayRules(r service.ClientRouting) []any {
	rules := []any{field("dns-out", "inboundTag", []string{"socks"}, "port", "53")}
	var dnsIPs []string
	for _, d := range r.DNSDirect {
		if net.ParseIP(d) != nil {
			dnsIPs = append(dnsIPs, d)
		}
	}
	if len(dnsIPs) > 0 {
		rules = append(rules, field("direct", "ip", dnsIPs, "port", "53"))
	}
	add := func(out string, domains, ips []string, apps ...string) {
		if len(domains) > 0 {
			rules = append(rules, field(out, "domain", domains))
		}
		if len(ips) > 0 {
			rules = append(rules, field(out, "ip", ips))
		}
		if len(apps) > 0 {
			// Xray 26.1.23 and newer; an older core refuses the whole config.
			rules = append(rules, field(out, "process", apps))
		}
	}
	bd, bi := split(r.Block)
	if r.BlockAds {
		bd = append([]string{"geosite:category-ads-all"}, bd...)
	}
	add("block", bd, bi, processes(r.Block)...)
	if r.Torrent == "direct" || r.Torrent == "block" {
		rules = append(rules, field(r.Torrent, "protocol", []string{"bittorrent"}))
	}
	pd, pi := split(r.Proxy)
	add("proxy", pd, pi, processes(r.Proxy)...)
	rules = append(rules, field("direct", "ip", []string{"geoip:private"}))
	if r.RUDirect {
		add("direct", ruDomains, []string{"geoip:ru"})
	}
	dd, di := split(r.Direct)
	add("direct", dd, di, processes(r.Direct)...)
	return append(rules, field("proxy", "network", "tcp,udp"))
}

// xrayClient is the full Xray client config of one server.
func xrayClient(h service.Host, uuid string, r service.ClientRouting) map[string]any {
	proxy := XrayOutbound(h, uuid)
	if c := h.Client; c != nil {
		if c.Mode == "json" {
			if cfg, err := service.ParseClientConfig(c.Config); err == nil {
				return withProxy(cfg, proxy, h.Remark)
			}
		} else if c.Routing != nil {
			r = *c.Routing
		}
	}
	cfg := map[string]any{
		"remarks": h.Remark,
		"log":     map[string]any{"loglevel": "warning"},
		"inbounds": []any{
			map[string]any{"tag": "socks", "listen": "127.0.0.1", "port": 10808, "protocol": "socks",
				"settings": map[string]any{"udp": true, "auth": "noauth"},
				"sniffing": map[string]any{"enabled": true, "routeOnly": true, "destOverride": []string{"http", "tls", "quic"}}},
			map[string]any{"tag": "http", "listen": "127.0.0.1", "port": 10809, "protocol": "http",
				"sniffing": map[string]any{"enabled": true, "routeOnly": true, "destOverride": []string{"http", "tls"}}},
		},
		"outbounds": []any{
			proxy,
			map[string]any{"tag": "direct", "protocol": "freedom"},
			map[string]any{"tag": "block", "protocol": "blackhole", "settings": map[string]any{"response": map[string]any{"type": "http"}}},
			map[string]any{"tag": "dns-out", "protocol": "dns"},
		},
		"routing": map[string]any{"domainStrategy": "IPIfNonMatch", "rules": xrayRules(r)},
	}
	if dns := xrayDNS(r); dns != nil {
		cfg["dns"] = dns
	}
	return cfg
}

// withProxy puts the server into a custom client config: it replaces the outbound tagged
// "proxy" (or comes first when there is none) and names the config after the server.
func withProxy(cfg, proxy map[string]any, remark string) map[string]any {
	out := make(map[string]any, len(cfg)+1)
	for k, v := range cfg {
		out[k] = v
	}
	out["remarks"] = remark
	obs, _ := cfg["outbounds"].([]any)
	list := make([]any, 0, len(obs)+1)
	replaced := false
	for _, o := range obs {
		if m, ok := o.(map[string]any); ok && m["tag"] == "proxy" {
			list = append(list, proxy)
			replaced = true
			continue
		}
		list = append(list, o)
	}
	if !replaced {
		list = append([]any{proxy}, list...)
	}
	out["outbounds"] = list
	return out
}

// mihomoEntry renders a list entry as a Mihomo rule payload ("" = no twin).
func mihomoEntry(e string) string {
	r := service.ParseRuleEntry(e)
	switch r.Kind {
	case "domain":
		return "DOMAIN-SUFFIX," + r.Value
	case "full":
		return "DOMAIN," + r.Value
	case "keyword":
		return "DOMAIN-KEYWORD," + r.Value
	case "regexp":
		if strings.Contains(r.Value, ",") {
			return ""
		}
		return "DOMAIN-REGEX," + r.Value
	case "geosite":
		return "GEOSITE," + r.Value
	case "geoip":
		return "GEOIP," + r.Value
	case "process":
		return "PROCESS-NAME," + r.Value
	}
	cidr := r.Value
	if !strings.Contains(cidr, "/") {
		if strings.Contains(cidr, ":") {
			cidr += "/128"
		} else {
			cidr += "/32"
		}
	}
	if strings.Contains(cidr, ":") {
		return "IP-CIDR6," + cidr + ",no-resolve"
	}
	return "IP-CIDR," + cidr + ",no-resolve"
}

// mihomoTarget appends the target to a rule payload; IP rules keep "no-resolve" last.
func mihomoTarget(payload, target string) string {
	if p, ok := strings.CutSuffix(payload, ",no-resolve"); ok {
		return p + "," + target + ",no-resolve"
	}
	return payload + "," + target
}

// mihomoRules are the rules of a Mihomo profile whose proxy group is called group.
func mihomoRules(r service.ClientRouting, group string) []string {
	var rules []string
	add := func(target string, entries ...string) {
		for _, e := range entries {
			if p := mihomoEntry(e); p != "" {
				rules = append(rules, mihomoTarget(p, target))
			}
		}
	}
	if r.BlockAds {
		add("REJECT", "geosite:category-ads-all")
	}
	add("REJECT", r.Block...)
	add(group, r.Proxy...)
	rules = append(rules, "GEOIP,private,DIRECT,no-resolve")
	if r.RUDirect {
		add("DIRECT", ruDomains...)
		add("DIRECT", "geoip:ru")
	}
	add("DIRECT", r.Direct...)
	return append(rules, "MATCH,"+group)
}

func mihomoServer(d string) string {
	if d == "localhost" {
		return "system"
	}
	return d
}

// mihomoDNS is the "dns" section: direct names on the direct servers, the rest through the
// proxy group (a "#group" suffix).
func mihomoDNS(r service.ClientRouting, group string) map[string]any {
	if len(r.DNSDirect) == 0 && len(r.DNSProxy) == 0 {
		return nil
	}
	var direct, proxy, boot []string
	for _, d := range r.DNSDirect {
		direct = append(direct, mihomoServer(d))
		if net.ParseIP(d) != nil {
			boot = append(boot, d)
		}
	}
	for _, d := range r.DNSProxy {
		proxy = append(proxy, d+"#"+group)
	}
	if len(boot) == 0 {
		boot = []string{"77.88.8.8"}
	}
	dns := map[string]any{"enable": true, "ipv6": false, "default-nameserver": boot}
	if len(proxy) == 0 {
		dns["nameserver"] = direct
		return dns
	}
	dns["nameserver"] = proxy
	if len(direct) > 0 {
		dns["proxy-server-nameserver"] = direct
		policy := map[string]any{}
		for _, e := range directNames(r) {
			switch x := service.ParseRuleEntry(e); x.Kind {
			case "domain":
				policy["+."+x.Value] = direct
			case "full":
				policy[x.Value] = direct
			case "geosite":
				policy["geosite:"+x.Value] = direct
			}
		}
		if len(policy) > 0 {
			dns["nameserver-policy"] = policy
		}
	}
	return dns
}

const (
	singGeosite = "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-%s.srs"
	singGeoip   = "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set/geoip-%s.srs"
)

// singRoute builds the sing-box route rules, rule sets and DNS (sing-box 1.12 format).
type singRoute struct {
	rules    []map[string]any
	ruleSets map[string]map[string]any
	order    []string
}

func (s *singRoute) set(kind, name string) string {
	tag := kind + "-" + name
	if _, ok := s.ruleSets[tag]; !ok {
		u := strings.Replace(singGeosite, "%s", name, 1)
		if kind == "geoip" {
			u = strings.Replace(singGeoip, "%s", name, 1)
		}
		s.ruleSets[tag] = map[string]any{"type": "remote", "tag": tag, "format": "binary", "url": u, "download_detour": "proxy"}
		s.order = append(s.order, tag)
	}
	return tag
}

// match turns list entries into sing-box rule fields (one rule per kind keeps them OR-ed).
func (s *singRoute) match(entries []string) []map[string]any {
	group := map[string][]string{}
	var keys []string
	put := func(k, v string) {
		if _, ok := group[k]; !ok {
			keys = append(keys, k)
		}
		group[k] = append(group[k], v)
	}
	for _, e := range entries {
		r := service.ParseRuleEntry(e)
		switch r.Kind {
		case "domain":
			put("domain_suffix", r.Value)
		case "full":
			put("domain", r.Value)
		case "keyword":
			put("domain_keyword", r.Value)
		case "regexp":
			put("domain_regex", r.Value)
		case "geosite", "geoip":
			put("rule_set", s.set(r.Kind, r.Value))
		case "process":
			put("process_name", r.Value)
		case "ip":
			v := r.Value
			if !strings.Contains(v, "/") {
				if strings.Contains(v, ":") {
					v += "/128"
				} else {
					v += "/32"
				}
			}
			put("ip_cidr", v)
		}
	}
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		out = append(out, map[string]any{k: group[k]})
	}
	return out
}

func (s *singRoute) add(entries []string, action map[string]any) {
	for _, m := range s.match(entries) {
		for k, v := range action {
			m[k] = v
		}
		s.rules = append(s.rules, m)
	}
}

// singDNSServer turns a DNS setting into a sing-box 1.12 server.
func singDNSServer(d, tag, detour string) map[string]any {
	var m map[string]any
	switch {
	case d == "localhost":
		return map[string]any{"type": "local", "tag": tag}
	case strings.HasPrefix(d, "https://"):
		u, _ := url.Parse(d)
		m = map[string]any{"type": "https", "tag": tag, "server": u.Hostname()}
		if u.Port() != "" {
			m["server_port"] = u.Port()
		}
		if u.Path != "" && u.Path != "/dns-query" {
			m["path"] = u.Path
		}
	default:
		m = map[string]any{"type": "udp", "tag": tag, "server": d}
	}
	if detour != "" {
		m["detour"] = detour
	}
	return m
}

// singBoxRouting returns the route and dns sections of a sing-box profile.
func singBoxRouting(r service.ClientRouting) (route, dns map[string]any) {
	s := &singRoute{ruleSets: map[string]map[string]any{}}
	s.rules = []map[string]any{{"action": "sniff"}, {"protocol": "dns", "action": "hijack-dns"}, {"ip_is_private": true, "outbound": "direct"}}
	block := r.Block
	if r.BlockAds {
		block = append([]string{"geosite:category-ads-all"}, block...)
	}
	s.add(block, map[string]any{"action": "reject"})
	switch r.Torrent {
	case "direct":
		s.rules = append(s.rules, map[string]any{"protocol": "bittorrent", "outbound": "direct"})
	case "block":
		s.rules = append(s.rules, map[string]any{"protocol": "bittorrent", "action": "reject"})
	}
	s.add(r.Proxy, map[string]any{"outbound": "proxy"})
	if r.RUDirect {
		s.add(append(append([]string{}, ruDomains...), "geoip:ru"), map[string]any{"outbound": "direct"})
	}
	s.add(r.Direct, map[string]any{"outbound": "direct"})

	route = map[string]any{"auto_detect_interface": true, "final": "proxy", "rules": s.rules}
	var servers []map[string]any
	var dnsRules []map[string]any
	directTag := ""
	if len(r.DNSDirect) > 0 {
		directTag = "dns-direct"
		servers = append(servers, singDNSServer(r.DNSDirect[0], directTag, ""))
		route["default_domain_resolver"] = directTag
	}
	if len(r.DNSProxy) > 0 {
		servers = append(servers, singDNSServer(r.DNSProxy[0], "dns-proxy", "proxy"))
		if directTag != "" {
			for _, m := range s.match(directNames(r)) {
				m["server"] = directTag
				dnsRules = append(dnsRules, m)
			}
		}
	}
	if len(s.order) > 0 {
		sets := make([]map[string]any, 0, len(s.order))
		for _, t := range s.order {
			sets = append(sets, s.ruleSets[t])
		}
		route["rule_set"] = sets
	}
	if len(servers) == 0 {
		return route, nil
	}
	dns = map[string]any{"servers": servers, "strategy": "ipv4_only", "final": servers[len(servers)-1]["tag"]}
	if len(dnsRules) > 0 {
		dns["rules"] = dnsRules
	}
	return route, dns
}
