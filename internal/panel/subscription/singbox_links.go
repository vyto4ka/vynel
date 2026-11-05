package subscription

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

func decodeLinkBase64(s string) ([]byte, bool) {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, true
		}
	}
	return nil, false
}

// singBoxLink translates external URI servers into sing-box outbound fields. Keep this
// separate from service.Host: Xray's streamSettings/encryption/UUID fields are not sing-box's
// transport/tls/password fields. Unsupported transports are rejected, never changed to TCP.
func singBoxLink(raw string) (map[string]any, bool) {
	if strings.HasPrefix(raw, "vmess://") {
		return singBoxVMess(strings.TrimPrefix(raw, "vmess://"))
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, false
	}
	// Legacy Shadowsocks encodes the entire method:password@host:port, SIP002 only userinfo.
	if u.Scheme == "ss" && u.User == nil {
		encoded, _, _ := strings.Cut(strings.TrimPrefix(raw, "ss://"), "#")
		if b, ok := decodeLinkBase64(encoded); ok {
			decoded, err := url.Parse("ss://" + string(b))
			if err == nil {
				decoded.Fragment = u.Fragment
				u = decoded
			}
		}
	}
	noAuth := u.Scheme == "socks" || u.Scheme == "socks5" || u.Scheme == "socks5h"
	if u.User == nil && !noAuth || u.Hostname() == "" {
		return nil, false
	}
	q := u.Query()
	if q.Get("pinSHA256") != "" || q.Get("ech") != "" {
		return nil, false // do not discard certificate constraints or ECH configuration
	}
	port, err := strconv.Atoi(u.Port())
	if u.Port() == "" && (u.Scheme == "hy2" || u.Scheme == "hysteria2" || u.Scheme == "trojan" || u.Scheme == "anytls") {
		port, err = 443, nil
	}
	if err != nil || port < 1 || port > 65535 {
		return nil, false
	}
	name := u.Fragment
	if name == "" {
		name = u.Hostname()
	}
	o := map[string]any{"tag": name, "server": u.Hostname(), "server_port": port}
	password := ""
	if u.User != nil {
		password = u.User.Username()
	}
	first := func(keys ...string) string {
		for _, k := range keys {
			if v := q.Get(k); v != "" {
				return v
			}
		}
		return ""
	}
	switch u.Scheme {
	case "vless":
		if e := q.Get("encryption"); e != "" && e != "none" {
			return nil, false
		}
		o["type"], o["uuid"] = "vless", password
		if flow := q.Get("flow"); flow != "" {
			o["flow"] = flow
		}
	case "trojan", "anytls":
		o["type"], o["password"] = u.Scheme, password
	case "hy2", "hysteria2":
		if _, hasPassword := u.User.Password(); hasPassword || q.Get("pinSHA256") != "" {
			return nil, false
		}
		o["type"], o["password"] = "hysteria2", password
		if ports := first("mport", "ports"); ports != "" {
			o["server_ports"] = strings.Split(strings.ReplaceAll(ports, "-", ":"), ",")
		}
		if obfs := q.Get("obfs"); obfs != "" {
			if obfs != "salamander" || q.Get("obfs-password") == "" {
				return nil, false
			}
			o["obfs"] = map[string]any{"type": obfs, "password": q.Get("obfs-password")}
		}
	case "ss":
		if q.Get("plugin") != "" {
			return nil, false
		}
		method, secret := password, ""
		if p, ok := u.User.Password(); ok {
			secret = p
		} else if b, ok := decodeLinkBase64(password); ok {
			var hasColon bool
			method, secret, hasColon = strings.Cut(string(b), ":")
			if !hasColon {
				return nil, false
			}
		}
		if method == "" || secret == "" {
			return nil, false
		}
		o["type"], o["method"], o["password"] = "shadowsocks", method, secret
		return o, true
	case "socks", "socks5", "socks5h":
		o["type"], o["version"] = "socks", "5"
		if u.User != nil {
			o["username"] = password
			if p, ok := u.User.Password(); ok {
				o["password"] = p
			}
		}
		return o, true
	case "tuic":
		o["type"], o["uuid"] = "tuic", password
		p, ok := u.User.Password()
		if !ok {
			return nil, false
		}
		o["password"] = p
		for _, key := range []string{"congestion_control", "udp_relay_mode"} {
			if v := q.Get(key); v != "" {
				o[key] = v
			}
		}
	default:
		return nil, false
	}
	security := q.Get("security")
	if security == "" && o["type"] != "vless" {
		security = "tls"
	}
	switch security {
	case "tls", "reality":
		tls := map[string]any{"enabled": true, "server_name": first("sni", "peer", "serverName")}
		if tls["server_name"] == "" {
			tls["server_name"] = u.Hostname()
		}
		if fp := q.Get("fp"); fp != "" && o["type"] != "hysteria2" && o["type"] != "tuic" {
			tls["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
		}
		if alpn := q.Get("alpn"); alpn != "" {
			tls["alpn"] = strings.Split(alpn, ",")
		}
		if v := first("allowInsecure", "insecure"); v == "1" || v == "true" {
			tls["insecure"] = true
		}
		if security == "reality" {
			if q.Get("pbk") == "" {
				return nil, false
			}
			tls["reality"] = map[string]any{"enabled": true, "public_key": q.Get("pbk"), "short_id": q.Get("sid")}
			if tls["utls"] == nil {
				tls["utls"] = map[string]any{"enabled": true, "fingerprint": "chrome"}
			}
		}
		o["tls"] = tls
	case "", "none":
	default:
		return nil, false
	}
	if o["type"] == "vless" || o["type"] == "trojan" {
		if !singBoxTransport(o, q.Get("type"), q.Get("host"), q.Get("path"), q.Get("serviceName"), q.Get("headerType")) {
			return nil, false
		}
	}
	return o, true
}

func singBoxTransport(o map[string]any, network, host, path, serviceName, header string) bool {
	switch network {
	case "", "tcp", "raw":
		return header == "" || header == "none"
	case "ws", "httpupgrade":
		t := map[string]any{"type": network, "path": path}
		if network == "ws" {
			if u, err := url.Parse(path); err == nil && u.Query().Has("ed") {
				early, err := strconv.Atoi(u.Query().Get("ed"))
				if err != nil || early < 0 {
					return false
				}
				q := u.Query()
				q.Del("ed")
				u.RawQuery = q.Encode()
				t["path"], t["max_early_data"], t["early_data_header_name"] = u.String(), early, "Sec-WebSocket-Protocol"
			}
		}
		if host != "" {
			if network == "ws" {
				t["headers"] = map[string]any{"Host": host}
			} else {
				t["host"] = host
			}
		}
		o["transport"] = t
	case "grpc":
		if serviceName == "" {
			serviceName = strings.TrimPrefix(path, "/")
		}
		o["transport"] = map[string]any{"type": "grpc", "service_name": serviceName}
	case "http", "h2":
		t := map[string]any{"type": "http", "path": path}
		if host != "" {
			t["host"] = strings.Split(host, ",")
		}
		o["transport"] = t
	default:
		return false // XHTTP and other Xray-only transports must not become plain TCP
	}
	return true
}

func singBoxVMess(payload string) (map[string]any, bool) {
	payload, _, _ = strings.Cut(payload, "#")
	b, ok := decodeLinkBase64(payload)
	if !ok {
		return nil, false
	}
	var v map[string]any
	if json.Unmarshal(b, &v) != nil {
		return nil, false
	}
	str := func(k string) string {
		if v[k] == nil {
			return ""
		}
		return fmt.Sprint(v[k])
	}
	port, err := strconv.Atoi(str("port"))
	if err != nil || port < 1 || port > 65535 || str("add") == "" || str("id") == "" {
		return nil, false
	}
	name := str("ps")
	if name == "" {
		name = str("add")
	}
	security := str("scy")
	if security == "" {
		security = "auto"
	}
	o := map[string]any{"type": "vmess", "tag": name, "server": str("add"), "server_port": port, "uuid": str("id"), "security": security}
	if id, _ := strconv.Atoi(str("aid")); id != 0 {
		o["alter_id"] = id
	}
	if str("tls") == "tls" {
		sni := str("sni")
		if sni == "" {
			sni = str("host")
		}
		tls := map[string]any{"enabled": true, "server_name": sni}
		if fp := str("fp"); fp != "" {
			tls["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
		}
		if alpn := str("alpn"); alpn != "" {
			tls["alpn"] = strings.Split(alpn, ",")
		}
		o["tls"] = tls
	}
	if !singBoxTransport(o, str("net"), str("host"), str("path"), str("serviceName"), str("type")) {
		return nil, false
	}
	return o, true
}
