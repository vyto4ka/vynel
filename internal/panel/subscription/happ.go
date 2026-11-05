package subscription

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"net/url"
	"strings"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

// HappRouting is a subscription-bound Happ profile, not an Xray routing section.
// Keep its name stable: Happ updates profiles by name. Process rules, BitTorrent and
// fragment have no documented equivalent in this format.
func HappRouting(r service.ClientRouting) string {
	direct, directIP := split(r.Direct)
	proxy, proxyIP := split(r.Proxy)
	block, blockIP := split(r.Block)
	directIP = append([]string{"geoip:private"}, directIP...)
	if r.RUDirect {
		direct = append(append([]string{}, ruDomains...), direct...)
		directIP = append(directIP, "geoip:ru")
	}
	if r.BlockAds {
		block = append([]string{"geosite:category-ads-all"}, block...)
	}
	// Happ's lists must be arrays, including empty ones.
	list := func(v []string) []string {
		if v == nil {
			return []string{}
		}
		return v
	}
	p := map[string]any{
		"Name": "Vynel", "GlobalProxy": "true", "DomainStrategy": "IPIfNonMatch", "FakeDNS": "false",
		"Geoipurl":    "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat",
		"Geositeurl":  "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geosite.dat",
		"DirectSites": list(direct), "DirectIp": list(directIP),
		"ProxySites": list(proxy), "ProxyIp": list(proxyIP),
		"BlockSites": list(block), "BlockIp": list(blockIP),
	}
	happDNS(p, "Domestic", r.DNSDirect)
	happDNS(p, "Remote", r.DNSProxy)
	b, _ := json.Marshal(p) // only strings, string slices and maps
	return "happ://routing/onadd/" + base64.StdEncoding.EncodeToString(b)
}

// Happ has one resolver per direction. Prefer the first representable resolver;
// localhost and DoH names without a known bootstrap IP use Happ's defaults.
func happDNS(p map[string]any, prefix string, servers []string) {
	for _, server := range servers {
		if net.ParseIP(server) != nil {
			p[prefix+"DNSType"], p[prefix+"DNSIP"] = "DoU", server
			p[prefix+"DNSDomain"] = ""
			return
		}
		u, err := url.Parse(server)
		if err != nil || u.Scheme != "https" || u.Port() != "" && u.Port() != "443" {
			continue
		}
		ip := u.Hostname()
		if net.ParseIP(ip) == nil {
			ip = map[string]string{"cloudflare-dns.com": "1.1.1.1", "dns.google": "8.8.8.8"}[strings.ToLower(ip)]
		}
		if ip != "" {
			p[prefix+"DNSType"], p[prefix+"DNSIP"], p[prefix+"DNSDomain"] = "DoH", ip, server
			return
		}
	}
}

// RenderClientHeaders is shared by the response and the draft preview. The generated
// Happ profile is opt-in through {happ_routing}; manual headers remain authoritative.
func RenderClientHeaders(hs []service.SubHeader, vars Vars, userAgent string, format Format, r service.ClientRouting) []HeaderValue {
	copyVars := make(Vars, len(vars)+1)
	for k, v := range vars {
		copyVars[k] = v
	}
	copyVars["happ_routing"] = ""
	if format == FormatBase64 && clientRe(`(?i)\bhapp\b`).MatchString(userAgent) {
		copyVars["happ_routing"] = HappRouting(r)
	}
	return RenderHeaders(hs, copyVars, userAgent)
}
