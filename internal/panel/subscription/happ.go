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
// Keep its name stable: Happ updates profiles by name. Process rules and BitTorrent
// have no documented equivalent in this format.
func HappRouting(r service.ClientRouting) string {
	return buildHappRouting("happ", "Vynel", r)
}

var knownDoHBootstrap = map[string]string{
	"cloudflare-dns.com":       "1.1.1.1",
	"dns.google":               "8.8.8.8",
	"dns.quad9.net":            "9.9.9.9",
	"dns.adguard-dns.com":      "94.140.14.14",
	"common.dot.dns.yandex.net": "77.88.8.8",
	"freedns.controld.com":     "76.76.2.0",
}

func buildHappRouting(scheme, name string, r service.ClientRouting) string {
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
		"Name": name, "GlobalProxy": "true", "DomainStrategy": "IPIfNonMatch", "FakeDNS": "false",
		"RouteOrder":  "block-proxy-direct",
		"Geoipurl":    "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat",
		"Geositeurl":  "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geosite.dat",
		"DirectSites": list(direct), "DirectIp": list(directIP),
		"ProxySites": list(proxy), "ProxyIp": list(proxyIP),
		"BlockSites": list(block), "BlockIp": list(blockIP),
	}
	dnsHosts := map[string]string{}
	happDNS(p, dnsHosts, "Domestic", r.DNSDirect)
	happDNS(p, dnsHosts, "Remote", r.DNSProxy)
	if len(dnsHosts) > 0 {
		p["DnsHosts"] = dnsHosts
	}
	b, _ := json.Marshal(p) // only strings, string slices and maps
	return scheme + "://routing/onadd/" + base64.StdEncoding.EncodeToString(b)
}

// Happ has one resolver per direction. Prefer the first representable resolver with a known
// bootstrap IP, falling back to any valid https:// DoH resolver if none has a bootstrap IP.
func happDNS(p map[string]any, dnsHosts map[string]string, prefix string, servers []string) {
	var fallbackDoH string
	for _, server := range servers {
		if net.ParseIP(server) != nil {
			p[prefix+"DNSType"], p[prefix+"DNSIP"] = "DoU", server
			p[prefix+"DNSDomain"] = ""
			return
		}
		u, err := url.Parse(server)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || (u.Port() != "" && u.Port() != "443") {
			continue
		}
		host := u.Hostname()
		if net.ParseIP(host) != nil {
			p[prefix+"DNSType"], p[prefix+"DNSIP"], p[prefix+"DNSDomain"] = "DoH", host, server
			return
		}
		if ip := knownDoHBootstrap[strings.ToLower(host)]; ip != "" {
			p[prefix+"DNSType"], p[prefix+"DNSIP"], p[prefix+"DNSDomain"] = "DoH", ip, server
			dnsHosts[host] = ip
			return
		}
		if fallbackDoH == "" {
			fallbackDoH = server
		}
	}
	if fallbackDoH != "" {
		p[prefix+"DNSType"], p[prefix+"DNSDomain"] = "DoH", fallbackDoH
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
	if (format == FormatBase64 || format == FormatXray) && clientRe(`(?i)\bhapp\b`).MatchString(userAgent) {
		copyVars["happ_routing"] = HappRouting(r)
	}
	return RenderHeaders(hs, copyVars, userAgent)
}
