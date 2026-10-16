package service

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// HostOutbound is an Xray outbound to a connection point: the client's proxy in subscriptions,
// and the entry node's way to the exit node in a cascade.
func HostOutbound(h Host, uuid, tag string) map[string]any {
	if h.Protocol == "hysteria" {
		tls := map[string]any{"serverName": h.SNI}
		if len(h.ALPN) > 0 {
			tls["alpn"] = h.ALPN
		}
		return map[string]any{
			"tag": tag, "protocol": "hysteria",
			"settings": map[string]any{"version": 2, "address": h.Address, "port": h.Port},
			"streamSettings": map[string]any{"network": "hysteria", "security": "tls", "tlsSettings": tls,
				"hysteriaSettings": map[string]any{"version": 2, "auth": uuid}},
		}
	}
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
		"tag": tag, "protocol": "vless",
		"settings":       map[string]any{"vnext": []any{map[string]any{"address": h.Address, "port": h.Port, "users": []any{user}}}},
		"streamSettings": ss,
	}
}

// HostLink renders a vless:// link, or hysteria2:// for Hysteria2 hosts (the UUID is the
// password); a server from another subscription that is not one of those keeps its own link.
func HostLink(h Host, uuid string) string {
	if h.Raw != "" {
		return h.Raw
	}
	if h.Protocol == "hysteria" {
		q := url.Values{}
		if h.SNI != "" {
			q.Set("sni", h.SNI)
		}
		if len(h.ALPN) > 0 {
			q.Set("alpn", strings.Join(h.ALPN, ","))
		}
		q.Set("insecure", "0")
		return fmt.Sprintf("hysteria2://%s@%s/?%s#%s", uuid, net.JoinHostPort(h.Address, strconv.Itoa(h.Port)), q.Encode(), url.PathEscape(h.Remark))
	}
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
