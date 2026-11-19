package subscription

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

// Wire formats:
// https://github.com/Lemonochka/keqdroid/blob/v0.34.1/lib/utils/provider_routing.dart
// https://github.com/Flyfrog-LLC/Happ-docs/blob/main/dev-docs/routing.md
// https://github.com/INCY-DEV/incy-docs/blob/main/ru/dev-docs/routing.md
// https://docs.v2raytun.com/overview/supported-headers

// LinkRouting reports whether routing must travel separately in HTTP headers.
func LinkRouting(format Format, ua string, hosts []service.Host) bool {
	if format == FormatBase64 {
		return true
	}
	if format == FormatXray && keqClient.MatchString(ua) && hasRawHosts(hosts) {
		return true
	}
	// Happ uses the Routing header for Tunnel DNS and Trimmed GEO-assets in JSON mode too.
	return format == FormatXray && clientRe(`(?i)\b(happ|incy)\b`).MatchString(ua)
}

// RoutingHeaders adds the panel's routing for link subscriptions. An applicable Routing row
// (including a disabled one) is an explicit override; a custom Autorouting takes precedence too.
// This also works with saved header lists from before automatic routing was introduced.
func RoutingHeaders(hs []service.SubHeader, rendered []HeaderValue, ua, title string, r service.ClientRouting) []HeaderValue {
	if !keqClient.MatchString(ua) && !clientRe(`(?i)\b(happ|incy)\b`).MatchString(ua) && !v2rayTunClient.MatchString(ua) {
		return rendered
	}
	for _, h := range hs {
		if h.Clients != "" {
			re := clientRe(h.Clients)
			if re == nil || !re.MatchString(ua) {
				continue
			}
		}
		if strings.EqualFold(h.Name, "Routing") || h.Enabled && strings.EqualFold(h.Name, "Autorouting") {
			return rendered
		}
	}
	value := providerRouting(ua, title, r)
	return append(rendered, HeaderValue{Name: "Routing", Value: value})
}

// FragmentHeaders adds Happ/INCY fragmentation HTTP headers for Base64 subscriptions when
// ClientHello fragmentation is enabled and not explicitly overridden in SubHeaders.
func FragmentHeaders(hs []service.SubHeader, rendered []HeaderValue, ua string, format Format, r service.ClientRouting) []HeaderValue {
	if !r.Fragment.Enabled || format != FormatBase64 || !clientRe(`(?i)\b(happ|incy)\b`).MatchString(ua) {
		return rendered
	}
	has := func(name string) bool {
		for _, h := range hs {
			if h.Clients != "" {
				re := clientRe(h.Clients)
				if re == nil || !re.MatchString(ua) {
					continue
				}
			}
			if strings.EqualFold(h.Name, name) {
				return true
			}
		}
		for _, h := range rendered {
			if strings.EqualFold(h.Name, name) {
				return true
			}
		}
		return false
	}
	packets := r.Fragment.Packets
	if packets == "" {
		packets = "tlshello"
	}
	length := r.Fragment.Length
	if length == "" {
		length = "10-20"
	}
	interval := r.Fragment.Delay
	if interval == "" {
		interval = "10-20"
	}
	for _, kv := range []HeaderValue{
		{Name: "fragmentation-enable", Value: "true"},
		{Name: "fragmentation-packets", Value: packets},
		{Name: "fragmentation-length", Value: length},
		{Name: "fragmentation-interval", Value: interval},
	} {
		if !has(kv.Name) {
			rendered = append(rendered, kv)
		}
	}
	return rendered
}

func providerRouting(ua, title string, r service.ClientRouting) string {
	if title == "" {
		title = "vynel"
	}
	if keqClient.MatchString(ua) {
		// Since 0.34.0 KeqDroid accepts base64 Xray rules in Routing. Unlike its Happ
		// importer, this retains rule order: explicit proxy rules precede RU/direct.
		// The app ignores unsupported process/protocol/DNS rules rather than widening them.
		b, _ := json.Marshal(map[string]any{"name": title, "rules": xrayRules(r)})
		return base64.StdEncoding.EncodeToString(b)
	}
	if v2rayTunClient.MatchString(ua) {
		return v2rayTunRouting(title, r)
	}
	scheme := "happ"
	if clientRe(`(?i)\bincy\b`).MatchString(ua) {
		scheme = "incy"
	}
	return buildHappRouting(scheme, title, r)
}

// v2rayTunRouting renders a Base64-encoded v2RayTun routing JSON object.
// Domain and IP matchers are kept in separate rules so Xray does not AND them together.
func v2rayTunRouting(title string, r service.ClientRouting) string {
	var rules []map[string]any
	add := func(key, name, tag string, domains, ips []string) {
		if len(domains) > 0 {
			rules = append(rules, map[string]any{
				"id":          deterministicUUID(title + ":" + key + ":domain"),
				"type":        "field",
				"outboundTag": tag,
				"__name__":    name,
				"domain":      domains,
			})
		}
		if len(ips) > 0 {
			ipName := name
			if len(domains) > 0 {
				ipName += " (IP)"
			}
			rules = append(rules, map[string]any{
				"id":          deterministicUUID(title + ":" + key + ":ip"),
				"type":        "field",
				"outboundTag": tag,
				"__name__":    ipName,
				"ip":          ips,
			})
		}
	}
	bd, bi := split(r.Block)
	if r.BlockAds {
		bd = append([]string{"geosite:category-ads-all"}, bd...)
	}
	add("block", "Block", "block", bd, bi)
	pd, pi := split(r.Proxy)
	add("proxy", "Proxy", "proxy", pd, pi)
	add("private", "Direct Private", "direct", nil, []string{"geoip:private"})
	if r.RUDirect {
		add("ru", "Direct Russia", "direct", ruDomains, []string{"geoip:ru"})
	}
	dd, di := split(r.Direct)
	add("direct", "Direct", "direct", dd, di)
	if rules == nil {
		rules = []map[string]any{}
	}
	doc := map[string]any{
		"id":             deterministicUUID(title + ":profile"),
		"name":           title,
		"domainStrategy": "IPIfNonMatch",
		"domainMatcher":  "hybrid",
		"balancers":      []any{},
		"rules":          rules,
	}
	b, _ := json.Marshal(doc)
	return base64.StdEncoding.EncodeToString(b)
}

func deterministicUUID(seed string) string {
	sum := sha256.Sum256([]byte("vynel-v2raytun:" + seed))
	sum[6] = (sum[6] & 0x0f) | 0x50 // version 5
	sum[8] = (sum[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%08X-%04X-%04X-%04X-%012X",
		sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}
