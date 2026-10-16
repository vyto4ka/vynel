package service

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/vyto4ka/vynel/internal/node/firewall"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

// nginxNode marks a node's agent as able to run nginx, as its Hello would.
func (f *fixture) nginxNode(t *testing.T, n *store.Node) {
	t.Helper()
	if err := store.SetNodeFacts(f.ctx, f.s.st.DB, n.ID, store.NodeFacts{NginxVersion: "1.24.0", Capabilities: []string{CapNginx}}); err != nil {
		t.Fatal(err)
	}
}

func inboundByTag(t *testing.T, cfg []byte, tag string) map[string]any {
	t.Helper()
	var c struct {
		Inbounds []map[string]any `json:"inbounds"`
	}
	if err := json.Unmarshal(cfg, &c); err != nil {
		t.Fatal(err)
	}
	for _, in := range c.Inbounds {
		if in["tag"] == tag {
			return in
		}
	}
	t.Fatalf("no inbound %s", tag)
	return nil
}

func contains(t *testing.T, s string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(s, w) {
			t.Fatalf("missing %q in:\n%s", w, s)
		}
	}
}

// Reality alone keeps :443; nginx is its target on loopback, as Caddy was.
func TestNginxRealityOnly(t *testing.T) {
	f := newFixture(t)
	n, _ := f.aio(t, "")
	f.nginxNode(t, n)
	ds := must(f.s.DesiredState(f.ctx, n.ID))
	if len(ds.Problems) > 0 || ds.Caddy != nil || ds.Nginx == "" {
		t.Fatalf("problems %v caddy %d nginx %d", ds.Problems, len(ds.Caddy), len(ds.Nginx))
	}
	if strings.Contains(ds.Nginx, "ssl_preread") {
		t.Fatal("no SNI router for Reality alone")
	}
	contains(t, ds.Nginx, "listen 127.0.0.1:8443 ssl proxy_protocol http2;", "server_name nl.example.com;")
	in := inboundByTag(t, ds.Config, "VLESS_NL")
	if in["port"] != float64(443) || in["listen"] != "0.0.0.0" {
		t.Fatalf("Reality moved: %v %v", in["listen"], in["port"])
	}
}

// One domain for Reality, the panel and subscriptions (the minimal all-in-one): every name the
// panel's site has is a Reality name, so Reality keeps :443 and the site sits on its target.
func TestNginxOneDomainNoRouter(t *testing.T) {
	f := newFixture(t)
	n, _ := f.aio(t, "nl.example.com")
	f.nginxNode(t, n)
	if err := f.s.EnsureWebDefaults(f.ctx); err != nil {
		t.Fatal(err)
	}
	ds := must(f.s.DesiredState(f.ctx, n.ID))
	if len(ds.Problems) > 0 {
		t.Fatal(ds.Problems)
	}
	if strings.Contains(ds.Nginx, "ssl_preread") || strings.Contains(ds.Nginx, "\nstream {") {
		t.Fatalf("SNI router for names Reality already has:\n%s", ds.Nginx)
	}
	contains(t, ds.Nginx, "listen 127.0.0.1:8443 ssl proxy_protocol http2;", "server_name nl.example.com;", "location ^~ /s/ {")
	in := inboundByTag(t, ds.Config, "VLESS_NL")
	if in["port"] != float64(443) || in["listen"] != "0.0.0.0" {
		t.Fatalf("Reality moved: %v %v", in["listen"], in["port"])
	}
	// The firewall opens 443 for Reality and 80 for nginx; the target on loopback stays closed.
	open := map[string]bool{}
	for _, p := range append(firewall.XrayPorts(ds.Config), firewall.NginxPorts(ds.Nginx)...) {
		open[fmt.Sprintf("%d/%s", p.Port, p.Proto)] = true
	}
	if !open["443/tcp"] || !open["80/tcp"] || open["8443/tcp"] {
		t.Fatalf("firewall ports %v", open)
	}
}

