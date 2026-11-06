package subscription

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
)

func TestSingBoxExternalLinkFields(t *testing.T) {
	const id = "11111111-2222-4333-8444-555555555555"
	vmess, _ := json.Marshal(map[string]any{"v": "2", "ps": "VMess", "add": "vmess.example.org", "port": "443", "id": id,
		"net": "ws", "host": "cdn.example.org", "path": "/vm", "tls": "tls", "sni": "tls.example.org", "aid": "0"})
	cases := []struct {
		link string
		want []string
	}{
		{"vless://" + id + "@ws.example.org:443?type=ws&security=tls&sni=tls.example.org&host=cdn.example.org&path=%2Fws#WS",
			[]string{`"type":"vless"`, `"server_port":443`, `"uuid":"` + id + `"`, `"transport":{"headers":{"Host":"cdn.example.org"},"path":"/ws","type":"ws"}`, `"server_name":"tls.example.org"`}},
		{"vless://" + id + "@grpc.example.org:443?type=grpc&security=tls&serviceName=svc#GRPC",
			[]string{`"transport":{"service_name":"svc","type":"grpc"}`}},
		{"vless://" + id + "@reality.example.org:443?type=grpc&security=reality&pbk=qGPTy8EZokn3hWp6hKBQ0MVvEuLRJCcv5UdWeP4TVhI&sid=ab&serviceName=svc#Reality",
			[]string{`"public_key":"qGPTy8EZokn3hWp6hKBQ0MVvEuLRJCcv5UdWeP4TVhI"`, `"fingerprint":"chrome"`}},
		{"trojan://p%40ss%2F%23%3F@trojan.example.org:443?type=grpc&serviceName=rpc&sni=tls.example.org#Trojan",
			[]string{`"type":"trojan"`, `"password":"p@ss/#?"`, `"service_name":"rpc"`}},
		{"vmess://" + base64.StdEncoding.EncodeToString(vmess),
			[]string{`"type":"vmess"`, `"security":"auto"`, `"tag":"VMess"`, `"path":"/vm"`}},
		{"ss://" + base64.RawURLEncoding.EncodeToString([]byte("aes-128-gcm:p@ss/#?")) + "@ss.example.org:8388#SS",
			[]string{`"type":"shadowsocks"`, `"method":"aes-128-gcm"`, `"password":"p@ss/#?"`}},
		{"hy2://secret@hy.example.org?insecure=1&obfs=salamander&obfs-password=mask&sni=tls.example.org#HY",
			[]string{`"type":"hysteria2"`, `"password":"secret"`, `"insecure":true`, `"obfs":{"password":"mask","type":"salamander"}`}},
		{"socks5://user:pass@socks.example.org:1080#SOCKS",
			[]string{`"type":"socks"`, `"version":"5"`, `"username":"user"`, `"password":"pass"`}},
		{"socks://socks.example.org:1080#NoAuth", []string{`"type":"socks"`, `"server_port":1080`}},
		{"vless://" + id + "@ws.example.org:443?type=ws&security=tls&path=%2Fws%3Fed%3D2048#Early",
			[]string{`"max_early_data":2048`, `"early_data_header_name":"Sec-WebSocket-Protocol"`, `"path":"/ws"`}},
		{"vless://" + id + "@tls.example.org:443?type=tcp&security=tls&allowInsecure=1#TLS",
			[]string{`"insecure":true`, `"enabled":true`, `"server_name":"tls.example.org"`}},
		{"tuic://" + id + ":secret@tuic.example.org:443?congestion_control=bbr&udp_relay_mode=native#TUIC",
			[]string{`"type":"tuic"`, `"uuid":"` + id + `"`, `"congestion_control":"bbr"`}},
		{"anytls://secret@any.example.org:443#AnyTLS", []string{`"type":"anytls"`, `"password":"secret"`}},
	}
	for _, tc := range cases {
		p, ok := singBoxLink(tc.link)
		if !ok {
			t.Fatalf("supported link refused: %s", tc.link)
		}
		b, _ := json.Marshal(p)
		for _, want := range tc.want {
			if !strings.Contains(string(b), want) {
				t.Fatalf("missing %s in %s", want, b)
			}
		}
		if strings.Contains(string(b), "streamSettings") || strings.Contains(string(b), "serverName") {
			t.Fatalf("Xray fields leaked into sing-box: %s", b)
		}
		if bin := os.Getenv("SINGBOX_BIN"); bin != "" {
			b, _ := json.Marshal(map[string]any{"outbounds": []any{p}})
			f := filepath.Join(t.TempDir(), "outbound.json")
			if err := os.WriteFile(f, b, 0600); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(bin, "check", "-c", f).CombinedOutput(); err != nil {
				t.Fatalf("sing-box rejects outbound: %s\n%s\n%v", b, out, err)
			}
		}
	}
	for _, raw := range []string{
		"vless://x@x.example.org:443?type=xhttp&security=tls",
		"vless://x@x.example.org:443?type=unknown",
		"vless://x@x.example.org:443?encryption=mlkem",
		"vmess://broken", "ss://broken", "hy2://secret@x.example.org:443?pinSHA256=cert-hash",
	} {
		if _, ok := singBoxLink(raw); ok {
			t.Fatalf("unsupported link silently became a server: %s", raw)
		}
	}
}

