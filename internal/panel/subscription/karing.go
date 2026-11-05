package subscription

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

// Karing imports servers and remote rule-set/outbound pairs rather than running the supplied
// config. In particular, inbounds, DNS, inline rule sets and action:reject are not portable
// subscription settings. Its importer recognizes the legacy block outbound as the Block target.
// https://github.com/KaringX/karing/blob/e3c19f1eb1225dbd48a4862f0983b82d0af8de69/lib/app/utils/singbox_json_utils.dart
// https://karing.app/en/app-manual/diversion-rule/
func Karing(hosts []service.Host, uuid, subURL string, r service.ClientRouting) ([]byte, error) {
	var proxies []map[string]any
	var named []service.Host
	for _, h := range hosts {
		var p map[string]any
		if h.Raw != "" {
			var ok bool
			p, ok = singBoxLink(h.Raw)
			if !ok {
				continue
			}
			h.Remark, _ = p["tag"].(string)
		} else {
			out, _ := singBoxOutbounds([]service.Host{h}, uuid)
			p = out[0]
		}
		if h.Remark == "" {
			h.Remark = h.Address
		}
		named = append(named, h)
		proxies = append(proxies, p)
	}
	names := uniqueNames(named, "proxy", "direct", "block")
	for i := range proxies {
		proxies[i]["tag"] = names[i]
	}
	outbounds := []map[string]any{
		{"type": "selector", "tag": "proxy", "outbounds": names},
		{"type": "direct", "tag": "direct"},
		{"type": "block", "tag": "block"},
	}
	outbounds = append(outbounds, proxies...)
	route, _ := karingRouting(subURL, r)
	return json.MarshalIndent(map[string]any{"outbounds": outbounds, "route": route}, "", "  ")
}

// karingRouting exposes ordinary matchers as source rule sets and geo categories as remote
// binary rule sets. Every imported rule has exactly two keys: rule_set and outbound. Splitting
// them by target preserves priority: block, explicit proxy, private, RU, direct, final.
func karingRouting(subURL string, r service.ClientRouting) (map[string]any, map[string][]map[string]any) {
	s := &singRoute{ruleSets: map[string]map[string]any{}}
	sources := map[string][]map[string]any{}
	var rules []map[string]any
	add := func(name, target string, entries []string, extra ...map[string]any) {
		var local []string
		var refs []string
		for _, e := range entries {
			entry := service.ParseRuleEntry(e)
			if entry.Kind == "geosite" || entry.Kind == "geoip" {
				refs = append(refs, s.set(entry.Kind, entry.Value))
			} else {
				local = append(local, e)
			}
		}
		matches := append(s.match(local), extra...)
		if len(matches) > 0 {
			sources[name] = matches
			tag := "vynel-" + name
			data, _ := json.Marshal(matches)
			hash := sha256.Sum256(data)
			s.ruleSets[tag] = map[string]any{"type": "remote", "tag": tag, "format": "source",
				"url": strings.TrimRight(subURL, "/") + "/karing-rules/" + name + "?v=" + hex.EncodeToString(hash[:8]), "download_detour": "direct"}
			s.order = append(s.order, tag)
			refs = append([]string{tag}, refs...)
		}
		for _, ref := range refs {
			rules = append(rules, map[string]any{"rule_set": ref, "outbound": target})
		}
	}
	block := r.Block
	if r.BlockAds {
		block = append([]string{"geosite:category-ads-all"}, block...)
	}
	add("block", "block", block)
	// Headless rule sets cannot match sniffed protocols. Do not turn a BitTorrent
	// policy into an overbroad rule for all traffic; Karing owns protocol detection.
	add("proxy", "proxy", r.Proxy)
	add("private", "direct", nil, map[string]any{"ip_cidr": []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
		"172.16.0.0/12", "192.168.0.0/16", "::1/128", "fc00::/7", "fe80::/10",
	}})
	if r.RUDirect {
		add("ru", "direct", append(append([]string{}, ruDomains...), "geoip:ru"))
	}
	add("direct", "direct", r.Direct)
	add("final", "proxy", nil, map[string]any{"network": []string{"tcp", "udp"}})
	sets := make([]map[string]any, 0, len(s.order))
	for _, tag := range s.order {
		sets = append(sets, s.ruleSets[tag])
	}
	return map[string]any{"rule_set": sets, "rules": rules, "final": "proxy"}, sources
}

// KaringRuleSet is a source-format sing-box rule set. It contains matchers only, never server
// credentials; Karing fetches it without the subscription's HWID header.
func KaringRuleSet(name string, r service.ClientRouting) ([]byte, bool) {
	_, sources := karingRouting("", r)
	rules, ok := sources[name]
	if !ok {
		return nil, false
	}
	b, _ := json.Marshal(map[string]any{"version": 3, "rules": rules})
	return b, true
}