// Reality with extra server names (e.g. names an operator's whitelist lets through): a site on
// one of them needs no router; a site on another name gets one that sends every Reality name to
// Xray.
func TestNginxRealityExtraNames(t *testing.T) {
	for _, tc := range []struct {
		sub    string
		router bool
	}{{"nl.example.com", false}, {"sub.example.com", true}} {
		f := newFixture(t)
		n, prof := f.aio(t, tc.sub)
		f.nginxNode(t, n)
		if err := f.s.EnsureWebDefaults(f.ctx); err != nil {
			t.Fatal(err)
		}
		must(f.s.UpdateProfile(f.ctx, ActorCLI, prof.ID, ProfileInput{Name: prof.Name, Override: map[string]any{
			"streamSettings": map[string]any{"realitySettings": map[string]any{"serverNames": []any{"nl.example.com", "wl.example.ru"}}}}}))
		ds := must(f.s.DesiredState(f.ctx, n.ID))
		if len(ds.Problems) > 0 {
			t.Fatal(ds.Problems)
		}
		in := inboundByTag(t, ds.Config, "VLESS_NL")
		names := in["streamSettings"].(map[string]any)["realitySettings"].(map[string]any)["serverNames"]
		if fmt.Sprint(names) != "[nl.example.com wl.example.ru]" {
			t.Fatalf("%s: server names %v", tc.sub, names)
		}
		if got := strings.Contains(ds.Nginx, "ssl_preread on;"); got != tc.router {
			t.Fatalf("%s: router %v, want %v", tc.sub, got, tc.router)
		}
		if tc.router {
			contains(t, ds.Nginx, "nl.example.com 127.0.0.1:10443;", "wl.example.ru 127.0.0.1:10443;", "default 127.0.0.1:8443;")
		} else if in["port"] != float64(443) {
			t.Fatalf("Reality moved to %v", in["port"])
		}
	}
}

// Reality and the panel's subscription site on one IP: nginx takes :443 and routes by SNI.
func TestNginxAllInOneSNIRouter(t *testing.T) {
	f := newFixture(t)
	n, _ := f.aio(t, "sub.example.com")
	f.nginxNode(t, n)
	ds := must(f.s.DesiredState(f.ctx, n.ID))
	if len(ds.Problems) > 0 {
		t.Fatal(ds.Problems)
	}
	contains(t, ds.Nginx, "ssl_preread on;", "nl.example.com 127.0.0.1:10443;", "default 127.0.0.1:8443;",
		"server_name sub.example.com;", "listen 127.0.0.1:8443 ssl proxy_protocol http2;")
	in := inboundByTag(t, ds.Config, "VLESS_NL")
	ss := in["streamSettings"].(map[string]any)
	if in["listen"] != "127.0.0.1" || in["port"] != float64(10443) || ss["sockopt"].(map[string]any)["acceptProxyProtocol"] != true {
		t.Fatalf("Reality not behind the router: %v", in)
	}
	if rs := ss["realitySettings"].(map[string]any); rs["xver"] != float64(2) {
		t.Fatalf("xver %v", rs["xver"])
	}
	// The firewall opens nginx's :443 and :80; Xray has no public TCP port left.
	ports := map[int]bool{}
	for _, p := range append(firewall.NginxPorts(ds.Nginx), firewall.XrayPorts(ds.Config)...) {
		if p.Proto == "tcp" {
			ports[p.Port] = true
		}
	}
	if !ports[443] || !ports[80] || len(ports) != 2 {
		t.Fatalf("ports %v", ports)
	}
	// The subscription link still points at :443.
	nis := must(store.ListNodeInbounds(f.ctx, f.s.st.DB, n.ID, 0))
	if h := must(f.s.HostFor(f.ctx, nis[0])); h.Port != 443 {
		t.Fatalf("host port %d", h.Port)
	}
}

// An old agent (no nginx capability) keeps getting the Caddy layout.
func TestNginxOldAgentGetsCaddy(t *testing.T) {
	f := newFixture(t)
	n, _ := f.aio(t, "sub.example.com")
	ds := must(f.s.DesiredState(f.ctx, n.ID))
	if ds.Caddy == nil || ds.Nginx != "" {
		t.Fatalf("caddy %d nginx %d", len(ds.Caddy), len(ds.Nginx))
	}
	if in := inboundByTag(t, ds.Config, "VLESS_NL"); in["port"] != float64(443) {
		t.Fatal("Caddy layout must not move Reality")
	}
}

const cdnTemplate = `id: my-cdn
version: 1
title: CDN
tag_pattern: CDNB_${NODE_CODE}
protocol: vless
client:
  flow: ""
variables:
  - { name: ORIGIN_DOMAIN, scope: node, source: input, default_from: node.domain, validate: domain }
  - { name: CDN_DOMAIN, scope: node, source: input, validate: domain }
  - { name: XRAY_PORT, scope: profile, source: const, default: 8086, validate: port }
  - { name: UPLINK_PATH, scope: profile, source: const, default: /upload, validate: path }
xray_inbound: |
  {"tag": "${TAG}", "port": "${XRAY_PORT}", "listen": "127.0.0.1", "protocol": "vless",
   "settings": {"clients": [], "decryption": "none"},
   "streamSettings": {"network": "xhttp", "xhttpSettings": {"mode": "packet-up", "path": "${UPLINK_PATH}"}}}
host:
  address: ${CDN_DOMAIN}
  port: 443
nginx:
  role: reverse_proxy_xhttp
  domain: ${ORIGIN_DOMAIN}
  server_names: [ "${CDN_DOMAIN}" ]
  path: ${UPLINK_PATH}
  upstream: 127.0.0.1:${XRAY_PORT}
  location: |
    proxy_pass http://127.0.0.1:${XRAY_PORT};
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_buffering off;
    proxy_request_buffering off;
    client_max_body_size 0;
  server: |
    large_client_header_buffers 8 64k;
`

