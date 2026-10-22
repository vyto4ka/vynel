package subscription

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

// A client's own template (service.SubRule.Template) is a whole config the servers are put into,
// read the way Remnawave reads its templates, so one made there works here as it is:
//   - Mihomo: the servers join "proxies"; every proxy group gets their names after its own
//     entries, unless the group says otherwise in a "remnawave" key (include-proxies: false,
//     select-random-proxy: true, shuffle-proxies-order: true); a proxy provider with
//     remnawave: {include-proxies: true} gets them as its payload.
//   - sing-box: the servers join "outbounds"; selector and urltest outbounds list them, unless
//     they say remnawave: {includeProxies: false}.
//   - Xray JSON: one config per server, the server's outbound replacing the one tagged "proxy"
//     (or coming first).
// The "remnawave" keys are removed from what apps get.

// MihomoFromTemplate puts the servers into a Mihomo template.
func MihomoFromTemplate(hosts []service.Host, uuid, tpl string) ([]byte, error) {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(tpl), &doc); err != nil {
		return nil, err
	}
	proxies, names := mihomoProxies(hosts, uuid)
	delete(doc, "remnawave")
	own, _ := doc["proxies"].([]any)
	all := append([]any(nil), own...)
	for _, p := range proxies {
		all = append(all, p)
	}
	doc["proxies"] = all
	if groups, ok := doc["proxy-groups"].([]any); ok {
		for i, g := range groups {
			m, ok := g.(map[string]any)
			if !ok {
				continue
			}
			custom, _ := m["remnawave"].(map[string]any)
			delete(m, "remnawave")
			list, _ := m["proxies"].([]any)
			for _, n := range groupNames(custom, names) {
				list = append(list, n)
			}
			if len(list) > 0 {
				m["proxies"] = list
			}
			groups[i] = m
		}
	}
	if providers, ok := doc["proxy-providers"].(map[string]any); ok {
		for k, p := range providers {
			m, ok := p.(map[string]any)
			if !ok {
				continue
			}
			custom, _ := m["remnawave"].(map[string]any)
			delete(m, "remnawave")
			if custom["include-proxies"] == true {
				m["payload"] = proxies
			}
			providers[k] = m
		}
	}
	return yaml.Marshal(doc)
}

// groupNames is what a Mihomo proxy group gets of the servers (see the "remnawave" key).
func groupNames(custom map[string]any, names []string) []string {
	switch {
	case custom["include-proxies"] == false:
		return nil
	case custom["select-random-proxy"] == true && len(names) > 0:
		return []string{names[rand.IntN(len(names))]}
	case custom["shuffle-proxies-order"] == true:
		out := append([]string(nil), names...)
		rand.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
		return out
	}
	return names
}

// singBoxProxyTypes are the outbound types selector and urltest outbounds pick from.
var singBoxProxyTypes = map[string]bool{"vless": true, "hysteria2": true, "trojan": true, "shadowsocks": true, "vmess": true, "tuic": true}

// SingBoxFromTemplate puts the servers into a sing-box template.
func SingBoxFromTemplate(hosts []service.Host, uuid, tpl string) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(tpl), &doc); err != nil {
		return nil, err
	}
	delete(doc, "remnawave")
	servers, _ := singBoxOutbounds(hosts, uuid)
	own, _ := doc["outbounds"].([]any)
	all := append([]any(nil), own...)
	for _, o := range servers {
		all = append(all, o)
	}
	var proxyTags, selectTags []any
	for _, o := range all {
		m, _ := o.(map[string]any)
		t, _ := m["type"].(string)
		if singBoxProxyTypes[t] {
			proxyTags = append(proxyTags, m["tag"])
		}
		if singBoxProxyTypes[t] || t == "urltest" {
			selectTags = append(selectTags, m["tag"])
		}
	}
	for i, o := range all {
		m, ok := o.(map[string]any)
		if !ok {
			continue
		}
		custom, _ := m["remnawave"].(map[string]any)
		delete(m, "remnawave")
		if custom["includeProxies"] == false {
			continue
		}
		switch m["type"] {
		case "urltest":
			m["outbounds"] = proxyTags
		case "selector":
			m["outbounds"] = selectTags
		}
		all[i] = m
	}
	doc["outbounds"] = all
	return json.MarshalIndent(doc, "", "  ")
}

