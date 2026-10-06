// Package subscription serves subscription URLs in the format each client understands
// (docs/ARCHITECTURE.md §8).
package subscription

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

// Format is a subscription body format.
type Format string

// Supported formats.
const (
	FormatBase64  Format = "base64"  // vless:// links, one per line, base64 (v2rayN/NG, Happ, v2RayTun, Streisand, Hiddify)
	FormatMihomo  Format = "mihomo"  // Clash Meta / Mihomo / FlClash / Stash YAML
	FormatSingBox Format = "singbox" // sing-box JSON (SFA/SFI/Karing)
	FormatXray    Format = "xray"    // JSON array of full Xray client configs
	FormatHTML    Format = "html"    // page for browsers
)

// ParseFormat accepts a format name or alias.
func ParseFormat(s string) (Format, bool) {
	switch strings.ToLower(s) {
	case "base64", "v2ray", "links", "raw":
		return FormatBase64, true
	case "mihomo", "clash", "clash-meta", "stash":
		return FormatMihomo, true
	case "singbox", "sing-box", "sb":
		return FormatSingBox, true
	case "xray", "json", "xray-json":
		return FormatXray, true
	case "html":
		return FormatHTML, true
	}
	return "", false
}

// Supports reports whether a format can express a host. Mihomo and sing-box get Reality hosts
// only: XHTTP with the CDN extra (cookies/query placement) is not supported there, and a
// half-working point is worse than none (docs/PROFILES.md §4.5).
func (f Format) Supports(h service.Host) bool {
	switch f {
	case FormatMihomo, FormatSingBox:
		return h.Network == "tcp"
	}
	return true
}

// StubUUID marks a stub server carrying a message instead of a real endpoint.
const StubUUID = "00000000-0000-0000-0000-000000000000"

// Stub is a fake server whose name explains why there are no real ones.
func Stub(message string) service.Host {
	return service.Host{Tag: "stub", Remark: message, Protocol: "vless", Network: "tcp", Security: "none", Address: "0.0.0.0", Port: 1}
}

// Link renders a vless:// link.
func Link(h service.Host, uuid string) string {
	q := url.Values{}
	q.Set("encryption", "none")
	q.Set("type", h.Network)
	q.Set("security", h.Security)
	if h.SNI != "" {
		q.Set("sni", h.SNI)
	}
	if h.Fingerprint != "" {
		q.Set("fp", h.Fingerprint)
	}
	switch h.Security {
	case "reality":
		q.Set("pbk", h.PublicKey)
		q.Set("sid", h.ShortID)
	case "tls":
		if len(h.ALPN) > 0 {
			q.Set("alpn", strings.Join(h.ALPN, ","))
		}
	}
	if h.Flow != "" {
		q.Set("flow", h.Flow)
	}
	if h.Network == "xhttp" {
		if h.HostHeader != "" {
			q.Set("host", h.HostHeader)
		}
		q.Set("path", h.Path)
		if h.Mode != "" {
			q.Set("mode", h.Mode)
		}
		if len(h.Extra) > 0 {
			b, _ := json.Marshal(h.Extra)
			q.Set("extra", string(b))
		}
	}
	return fmt.Sprintf("vless://%s@%s?%s#%s", uuid, net.JoinHostPort(h.Address, strconv.Itoa(h.Port)), q.Encode(), url.PathEscape(h.Remark))
}

// Base64 renders links for v2ray-style clients.
func Base64(hosts []service.Host, uuid string) []byte {
	lines := make([]string, 0, len(hosts))
	for _, h := range hosts {
		lines = append(lines, Link(h, uuid))
	}
	return []byte(base64.StdEncoding.EncodeToString([]byte(strings.Join(lines, "\n"))))
}

// uniqueNames makes remarks unique (Mihomo and sing-box key proxies by name).
func uniqueNames(hosts []service.Host) []string {
	seen := map[string]int{}
	out := make([]string, len(hosts))
	for i, h := range hosts {
		name := h.Remark
		if name == "" {
			name = h.Tag
		}
		seen[name]++
		if n := seen[name]; n > 1 {
			name = fmt.Sprintf("%s %d", name, n)
		}
		out[i] = name
	}
	return out
}

// Mihomo renders a Clash Meta / Mihomo profile.
func Mihomo(hosts []service.Host, uuid string) ([]byte, error) {
	names := uniqueNames(hosts)
	proxies := make([]map[string]any, 0, len(hosts))
	for i, h := range hosts {
		p := map[string]any{
			"name": names[i], "type": "vless", "server": h.Address, "port": h.Port, "uuid": uuid,
			"network": "tcp", "udp": true,
		}
		if h.Security == "reality" {
			p["tls"] = true
			p["servername"] = h.SNI
			p["reality-opts"] = map[string]any{"public-key": h.PublicKey, "short-id": h.ShortID}
			p["client-fingerprint"] = h.Fingerprint
		}
		if h.Flow != "" {
			p["flow"] = h.Flow
		}
		proxies = append(proxies, p)
	}
	doc := map[string]any{
		"mixed-port": 7890, "allow-lan": false, "mode": "rule", "log-level": "warning", "ipv6": false,
		"proxies":      proxies,
		"proxy-groups": []map[string]any{{"name": "VPN", "type": "select", "proxies": names}},
		"rules":        []string{"GEOIP,private,DIRECT,no-resolve", "MATCH,VPN"},
	}
	return yaml.Marshal(doc)
}

