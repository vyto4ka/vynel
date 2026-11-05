package subscription

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"net/url"
	"strings"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

// Wire formats:
// https://github.com/Lemonochka/keqdroid/blob/v0.34.1/lib/utils/provider_routing.dart
// https://github.com/Flyfrog-LLC/Happ-docs/blob/main/dev-docs/routing.md
// https://github.com/INCY-DEV/incy-docs/blob/main/ru/dev-docs/routing.md

// LinkRouting reports whether routing must travel separately from server links.
func LinkRouting(format Format, ua string, hosts []service.Host) bool {
	return format == FormatBase64 || format == FormatXray && keqClient.MatchString(ua) && hasRawHosts(hosts)
}

// RoutingHeaders adds the panel's routing for link subscriptions. An applicable Routing row
// (including a disabled one) is an explicit override; a custom Autorouting takes precedence too.
// This also works with saved header lists from before automatic routing was introduced.
func RoutingHeaders(hs []service.SubHeader, rendered []HeaderValue, ua, title string, r service.ClientRouting) []HeaderValue {
	if !keqClient.MatchString(ua) && !clientRe(`(?i)\b(happ|incy)\b`).MatchString(ua) {
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
	dd, di := split(r.Direct)
	pd, pi := split(r.Proxy)
	bd, bi := split(r.Block)
	if r.RUDirect {
		dd = append(append([]string{}, ruDomains...), dd...)
		di = append([]string{"geoip:ru"}, di...)
	}
	di = append([]string{"geoip:private"}, di...)
	if r.BlockAds {
		bd = append([]string{"geosite:category-ads-all"}, bd...)
	}
	list := func(v []string) []string {
		if v == nil {
			return []string{}
		}
		return v
	}
	p := map[string]any{
		"Name": title, "GlobalProxy": "true", "DomainStrategy": "IPIfNonMatch", "FakeDNS": "false",
		"RouteOrder":  "block-proxy-direct",
		"DirectSites": list(dd), "DirectIp": list(di), "ProxySites": list(pd), "ProxyIp": list(pi),
		"BlockSites": list(bd), "BlockIp": list(bi),
	}
	profileDNS(p, "Domestic", r.DNSDirect)
	profileDNS(p, "Remote", r.DNSProxy)
	scheme := "happ"
	if clientRe(`(?i)\bincy\b`).MatchString(ua) {
		scheme = "incy"
	}
	b, _ := json.Marshal(p)
	return scheme + "://routing/onadd/" + base64.StdEncoding.EncodeToString(b)
}

// Happ/Incy have one resolver per direction, without a system-resolver value. Pick the first
// representable resolver, skipping localhost. KeqDroid keeps DNS in its own app settings.
func profileDNS(p map[string]any, prefix string, servers []string) {
	for _, server := range servers {
		if net.ParseIP(server) != nil {
			p[prefix+"DNSType"], p[prefix+"DNSIP"], p[prefix+"DNSDomain"] = "DoU", server, ""
			return
		}
		if strings.HasPrefix(server, "https://") {
			u, err := url.Parse(server)
			if err != nil || u.Hostname() == "" {
				continue
			}
			p[prefix+"DNSType"], p[prefix+"DNSDomain"] = "DoH", server
			if ip := net.ParseIP(u.Hostname()); ip != nil {
				p[prefix+"DNSIP"] = ip.String()
			}
			return
		}
	}
}