// Karing's Reality verification needs a complete TLS block for every VLESS server: own servers
// get their public key, short id, SNI and uTLS fingerprint; external Reality links must not
// lose pbk/sid either (Karing error «reality verification failed»).
func TestKaringRealityFields(t *testing.T) {
	const (
		id  = "11111111-2222-4333-8444-555555555555"
		pbk = "qGPTy8EZokn3hWp6hKBQ0MVvEuLRJCcv5UdWeP4TVhI"
	)
	own := []service.Host{{
		Remark: "NL", Protocol: "vless", Network: "tcp", Address: "nl.example.com", Port: 443,
		Security: "reality", SNI: "www.microsoft.com", PublicKey: pbk, ShortID: "ab12cd",
		Fingerprint: "firefox", Flow: "xtls-rprx-vision",
	}}
	r := service.DefaultClientRouting()
	b, err := Karing(own, id, "https://sub.example.com/s/t", r)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	check := func(ob map[string]any) {
		tls, ok := ob["tls"].(map[string]any)
		if !ok {
			t.Fatalf("no tls block: %v", ob)
		}
		reality, _ := tls["reality"].(map[string]any)
		utls, _ := tls["utls"].(map[string]any)
		if reality["enabled"] != true || reality["public_key"] != pbk || reality["short_id"] == "" || reality["short_id"] == nil {
			t.Fatalf("incomplete reality: %v", tls)
		}
		if utls["enabled"] != true || utls["fingerprint"] == "" || utls["fingerprint"] == nil {
			t.Fatalf("incomplete utls: %v", tls)
		}
		if tls["server_name"] == "" || tls["server_name"] == nil {
			t.Fatalf("no SNI: %v", tls)
		}
	}
	var sawOwn bool
	for _, ob := range cfg.Outbounds {
		if ob["type"] == "vless" {
			check(ob)
			sawOwn = true
		}
	}
	if !sawOwn {
		t.Fatal("own vless server missing")
	}
	// An external Reality link converts to sing-box with the same fields kept.
	p, ok := singBoxLink("vless://" + id + "@ext.example.org:443?type=tcp&security=reality&pbk=" + pbk + "&sid=ab&sni=www.microsoft.com#Ext")
	if !ok {
		t.Fatal("external Reality link refused")
	}
	check(p)
}

