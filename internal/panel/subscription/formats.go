// Package subscription serves subscription URLs in the format each client understands
// (docs/ARCHITECTURE.md §8).
package subscription

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

// Format is a subscription body format.
type Format string

// Supported formats.
const (
	FormatBase64  Format = "base64"  // vless:// and hysteria2:// links, one per line, base64 (v2rayN/NG, Happ, v2RayTun, Streisand, Hiddify)
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

// Supports reports whether a format can express a host. sing-box gets plain TCP hosts only: it
// has no XHTTP. Mihomo gets XHTTP hosts only when the template opts in (host.mihomo): xhttp-opts
// needs Mihomo 1.19+, and an older core rejects the whole profile over one unknown network, so a
// half-working point is worse than none (docs/PROFILES.md §4.5).
func (f Format) Supports(h service.Host) bool {
	switch f {
	case FormatMihomo:
		return h.Raw == "" && (h.Network == "tcp" || h.Network == "hysteria" || (h.Network == "xhttp" && h.Mihomo))
	case FormatSingBox:
		return h.Raw == "" && (h.Network == "tcp" || h.Network == "hysteria")
	}
	return h.Raw == "" || f == FormatBase64
}

// StubUUID marks a stub server carrying a message instead of a real endpoint.
const StubUUID = "00000000-0000-0000-0000-000000000000"

// Stub is a fake server whose name explains why there are no real ones.
func Stub(message string) service.Host {
	return service.Host{Tag: "stub", Remark: message, Protocol: "vless", Network: "tcp", Security: "none", Address: "0.0.0.0", Port: 1}
}

// Link renders a vless:// link, or hysteria2:// for Hysteria2 hosts (the UUID is the password).
func Link(h service.Host, uuid string) string {
	return service.HostLink(h, cred(h, uuid))
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
func Mihomo(hosts []service.Host, uuid string, r service.ClientRouting) ([]byte, error) {
	proxies, names := mihomoProxies(hosts, uuid)
	doc := map[string]any{
		"mixed-port": 7890, "allow-lan": false, "mode": "rule", "log-level": "warning", "ipv6": false,
		"proxies":      proxies,
		"proxy-groups": []map[string]any{{"name": "VPN", "type": "select", "proxies": names}},
		"rules":        mihomoRules(r, "VPN"),
	}
	if dns := mihomoDNS(r, "VPN"); dns != nil {
		doc["dns"] = dns
	}
	return yaml.Marshal(doc)
}

// mihomoProxies renders the hosts as Mihomo proxies, with their (unique) names.
func mihomoProxies(hosts []service.Host, uuid string) ([]map[string]any, []string) {
	names := uniqueNames(hosts)
	proxies := make([]map[string]any, 0, len(hosts))
	for i, h := range hosts {
		uuid := cred(h, uuid)
		if h.Protocol == "hysteria" {
			p := map[string]any{"name": names[i], "type": "hysteria2", "server": h.Address, "port": h.Port, "password": uuid, "udp": true}
			if h.SNI != "" {
				p["sni"] = h.SNI
			}
			if len(h.ALPN) > 0 {
				p["alpn"] = h.ALPN
			}
			proxies = append(proxies, p)
			continue
		}
		p := map[string]any{
			"name": names[i], "type": "vless", "server": h.Address, "port": h.Port, "uuid": uuid,
			"network": "tcp", "udp": true,
		}
		switch h.Security {
		case "reality":
			p["tls"] = true
			p["servername"] = h.SNI
			ro := map[string]any{"public-key": h.PublicKey, "short-id": h.ShortID}
			if h.MLKEM {
				ro["support-x25519mlkem768"] = true
			}
			p["reality-opts"] = ro
			p["client-fingerprint"] = mihomoFingerprint(h.Fingerprint)
		case "tls":
			p["tls"] = true
			p["servername"] = h.SNI
			if h.Fingerprint != "" {
				p["client-fingerprint"] = mihomoFingerprint(h.Fingerprint)
			}
			if len(h.ALPN) > 0 {
				p["alpn"] = h.ALPN
			}
		}
		if h.Flow != "" {
			p["flow"] = h.Flow
		}
		if h.Network == "xhttp" {
			p["network"] = "xhttp"
			p["xhttp-opts"] = mihomoXHTTP(h)
		}
		proxies = append(proxies, p)
	}
	return proxies, names
}

// mihomoFingerprint maps uTLS names Mihomo does not know ("randomized") to its closest one.
func mihomoFingerprint(fp string) string {
	if fp == "randomized" || fp == "randomizednoalpn" {
		return "random"
	}
	return fp
}

// mihomoXHTTPKeys maps Xray xhttp extra keys to Mihomo xhttp-opts (adapter/outbound/vless.go).
// sessionIDPlacement/sessionIDKey have no Mihomo twin: session-placement/session-key cover both.
var mihomoXHTTPKeys = map[string]string{
	"noGRPCHeader": "no-grpc-header", "xPaddingBytes": "x-padding-bytes", "xPaddingObfsMode": "x-padding-obfs-mode",
	"xPaddingKey": "x-padding-key", "xPaddingHeader": "x-padding-header", "xPaddingPlacement": "x-padding-placement",
	"xPaddingMethod": "x-padding-method", "uplinkHTTPMethod": "uplink-http-method", "sessionPlacement": "session-placement",
	"sessionKey": "session-key", "seqPlacement": "seq-placement", "seqKey": "seq-key",
	"uplinkDataPlacement": "uplink-data-placement", "uplinkDataKey": "uplink-data-key", "uplinkChunkSize": "uplink-chunk-size",
	"scMaxEachPostBytes": "sc-max-each-post-bytes", "scMinPostsIntervalMs": "sc-min-posts-interval-ms",
	"headers": "headers",
}

var mihomoXmuxKeys = map[string]string{
	"maxConcurrency": "max-concurrency", "maxConnections": "max-connections", "cMaxReuseTimes": "c-max-reuse-times",
	"hMaxRequestTimes": "h-max-request-times", "hMaxReusableSecs": "h-max-reusable-secs", "hKeepAlivePeriod": "h-keep-alive-period",
}

// mihomoXHTTP converts the client xhttp settings into Mihomo xhttp-opts. Mihomo wants range
// fields as strings and h-keep-alive-period as a number; the mode must be explicit (the guide's
// warning: "mode" inside extra does not survive conversion on its own).
func mihomoXHTTP(h service.Host) map[string]any {
	// Xray clients add the trailing slash the server expects ("/sync" is served as "/sync/");
	// Mihomo sends the path verbatim and gets "failed to validate path".
	path := h.Path
	if !strings.HasSuffix(path, "/") && !strings.Contains(path, "?") {
		path += "/"
	}
	o := map[string]any{"path": path}
	if h.HostHeader != "" {
		o["host"] = h.HostHeader
	}
	mode := h.Mode
	if m, ok := h.Extra["mode"].(string); ok && m != "" {
		mode = m
	}
	if mode != "" {
		o["mode"] = mode
	}
	for k, v := range h.Extra {
		if mk, ok := mihomoXHTTPKeys[k]; ok {
			o[mk] = mihomoValue(v, mk != "x-padding-obfs-mode" && mk != "no-grpc-header" && mk != "headers")
		}
	}
	if xm, ok := h.Extra["xmux"].(map[string]any); ok {
		rs := map[string]any{}
		for k, v := range xm {
			if mk, ok := mihomoXmuxKeys[k]; ok {
				rs[mk] = mihomoValue(v, mk != "h-keep-alive-period")
			}
		}
		if len(rs) > 0 {
			o["reuse-settings"] = rs
		}
	}
	return o
}

// mihomoValue renders numbers as strings for Mihomo's string range fields.
func mihomoValue(v any, asString bool) any {
	if !asString {
		return v
	}
	switch n := v.(type) {
	case int, int64, float64:
		return fmt.Sprint(n)
	}
	return v
}

// SingBox renders a sing-box (1.12+) profile with a TUN inbound.
func SingBox(hosts []service.Host, uuid string, r service.ClientRouting) ([]byte, error) {
	servers, names := singBoxOutbounds(hosts, uuid)
	outbounds := append([]map[string]any{{"type": "selector", "tag": "proxy", "outbounds": names}}, servers...)
	outbounds = append(outbounds, map[string]any{"type": "direct", "tag": "direct"})
	route, dns := singBoxRouting(r)
	doc := map[string]any{
		"log":       map[string]any{"level": "warn"},
		"inbounds":  []map[string]any{{"type": "tun", "tag": "tun-in", "address": []string{"172.19.0.1/30"}, "auto_route": true, "strict_route": true}},
		"outbounds": outbounds,
		"route":     route,
	}
	if dns != nil {
		doc["dns"] = dns
	}
	return json.MarshalIndent(doc, "", "  ")
}

// singBoxOutbounds renders the hosts as sing-box outbounds, with their (unique) tags.
func singBoxOutbounds(hosts []service.Host, uuid string) ([]map[string]any, []string) {
	names := uniqueNames(hosts)
	var outbounds []map[string]any
	for i, h := range hosts {
		uuid := cred(h, uuid)
		if h.Protocol == "hysteria" {
			tls := map[string]any{"enabled": true, "server_name": h.SNI}
			if len(h.ALPN) > 0 {
				tls["alpn"] = h.ALPN
			}
			outbounds = append(outbounds, map[string]any{"type": "hysteria2", "tag": names[i], "server": h.Address,
				"server_port": h.Port, "password": uuid, "tls": tls})
			continue
		}
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
	return outbounds, names
}

// XrayOutbound renders the proxy outbound of a host.
func XrayOutbound(h service.Host, uuid string) map[string]any {
	return service.HostOutbound(h, cred(h, uuid), "proxy")
}

// cred is the secret a host takes: its own (a cascade, another subscription) or the user's.
func cred(h service.Host, uuid string) string {
	if h.UUID != "" && uuid != StubUUID {
		return h.UUID
	}
	return uuid
}

// XrayJSON renders one full Xray client config per host (KeqDroid, Happ, v2RayTun, v2rayN
// "JSON" subscriptions), with DNS and routing: a profile's own client config wins over r.
func XrayJSON(hosts []service.Host, uuid string, r service.ClientRouting) ([]byte, error) {
	configs := make([]map[string]any, 0, len(hosts))
	for _, h := range hosts {
		configs = append(configs, xrayClient(h, uuid, r))
	}
	return json.MarshalIndent(configs, "", "  ")
}