// A CDN origin behind Reality: the SNI router, the CDN name on the origin site and the
// template's own nginx directives, as in the VK CDN guide.
func TestNginxCDNOrigin(t *testing.T) {
	f := newFixture(t)
	n, _ := f.aio(t, "")
	f.nginxNode(t, n)
	must(f.s.SaveTemplate(f.ctx, ActorCLI, "", cdnTemplate))
	p := must(f.s.CreateProfile(f.ctx, ActorCLI, ProfileInput{Name: "CDN", TemplateID: "my-cdn"}))
	must(f.s.AttachProfile(f.ctx, ActorCLI, AttachInput{NodeID: n.ID, ProfileID: p.ID,
		Values: map[string]any{"CDN_DOMAIN": "cdn.example.com", "ORIGIN_DOMAIN": "origin.example.com"}}))
	ds := must(f.s.DesiredState(f.ctx, n.ID))
	if len(ds.Problems) > 0 {
		t.Fatal(ds.Problems)
	}
	contains(t, ds.Nginx, "ssl_preread on;", "server_name origin.example.com cdn.example.com;", "location ^~ /upload {",
		"proxy_pass http://127.0.0.1:8086;", "large_client_header_buffers 8 64k;", "server_name nl.example.com;")
	if strings.Contains(ds.Nginx, "cdn.example.com/fullchain.pem") {
		t.Fatal("the CDN name must not get a certificate of its own")
	}
}

// Template checks run nginx -t: a typo in the template's directives is caught in the editor.
func TestNginxTemplateCheck(t *testing.T) {
	if _, err := os.Stat(NginxCheckBin); err != nil {
		t.Skip("nginx not installed")
	}
	f := newFixture(t)
	if _, _, err := f.s.CheckTemplate(cdnTemplate); err != nil {
		t.Fatalf("good template: %v", err)
	}
	bad := strings.Replace(cdnTemplate, "proxy_buffering off;", "proxy_bufering off;", 1)
	if _, _, err := f.s.CheckTemplate(bad); err == nil || !strings.Contains(err.Error(), "proxy_bufering") {
		t.Fatalf("typo not caught: %v", err)
	}
	inc := strings.Replace(cdnTemplate, "client_max_body_size 0;", "include /etc/passwd;", 1)
	if _, _, err := f.s.CheckTemplate(inc); err == nil || !strings.Contains(err.Error(), "include") {
		t.Fatalf("include accepted: %v", err)
	}
	// The old section name still works.
	old := strings.Replace(cdnTemplate, "\nnginx:\n", "\ncaddy:\n", 1)
	if tpl, _, err := f.s.CheckTemplate(old); err != nil || tpl.Web() == nil {
		t.Fatalf("caddy: alias: %v", err)
	}
	if _, err := xrayconf.ParseTemplate([]byte(old + "nginx:\n  role: selfsteal\n")); err == nil {
		t.Fatal("both sections accepted")
	}
}

// Session paths: nginx sends the whole panel domain to the panel, which shows the decoy itself.
func TestNginxSessionPathsRoute(t *testing.T) {
	f := newFixture(t)
	n, _ := f.aio(t, "sub.example.com")
	f.nginxNode(t, n)
	if err := f.s.EnsureWebDefaults(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.s.SetLoginMode(f.ctx, ActorCLI, ModeLink); err != nil {
		t.Fatal(err)
	}
	if err := f.s.SetSessionPaths(f.ctx, ActorCLI, true); err != nil {
		t.Fatal(err)
	}
	ds := must(f.s.DesiredState(f.ctx, n.ID))
	i := strings.Index(ds.Nginx, "server_name sub.example.com;")
	if i < 0 {
		t.Fatal(ds.Nginx)
	}
	site := ds.Nginx[i:]
	site = site[:strings.Index(site, "\n    }\n")]
	contains(t, site, "location ^~ /s/ {", "location / {", "proxy_pass http://127.0.0.1:2097;")
	if strings.Contains(site, "@@DECOY@@") {
		t.Fatalf("the decoy must come from the panel now:\n%s", site)
	}
}