// SingBox renders a sing-box (1.11+) profile with a TUN inbound.
func SingBox(hosts []service.Host, uuid string) ([]byte, error) {
	names := uniqueNames(hosts)
	outbounds := []map[string]any{{"type": "selector", "tag": "proxy", "outbounds": names}}
	for i, h := range hosts {
		o := map[string]any{"type": "vless", "tag": names[i], "server": h.Address, "server_port": h.Port, "uuid": uuid}
		if h.Flow != "" {
			o["flow"] = h.Flow
		}
		if h.Security == "reality" {
			o["tls"] = map[string]any{
				"enabled": true, "server_name": h.SNI,
				"utls":    map[string]any{"enabled": true, "fingerprint": h.Fingerprint},
				"reality": map[string]any{"enabled": true, "public_key": h.PublicKey, "short_id": h.ShortID},
			}
		}
		outbounds = append(outbounds, o)
	}
	outbounds = append(outbounds, map[string]any{"type": "direct", "tag": "direct"})
	doc := map[string]any{
		"log":       map[string]any{"level": "warn"},
		"inbounds":  []map[string]any{{"type": "tun", "tag": "tun-in", "address": []string{"172.19.0.1/30"}, "auto_route": true, "strict_route": true}},
		"outbounds": outbounds,
		"route": map[string]any{
			"auto_detect_interface": true, "final": "proxy",
			"rules": []map[string]any{{"action": "sniff"}, {"protocol": "dns", "action": "hijack-dns"}, {"ip_is_private": true, "outbound": "direct"}},
		},
	}
	return json.MarshalIndent(doc, "", "  ")
}

// XrayOutbound renders the proxy outbound of a host.
func XrayOutbound(h service.Host, uuid string) map[string]any {
	user := map[string]any{"id": uuid, "encryption": "none"}
	if h.Flow != "" {
		user["flow"] = h.Flow
	}
	ss := map[string]any{"network": h.Network, "security": h.Security}
	if h.Network == "tcp" {
		ss["network"] = "raw"
	}
	switch h.Security {
	case "reality":
		ss["realitySettings"] = map[string]any{"serverName": h.SNI, "fingerprint": h.Fingerprint, "publicKey": h.PublicKey, "shortId": h.ShortID, "spiderX": ""}
	case "tls":
		tls := map[string]any{"serverName": h.SNI, "fingerprint": h.Fingerprint}
		if len(h.ALPN) > 0 {
			tls["alpn"] = h.ALPN
		}
		ss["tlsSettings"] = tls
	}
	if h.Network == "xhttp" {
		xs := map[string]any{"path": h.Path, "mode": h.Mode}
		if h.HostHeader != "" {
			xs["host"] = h.HostHeader
		}
		if len(h.Extra) > 0 {
			xs["extra"] = h.Extra
		}
		ss["xhttpSettings"] = xs
	}
	return map[string]any{
		"tag": "proxy", "protocol": "vless",
		"settings":       map[string]any{"vnext": []any{map[string]any{"address": h.Address, "port": h.Port, "users": []any{user}}}},
		"streamSettings": ss,
	}
}

// XrayJSON renders one full Xray client config per host (Happ, v2RayTun, v2rayN "JSON" subscriptions).
func XrayJSON(hosts []service.Host, uuid string) ([]byte, error) {
	configs := make([]map[string]any, 0, len(hosts))
	for _, h := range hosts {
		configs = append(configs, map[string]any{
			"remarks": h.Remark,
			"log":     map[string]any{"loglevel": "warning"},
			"dns":     map[string]any{"servers": []string{"1.1.1.1", "8.8.8.8"}},
			"inbounds": []any{
				map[string]any{"tag": "socks", "listen": "127.0.0.1", "port": 10808, "protocol": "socks",
					"settings": map[string]any{"udp": true},
					"sniffing": map[string]any{"enabled": true, "routeOnly": true, "destOverride": []string{"http", "tls", "quic"}}},
				map[string]any{"tag": "http", "listen": "127.0.0.1", "port": 10809, "protocol": "http"},
			},
			"outbounds": []any{
				XrayOutbound(h, uuid),
				map[string]any{"tag": "direct", "protocol": "freedom"},
				map[string]any{"tag": "block", "protocol": "blackhole"},
			},
			"routing": map[string]any{"domainStrategy": "IPIfNonMatch", "rules": []any{
				map[string]any{"type": "field", "ip": []string{"geoip:private"}, "outboundTag": "direct"},
			}},
		})
	}
	return json.MarshalIndent(configs, "", "  ")
}