func TestKaringImportsExternalServersAndRules(t *testing.T) {
	e := newEnv(t)
	const ws = "vless://11111111-2222-4333-8444-555555555555@ws.example.org:443?type=ws&security=tls&host=cdn.example.org&path=%2Fws#direct"
	ext, err := e.svc.CreateExternalSub(e.ctx, actor, service.ExternalSubInput{Name: "external", URL: ws + "\ntrojan://secret@tr.example.org:443#direct", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.PutGrant(e.ctx, actor, service.GrantInput{UserID: e.user.ID, Kind: store.GrantExternal, RefID: ext.ID}); err != nil {
		t.Fatal(err)
	}
	r := service.DefaultClientRouting()
	r.BlockAds, r.Block = true, []string{"domain:ads.example.org"}
	r.Proxy = []string{"full:exception.ru", "geoip:nl"}
	r.Direct = []string{"domain:example.org", "10.1.0.0/16", "process:game.exe", "process:com.example.game"}
	if err := e.svc.SetClientRouting(e.ctx, actor, &r); err != nil {
		t.Fatal(err)
	}
	response := e.get(e.path(), "karing/1.2.25.2802 android", "kr-1")
	var cfg struct {
		Outbounds []map[string]any `json:"outbounds"`
		Route     struct {
			Sets  []map[string]any `json:"rule_set"`
			Rules []map[string]any `json:"rules"`
		} `json:"route"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &cfg); err != nil {
		t.Fatal(err)
	}
	// One own Reality server + two external servers; XHTTP stays out. Reserved and
	// duplicate names must not collide with the direct/block/selector outbounds.
	if len(cfg.Outbounds) != 6 {
		t.Fatalf("missing external servers: %s", response.Body.String())
	}
	if strings.Contains(response.Body.String(), `"inbounds"`) || strings.Contains(response.Body.String(), `"dns"`) {
		t.Fatal("Karing owns DNS/TUN; do not pretend those settings are imported")
	}
	seen := map[string]bool{}
	for _, p := range cfg.Outbounds {
		tag := p["tag"].(string)
		if seen[tag] {
			t.Fatalf("duplicate tag %s", tag)
		}
		seen[tag] = true
	}
	blockAt, proxyAt, privateAt, ruAt := -1, -1, -1, -1
	for i, rule := range cfg.Route.Rules {
		if len(rule) != 2 || rule["rule_set"] == nil || rule["outbound"] == nil {
			t.Fatalf("Karing cannot import %v", rule)
		}
		switch rule["rule_set"] {
		case "vynel-block":
			blockAt = i
		case "vynel-proxy":
			proxyAt = i
		case "vynel-private":
			privateAt = i
		case "vynel-ru":
			ruAt = i
		}
	}
	if blockAt < 0 || proxyAt <= blockAt || privateAt <= proxyAt || ruAt <= privateAt {
		t.Fatal("routing priority lost")
	}
	devicesBefore, _ := e.svc.Devices(e.ctx, e.user.ID)
	var directURL string
	for _, set := range cfg.Route.Sets {
		if set["format"] != "source" {
			continue
		}
		u, err := url.Parse(set["url"].(string))
		if err != nil {
			t.Fatal(err)
		}
		if set["tag"] == "vynel-direct" {
			directURL = u.String()
		}
		rec := e.get(u.RequestURI(), "sing-box", "")
		if rec.Code != 200 || !json.Valid(rec.Body.Bytes()) || strings.Contains(rec.Body.String(), e.user.UUID) || strings.Contains(rec.Body.String(), "secret") {
			t.Fatalf("bad rules response: %d %s", rec.Code, rec.Body.String())
		}
		if set["tag"] == "vynel-direct" && (!strings.Contains(rec.Body.String(), `"package_name":["com.example.game"]`) || !strings.Contains(rec.Body.String(), `"process_name"`)) {
			t.Fatal("process/package matchers lost")
		}
		if bin := os.Getenv("SINGBOX_BIN"); bin != "" {
			f := filepath.Join(t.TempDir(), "rules.json")
			if err := os.WriteFile(f, rec.Body.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(bin, "rule-set", "compile", f).CombinedOutput(); err != nil {
				t.Fatalf("invalid source rules: %v\n%s\n%s", err, out, rec.Body.String())
			}
		}
	}
	devicesAfter, _ := e.svc.Devices(e.ctx, e.user.ID)
	if len(devicesAfter) != len(devicesBefore) {
		t.Fatal("rule sets consumed device slots")
	}
	r.Direct = []string{"domain:changed.example.org"}
	_, sources := karingRouting("https://sub.example.com"+e.path(), r)
	if sources["direct"] == nil {
		t.Fatal("lost changed rule set")
	}
	route, _ := karingRouting("https://sub.example.com"+e.path(), r)
	for _, rs := range route["rule_set"].([]map[string]any) {
		if rs["tag"] == "vynel-direct" && rs["url"] == directURL {
			t.Fatal("rules URL did not change; Karing would retain its cached source")
		}
	}
	if rec := e.get(e.path()+"/karing-rules/missing", "sing-box", ""); rec.Code != 404 {
		t.Fatal("unknown rule-set accepted")
	}
	if rec := e.get(service.DefaultSubPrefix+"invalid-token/karing-rules/private", "sing-box", ""); rec.Code != 404 {
		t.Fatal("unknown token received a rule set")
	}
	if _, err := e.svc.SetUserEnabled(e.ctx, actor, e.user.ID, false); err != nil {
		t.Fatal(err)
	}
	if rec := e.get(e.path()+"/karing-rules/private", "sing-box", ""); rec.Code != 404 {
		t.Fatal("disabled token received rules")
	}
	refused := e.get(e.path(), "karing/1.2.25.2802", "kr-1")
	if strings.Contains(refused.Body.String(), e.user.UUID) || strings.Contains(refused.Body.String(), "secret") || strings.Contains(refused.Body.String(), "ws.example.org") {
		t.Fatal("refused client received credentials")
	}
}

func TestKaringDefaultRuleMigration(t *testing.T) {
	e := newEnv(t)
	for _, tc := range []struct{ note, format, template, want string }{
		{"Karing", "base64", "", "karing"},
		{"Karing сам собирает sing-box из ссылок; XHTTP ему не отдаётся", "base64", "", "karing"},
		{"My links", "base64", "", "base64"},
		{"Karing", "singbox", `{ "outbounds": [] }`, "singbox"},
	} {
		rules := []service.SubRule{{Pattern: "(?i)karing", Format: tc.format, Template: tc.template, Note: tc.note, Enabled: true}}
		b, _ := json.Marshal(rules)
		if err := e.svc.SetSetting(e.ctx, actor, service.SettingSubRules, string(b)); err != nil {
			t.Fatal(err)
		}
		if err := e.svc.EnsureDefaults(e.ctx); err != nil {
			t.Fatal(err)
		}
		got, _ := e.svc.SubRules(e.ctx)
		if got[0].Format != tc.want || got[0].Template != tc.template {
			t.Fatalf("migration changed custom rule: %+v", got)
		}
	}
}

func TestSingBoxTLSAndDNSFields(t *testing.T) {
	out, _ := singBoxOutbounds([]service.Host{{Remark: "TLS", Protocol: "vless", Network: "tcp", Address: "tls.example.org", Port: 443, Security: "tls", SNI: "sni.example.org", ALPN: []string{"h2"}}}, "uuid")
	tls, ok := out[0]["tls"].(map[string]any)
	if !ok || tls["enabled"] != true || tls["server_name"] != "sni.example.org" || tls["alpn"] == nil {
		t.Fatalf("TLS omitted: %v", out)
	}
	dns := singDNSServer("https://dns.example.org:8443/custom", "dns", "proxy")
	if dns["server_port"] != 8443 || dns["path"] != "/custom" {
		t.Fatalf("wrong sing-box DNS fields: %v", dns)
	}
}

func TestKaringUnlimitedWithoutHWIDKeepsRouting(t *testing.T) {
	e := newEnv(t)
	if err := e.svc.SetSetting(e.ctx, actor, service.SettingHWIDLimit, "0"); err != nil {
		t.Fatal(err)
	}
	rec := e.get(e.path(), "karing/1.2.25.2802", "")
	if rec.Code != 200 || !json.Valid(rec.Body.Bytes()) || !strings.Contains(rec.Body.String(), `"rule_set"`) {
		t.Fatalf("unlimited Karing lost its routing without HWID: %d %s", rec.Code, rec.Body.String())
	}
	devices, _ := e.svc.Devices(e.ctx, e.user.ID)
	if len(devices) != 0 {
		t.Fatal("invented a device for a request without HWID")
	}
}