// XrayJSONFromTemplate makes one config per server from an Xray template.
func XrayJSONFromTemplate(hosts []service.Host, uuid, tpl string) ([]byte, error) {
	cfg, err := service.ParseClientConfig(tpl)
	if err != nil {
		return nil, err
	}
	delete(cfg, "remnawave")
	configs := make([]map[string]any, 0, len(hosts))
	for _, h := range hosts {
		// A fresh copy per server: withProxy changes the outbounds.
		var c map[string]any
		raw, _ := json.Marshal(cfg)
		_ = json.Unmarshal(raw, &c)
		configs = append(configs, withProxy(c, XrayOutbound(h, uuid), h.Remark))
	}
	return json.MarshalIndent(configs, "", "  ")
}

// TemplateInfo is what the panel found in a pasted template.
type TemplateInfo struct {
	Summary []string               `json:"summary"`
	Routing *service.ClientRouting `json:"routing"` // the rules that map to the routing fields (nil = none)
	Skipped []string               `json:"skipped"` // rules that do not
}

// ReadTemplate describes a template and takes out the routing the panel's fields can hold.
func ReadTemplate(format, text string) (*TemplateInfo, error) {
	if err := service.CheckClientTemplate(format, text); err != nil {
		return nil, err
	}
	info := &TemplateInfo{Summary: []string{}, Skipped: []string{}}
	r := service.ClientRouting{DNSDirect: []string{}, DNSProxy: []string{}, Direct: []string{}, Proxy: []string{}, Block: []string{}, Torrent: "proxy"}
	found := 0
	put := func(target, entry string) {
		found++
		switch target {
		case "direct":
			r.Direct = appendNew(r.Direct, entry)
		case "block":
			r.Block = appendNew(r.Block, entry)
		default:
			r.Proxy = appendNew(r.Proxy, entry)
		}
	}
	switch format {
	case "mihomo":
		var doc map[string]any
		_ = yaml.Unmarshal([]byte(text), &doc)
		proxies, _ := doc["proxies"].([]any)
		groups, _ := doc["proxy-groups"].([]any)
		rules, _ := doc["rules"].([]any)
		info.Summary = append(info.Summary, fmt.Sprintf("своих серверов: %d — останутся, серверы подписки добавятся после них", len(proxies)),
			fmt.Sprintf("групп: %d — в каждую попадут серверы подписки (кроме тех, где remnawave: include-proxies: false)", len(groups)),
			fmt.Sprintf("правил: %d", len(rules)))
		for _, x := range rules {
			line, _ := x.(string)
			if !mihomoRuleToEntry(line, &r, put) {
				info.Skipped = append(info.Skipped, line)
			}
		}
		if dns, ok := doc["dns"].(map[string]any); ok {
			for _, d := range anyStrings(dns["nameserver"]) {
				r.DNSProxy = appendDNS(r.DNSProxy, strings.SplitN(d, "#", 2)[0])
			}
			for _, d := range append(anyStrings(dns["proxy-server-nameserver"]), anyStrings(dns["default-nameserver"])...) {
				if d == "system" {
					d = "localhost"
				}
				r.DNSDirect = appendDNS(r.DNSDirect, d)
			}
		}
	case "singbox":
		var doc map[string]any
		_ = json.Unmarshal([]byte(text), &doc)
		outbounds, _ := doc["outbounds"].([]any)
		route, _ := doc["route"].(map[string]any)
		rules, _ := route["rules"].([]any)
		info.Summary = append(info.Summary, fmt.Sprintf("outbounds: %d — серверы подписки добавятся, selector и urltest их получат", len(outbounds)),
			fmt.Sprintf("правил маршрутизации: %d", len(rules)))
		for _, x := range rules {
			m, _ := x.(map[string]any)
			if !singBoxRuleToEntries(m, &r, put) {
				raw, _ := json.Marshal(m)
				info.Skipped = append(info.Skipped, string(raw))
			}
		}
	case "xray":
		cfg, _ := service.ParseClientConfig(text)
		outbounds, _ := cfg["outbounds"].([]any)
		routing, _ := cfg["routing"].(map[string]any)
		rules, _ := routing["rules"].([]any)
		info.Summary = append(info.Summary, fmt.Sprintf("outbounds: %d — сервер встанет вместо outbound с тегом proxy (или первым)", len(outbounds)),
			fmt.Sprintf("правил маршрутизации: %d", len(rules)))
		for _, x := range rules {
			m, _ := x.(map[string]any)
			if !xrayRuleToEntries(m, put) {
				raw, _ := json.Marshal(m)
				info.Skipped = append(info.Skipped, string(raw))
			}
		}
	}
	if found > 0 || len(r.DNSProxy) > 0 || len(r.DNSDirect) > 0 {
		if err := service.CheckClientRouting(&r); err == nil {
			info.Routing = &r
		}
	}
	return info, nil
}

