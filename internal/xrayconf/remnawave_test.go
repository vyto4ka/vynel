package xrayconf

import (
	"strings"
	"testing"
)

// A full Remnawave-style config: a VLESS+Reality inbound the way Remnawave Config Profiles
// keep it (the shape of testdata/pdf_reality.json, with the keys of a real export).
const remnaFull = `{
  "log": { "loglevel": "warning" },
  "inbounds": [
    {
      "tag": "VLESS_NL",
      "port": 443,
      "listen": "0.0.0.0",
      "protocol": "vless",
      "settings": { "clients": [], "decryption": "none" },
      "sniffing": { "enabled": true, "routeOnly": true, "destOverride": ["http", "tls"] },
      "streamSettings": {
        "network": "raw",
        "sockopt": { "tcpFastOpen": true, "tcpcongestion": "bbr" },
        "security": "reality",
        "realitySettings": {
          "xver": 0,
          "target": "127.0.0.1:8443",
          "shortIds": ["a1b2c3d4e5f60718"],
          "privateKey": "SG1wpwXVHo3KVNFGjW-qT9Qh3k0p9_sWoJ88nyRvt30",
          "serverNames": ["nl.example.com", "www.example.com"]
        }
      }
    }
  ],
  "outbounds": [
    { "tag": "DIRECT", "protocol": "freedom", "settings": { "domainStrategy": "UseIPv4" } },
    { "tag": "BLOCK", "protocol": "blackhole" }
  ]
}`

// A single inbound object, as the Remnawave profile editor also allows.
const remnaInboundOnly = `{
  "tag": "hy",
  "port": 443,
  "protocol": "hysteria",
  "settings": { "clients": [] },
  "streamSettings": { "network": "hysteria", "security": "tls", "tlsSettings": { "alpn": ["h3"], "certificates": [{"certificateFile": "/x"}] } }
}`

const remnaNginx = `server {
    listen 443 ssl http2;
    server_name nl.example.com;
    location /api/stream {
        proxy_pass http://127.0.0.1:8090;
        proxy_http_version 1.1;
    }
}`

const remnaHost = `{
  "address": "nl.example.com",
  "port": 443,
  "security": "reality",
  "sni": "nl.example.com",
  "fingerprint": "chrome",
  "flow": "xtls-rprx-vision"
}`

func TestImportRemnawaveFull(t *testing.T) {
	res, err := ImportRemnawave(RemnawaveImport{Xray: remnaFull, Host: remnaHost, Nginx: remnaNginx, WithNginx: true}, "Reality из Remna")
	if err != nil {
		t.Fatal(err)
	}
	tpl, err := ParseTemplate([]byte(res.Source))
	if err != nil {
		t.Fatalf("the produced template does not parse: %v\n%s", err, res.Source)
	}
	if tpl.ID != "reality--remna" {
		t.Errorf("id = %q", tpl.ID)
	}
	if r, err := SampleRender(tpl); err != nil {
		t.Fatalf("sample render: %v", err)
	} else {
		if _, ok := r.Inbound["port"].(int); !ok {
			t.Errorf("port did not render as int: %T", r.Inbound["port"])
		}
		rs, _ := r.Inbound["streamSettings"].(map[string]any)["realitySettings"].(map[string]any)
		sns, _ := rs["serverNames"].([]any)
		if len(sns) < 2 || sns[1] != "www.example.com" {
			t.Errorf("extra serverNames lost: %v", rs["serverNames"])
		}
	}
	// The host must point clients at the generated key, not the exported one.
	if !strings.Contains(res.Source, "public_key: \"${REALITY_PUBLIC_KEY}\"") {
		t.Errorf("host does not use ${REALITY_PUBLIC_KEY}:\n%s", res.Source)
	}
	if strings.Contains(res.Source, "SG1wpwXVHo3KVNFGjW-qT9Qh3k0p9_sWoJ88nyRvt30") {
		t.Errorf("the exported private key leaked into the template")
	}
	// The nginx section must have appeared.
	if !strings.Contains(res.Source, "nginx:") {
		t.Errorf("no nginx section")
	}
}

func TestImportRemnawaveNoHostNoNginx(t *testing.T) {
	res, err := ImportRemnawave(RemnawaveImport{Xray: remnaFull}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Source, "host: {") {
		t.Errorf("no derived host section:\n%s", res.Source)
	}
	if strings.Contains(res.Source, "nginx:") {
		t.Errorf("nginx section present without the checkbox")
	}
}

func TestImportRemnawaveHysteria(t *testing.T) {
	res, err := ImportRemnawave(RemnawaveImport{Xray: remnaInboundOnly}, "Hy2")
	if err != nil {
		t.Fatal(err)
	}
	tpl, err := ParseTemplate([]byte(res.Source))
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, res.Source)
	}
	if tpl.Protocol != "hysteria" {
		t.Errorf("protocol = %q", tpl.Protocol)
	}
	if strings.Contains(res.Source, "certificates") {
		t.Errorf("certificate files leaked into the template")
	}
}

func TestImportRemnawaveErrors(t *testing.T) {
	if _, err := ImportRemnawave(RemnawaveImport{}, ""); err == nil {
		t.Error("empty xray must fail")
	}
	if _, err := ImportRemnawave(RemnawaveImport{Xray: `{`}, ""); err == nil {
		t.Error("broken JSON must fail")
	}
	if _, err := ImportRemnawave(RemnawaveImport{Xray: `{"log":{}}`}, ""); err == nil {
		t.Error("config without inbounds must fail")
	}
	if _, err := ImportRemnawave(RemnawaveImport{Xray: `{"inbounds":[{"protocol":"vmess"}]}`}, ""); err == nil {
		t.Error("vmess must fail")
	}
	if _, err := ImportRemnawave(RemnawaveImport{Xray: remnaFull, Host: `{`}, ""); err == nil {
		t.Error("broken host JSON must fail")
	}
}