// mihomoRuleToEntry maps one Mihomo rule to the routing fields; false = it has no field.
func mihomoRuleToEntry(line string, r *service.ClientRouting, put func(target, entry string)) bool {
	parts := strings.Split(line, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	if len(parts) < 2 {
		return false
	}
	kind := strings.ToUpper(parts[0])
	if kind == "MATCH" {
		return true // the last rule: the rest through the tunnel, as the panel does
	}
	if len(parts) < 3 {
		return false
	}
	value, target := parts[1], strings.ToUpper(parts[2])
	to := "proxy"
	switch target {
	case "DIRECT":
		to = "direct"
	case "REJECT", "REJECT-DROP":
		to = "block"
	}
	var entry string
	switch kind {
	case "DOMAIN-SUFFIX":
		entry = value
	case "DOMAIN":
		entry = "full:" + value
	case "DOMAIN-KEYWORD":
		entry = "keyword:" + value
	case "DOMAIN-REGEX":
		entry = "regexp:" + value
	case "GEOSITE":
		entry = "geosite:" + value
	case "GEOIP":
		entry = "geoip:" + value
	case "IP-CIDR", "IP-CIDR6":
		entry = value
	case "PROCESS-NAME":
		entry = "process:" + value
	default:
		return false
	}
	switch {
	case kind == "GEOIP" && strings.EqualFold(value, "private"):
		return true // the panel always sends local addresses direct
	case to == "direct" && (entry == "geosite:category-ru" || entry == "geoip:ru" || entry == "ru" || entry == "su" || entry == "xn--p1ai"):
		r.RUDirect = true
		return true
	case to == "block" && entry == "geosite:category-ads-all":
		r.BlockAds = true
		return true
	}
	if service.CheckClientRouting(&service.ClientRouting{Direct: []string{entry}}) != nil {
		return false
	}
	put(to, entry)
	return true
}

// singBoxRuleToEntries maps a sing-box route rule to the routing fields.
func singBoxRuleToEntries(m map[string]any, r *service.ClientRouting, put func(target, entry string)) bool {
	to := "proxy"
	switch {
	case m["action"] == "reject":
		to = "block"
	case m["outbound"] == "direct":
		to = "direct"
	case m["outbound"] == nil:
		return m["action"] == "sniff" || m["action"] == "hijack-dns" || m["ip_is_private"] == true
	}
	prefixes := map[string]string{"domain_suffix": "", "domain": "full:", "domain_keyword": "keyword:", "domain_regex": "regexp:",
		"ip_cidr": "", "process_name": "process:"}
	matched := false
	for k, v := range m {
		switch k {
		case "outbound", "action", "invert":
			continue
		case "rule_set":
			for _, tag := range anyStrings(v) {
				kind, name, ok := strings.Cut(tag, "-")
				if !ok || (kind != "geosite" && kind != "geoip") {
					return false
				}
				matched = true
				if to == "direct" && (tag == "geosite-category-ru" || tag == "geoip-ru") {
					r.RUDirect = true
					continue
				}
				put(to, kind+":"+name)
			}
			continue
		}
		p, ok := prefixes[k]
		if !ok {
			return false
		}
		for _, x := range anyStrings(v) {
			matched = true
			put(to, p+x)
		}
	}
	return matched
}

// xrayRuleToEntries maps an Xray routing rule to the routing fields.
func xrayRuleToEntries(m map[string]any, put func(target, entry string)) bool {
	tag, _ := m["outboundTag"].(string)
	to := "proxy"
	switch tag {
	case "direct", "freedom":
		to = "direct"
	case "block", "blackhole":
		to = "block"
	case "dns-out":
		return true
	}
	matched := false
	for k, v := range m {
		switch k {
		case "type", "outboundTag", "ruleTag":
			continue
		case "domain", "ip":
			for _, x := range anyStrings(v) {
				if x == "geoip:private" {
					continue
				}
				matched = true
				put(to, x)
			}
		case "process":
			for _, x := range anyStrings(v) {
				matched = true
				put(to, "process:"+x)
			}
		default:
			return false
		}
	}
	return matched
}

func anyStrings(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, x := range t {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func appendNew(l []string, v string) []string {
	for _, x := range l {
		if x == v {
			return l
		}
	}
	return append(l, v)
}

// appendDNS keeps the DNS servers the routing fields understand: an IP, a DoH link, localhost.
func appendDNS(l []string, d string) []string {
	d = strings.TrimSpace(d)
	if d == "localhost" || net.ParseIP(d) != nil || strings.HasPrefix(d, "https://") {
		return appendNew(l, d)
	}
	return l
}
