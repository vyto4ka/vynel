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

	"gopkg.in/yaml.v3"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
)

func TestMihomoGroupNameReservationAndFeatures(t *testing.T) {
	hosts := []service.Host{
		{
			Tag: "n1", Remark: "VPN", Protocol: "vless", Network: "tcp", Security: "reality",
			Address: "nl.example.com", Port: 443, SNI: "nl.example.com", Fingerprint: "firefox",
			PublicKey: "qGPTy8EZokn3hWp6hKBQ0MVvEuLRJCcv5UdWeP4TVhI", ShortID: "ab12",
			Flow: "xtls-rprx-vision", MLKEM: true,
		},
		{
			Tag: "n2", Remark: "Auto", Protocol: "vless", Network: "xhttp", Security: "tls",
			Address: "tls.example.com", Port: 443, SNI: "tls.example.com", HostHeader: "tls.example.com",
			Path: "/sync", Mode: "stream-up", Fingerprint: "chrome", Mihomo: true,
			Extra: map[string]any{
				"xPaddingBytes": "100-500",
				"downloadSettings": map[string]any{
					"address":  "dl.example.com",
					"port":     443,
					"security": "tls",
					"tlsSettings": map[string]any{
						"serverName":  "dl.example.com",
						"fingerprint": "chrome",
					},
					"xhttpSettings": map[string]any{
						"path": "/dl",
						"host": "dl.example.com",
					},
				},
			},
		},
	}
	r := service.DefaultClientRouting()
	r.DNSProxy = []string{"https://1.1.1.1/dns-query#h3=true", "https://8.8.8.8/dns-query"}

	raw, err := Mihomo(hosts, "11111111-2222-4333-8444-555555555555", r)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["unified-delay"] != true || doc["tcp-concurrent"] != true {
		t.Fatalf("missing unified-delay / tcp-concurrent: %v", doc)
	}
	proxies := doc["proxies"].([]any)
	p0 := proxies[0].(map[string]any)
	p1 := proxies[1].(map[string]any)
	if p0["name"] == "VPN" || p1["name"] == "Auto" {
		t.Fatalf("reserved group names were not renamed: %v, %v", p0["name"], p1["name"])
	}
	ro := p0["reality-opts"].(map[string]any)
	if ro["support-x25519mlkem768"] != true {
		t.Fatalf("missing support-x25519mlkem768: %v", ro)
	}
	xo := p1["xhttp-opts"].(map[string]any)
	ds, ok := xo["download-settings"].(map[string]any)
	if !ok || ds["server"] != "dl.example.com" || ds["path"] != "/dl/" || ds["tls"] != true {
		t.Fatalf("bad download-settings: %v", xo)
	}
	dns := doc["dns"].(map[string]any)
	ns := dns["nameserver"].([]any)
	if ns[0] != "https://1.1.1.1/dns-query#h3=true&VPN" || ns[1] != "https://8.8.8.8/dns-query#VPN" {
		t.Fatalf("bad mihomo DNS fragment join: %v", ns)
	}

	// Verify MihomoFromTemplate also reserves group and proxy names from the template.
	tpl := "proxies:\n  - name: Static\n    type: direct\nproxy-groups:\n  - name: VPN\n    type: select\n"
	out, err := MihomoFromTemplate(hosts[:1], "11111111-2222-4333-8444-555555555555", tpl)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "name: VPN 2") {
		t.Fatalf("MihomoFromTemplate did not rename conflicting proxy:\n%s", out)
	}
}

func TestKaringXHTTPAndVersions(t *testing.T) {
	const id = "11111111-2222-4333-8444-555555555555"
	cookieHost := service.Host{
		Tag: "xh1", Remark: "XHTTP Cookie", Protocol: "vless", Network: "xhttp", Security: "tls",
		Address: "xhttp.example.com", Port: 443, SNI: "xhttp.example.com", HostHeader: "xhttp.example.com",
		Path: "/assets/sync", Mode: "stream-up", Fingerprint: "randomized", ALPN: []string{"h2", "http/1.1"},
		Extra: map[string]any{
			"sessionPlacement":     "cookie",
			"sessionKey":           "media_sid",
			"seqPlacement":         "query",
			"seqKey":               "offset",
			"xPaddingObfsMode":     true,
			"xPaddingPlacement":    "query",
			"xPaddingKey":          "cb",
			"xPaddingMethod":       "repeat-x",
			"xPaddingBytes":        "100-1000",
			"uplinkHTTPMethod":     "POST",
			"uplinkDataPlacement":  "body",
			"scMaxEachPostBytes":   "65536-262144",
			"scMinPostsIntervalMs": "5-15",
			"noGRPCHeader":         true,
			"xmux": map[string]any{
				"maxConcurrency":   "4-8",
				"maxConnections":   "2", // must be dropped when maxConcurrency is present
				"hMaxRequestTimes": "600-900",
				"hMaxReusableSecs": "600-1800",
				"hKeepAlivePeriod": 30,
			},
		},
	}
	raw, err := Karing([]service.Host{cookieHost}, id, "https://sub.example.com/s/tok", service.DefaultClientRouting())
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	var xh map[string]any
	for _, ob := range cfg.Outbounds {
		if ob["type"] == "vless" {
			xh = ob
		}
	}
	if xh == nil {
		t.Fatalf("missing vless outbound: %s", raw)
	}
	tls := xh["tls"].(map[string]any)
	utls := tls["utls"].(map[string]any)
	if utls["fingerprint"] != "random" {
		t.Fatalf("fingerprint not normalized: %v", utls)
	}
	tr := xh["transport"].(map[string]any)
	if tr["type"] != "xhttp" || tr["path"] != "/assets/sync/" || tr["session_placement"] != "cookie" || tr["x_padding_obfs_mode"] != true {
		t.Fatalf("bad karing xhttp transport: %v", tr)
	}
	xm := tr["xmux"].(map[string]any)
	if xm["max_concurrency"] != "4-8" || xm["max_connections"] != nil {
		t.Fatalf("bad xmux mutual exclusion: %v", xm)
	}
	if !karingOlderThan("karing/1.2.25.2802", 1, 2, 26) || karingOlderThan("karing/1.2.26.2934", 1, 2, 26) || karingOlderThan("Karing", 1, 2, 26) {
		t.Fatal("karingOlderThan mismatch")
	}
	if !karingNeeds1226(cookieHost) {
		t.Fatal("cookieHost should require Karing 1.2.26+")
	}
	// External XHTTP link must work for Karing but stay rejected by upstream singBoxLink.
	extXHTTP := "vless://" + id + "@x.example.org:443?type=xhttp&security=tls&sni=x.example.org&path=%2Fsync&mode=auto#ExtXHTTP"
	if _, ok := singBoxLink(extXHTTP); ok {
		t.Fatal("upstream singBoxLink must reject XHTTP")
	}
	if ob, ok := karingLink(extXHTTP); !ok || ob["transport"].(map[string]any)["type"] != "xhttp" {
		t.Fatalf("karingLink failed on external XHTTP: %v %v", ok, ob)
	}
}

func TestV2RayTunRoutingAndFragment(t *testing.T) {
	e := newEnv(t)
	r := service.DefaultClientRouting()
	r.BlockAds = true
	r.Fragment.Enabled = true
	r.Direct = []string{"example.com", "198.51.100.0/24"}
	if err := e.svc.SetClientRouting(e.ctx, actor, &r); err != nil {
		t.Fatal(err)
	}
	w := e.get(e.path(), "v2RayTun/5.1.0", "vt-1")
	hdr := w.Header().Get("Routing")
	if hdr == "" || strings.HasPrefix(hdr, "happ://") {
		t.Fatalf("bad v2RayTun Routing header: %q", hdr)
	}
	raw, err := base64.StdEncoding.DecodeString(hdr)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		ID             string           `json:"id"`
		Name           string           `json:"name"`
		DomainStrategy string           `json:"domainStrategy"`
		DomainMatcher  string           `json:"domainMatcher"`
		Balancers      []any            `json:"balancers"`
		Rules          []map[string]any `json:"rules"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("invalid v2RayTun routing JSON: %v\n%s", err, raw)
	}
	if doc.ID == "" || doc.DomainStrategy != "IPIfNonMatch" || doc.DomainMatcher != "hybrid" || len(doc.Rules) < 4 {
		t.Fatalf("unexpected v2RayTun routing doc: %+v", doc)
	}
	for _, rule := range doc.Rules {
		if rule["domain"] != nil && rule["ip"] != nil {
			t.Fatalf("domain and ip must not be ANDed in one Xray rule: %v", rule)
		}
		if rule["id"] == "" || rule["__name__"] == "" || rule["type"] != "field" {
			t.Fatalf("incomplete v2RayTun rule: %v", rule)
		}
	}
	// v2RayTun gets fm= parameter on vless:// links; v2rayNG does not.
	vtLinks := decodeB64Links(t, w.Body.String())
	if len(vtLinks) == 0 || !strings.Contains(vtLinks[0], "fm=") {
		t.Fatalf("v2RayTun link missing fm parameter: %v", vtLinks)
	}
	ngLinks := decodeB64Links(t, e.get(e.path(), "v2rayNG/1.9", "ng-1").Body.String())
	if len(ngLinks) == 0 || strings.Contains(ngLinks[0], "fm=") {
		t.Fatalf("v2rayNG link must not contain fm parameter: %v", ngLinks)
	}
}

func TestHappFragmentHeadersAndDoHBootstrap(t *testing.T) {
	e := newEnv(t)
	r := service.DefaultClientRouting()
	r.Fragment.Enabled = true
	r.Fragment.Packets = "tlshello"
	r.Fragment.Length = "10-30"
	r.Fragment.Delay = "5-15"
	r.DNSDirect = []string{"https://common.dot.dns.yandex.net/dns-query"}
	r.DNSProxy = []string{"https://cloudflare-dns.com/dns-query"}
	if err := e.svc.SetClientRouting(e.ctx, actor, &r); err != nil {
		t.Fatal(err)
	}
	w := e.get(e.path(), "Happ/4.8.0", "hp-1")
	if w.Header().Get("fragmentation-enable") != "true" || w.Header().Get("fragmentation-interval") != "5-15" || w.Header().Get("fragmentation-length") != "10-30" {
		t.Fatalf("missing Happ fragmentation headers: %v", w.Header())
	}
	p := decodeHapp(t, w.Header().Get("Routing"))
	if p["DomesticDNSIP"] != "77.88.8.8" || p["RemoteDNSIP"] != "1.1.1.1" || p["RouteOrder"] != "block-proxy-direct" {
		t.Fatalf("unexpected Happ routing DNS/order: %v", p)
	}
	hosts, _ := p["DnsHosts"].(map[string]any)
	if hosts["cloudflare-dns.com"] != "1.1.1.1" || hosts["common.dot.dns.yandex.net"] != "77.88.8.8" {
		t.Fatalf("missing DnsHosts bootstrap entries: %v", hosts)
	}
}

func TestXHTTPPathSyncAndPQVPreservation(t *testing.T) {
	e := newEnv(t)
	nis, err := e.svc.UserInbounds(e.ctx, e.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	var xhttpNI *store.NodeInbound
	for _, ni := range nis {
		h, err := e.svc.HostFor(e.ctx, ni)
		if err == nil && h.Network == "xhttp" {
			xhttpNI = ni
			break
		}
	}
	if xhttpNI == nil {
		t.Fatal("no xhttp inbound in test env")
	}
	// Editing profile override xhttpSettings.path must sync to HostFor.
	if _, err := e.svc.UpdateProfile(e.ctx, actor, xhttpNI.ProfileID, service.ProfileInput{
		Override: map[string]any{
			"streamSettings": map[string]any{
				"xhttpSettings": map[string]any{"path": "/renamed"},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	xhttpNI, _ = e.svc.NodeInboundByTag(e.ctx, xhttpNI.Tag)
	h, err := e.svc.HostFor(e.ctx, xhttpNI)
	if err != nil {
		t.Fatal(err)
	}
	if h.Path != "/renamed/" {
		t.Fatalf("HostFor path = %q, want /renamed/", h.Path)
	}
	// Explicit SetHostOverride("path") wins over inbound path.
	if err := e.svc.SetHostOverride(e.ctx, actor, xhttpNI.ID, map[string]any{"path": "/cdn-edge/"}); err != nil {
		t.Fatal(err)
	}
	xhttpNI, _ = e.svc.NodeInboundByTag(e.ctx, xhttpNI.Tag)
	h, err = e.svc.HostFor(e.ctx, xhttpNI)
	if err != nil || h.Path != "/cdn-edge/" {
		t.Fatalf("SetHostOverride path = %q, err=%v", h.Path, err)
	}

	// External VLESS link with pqv and spx must keep both on re-serialization and encode spaces as %20.
	ext := "vless://11111111-2222-4333-8444-555555555555@pq.example.com:443?encryption=none&type=xhttp&security=reality&sni=pq.example.com&fp=chrome&pbk=qGPTy8EZokn3hWp6hKBQ0MVvEuLRJCcv5UdWeP4TVhI&sid=ab12&pqv=MLDSA65KEY&spx=%2Fsp&path=%2F&extra=%7B%22headers%22%3A%7B%22User-Agent%22%3A%22My%20App%22%7D%7D#PQ"
	parsed := service.ParseLinks(ext)
	if len(parsed) != 1 || parsed[0].PQV != "MLDSA65KEY" || parsed[0].SpiderX != "/sp" {
		t.Fatalf("ParseLinks lost pqv/spx: %+v", parsed)
	}
	rebuilt := Link(parsed[0], parsed[0].UUID)
	u, err := url.Parse(rebuilt)
	if err != nil || u.Query().Get("pqv") != "MLDSA65KEY" || u.Query().Get("spx") != "/sp" {
		t.Fatalf("HostLink lost pqv/spx: %s", rebuilt)
	}
	if strings.Contains(rebuilt, "+") {
		t.Fatalf("HostLink encoded space as '+' instead of %%20: %s", rebuilt)
	}
	if FormatMihomo.Supports(parsed[0]) || FormatKaring.Supports(parsed[0]) {
		t.Fatal("PQV host must not be downgraded to Mihomo or Karing")
	}
}

func TestXrayDNSNoLoopAndNoSocksHardcode(t *testing.T) {
	r := service.DefaultClientRouting()
	dns := xrayDNS(r)
	if dns["tag"] != "dns-in" {
		t.Fatalf("xrayDNS missing tag dns-in: %v", dns)
	}
	rules := xrayRules(r)
	b, _ := json.Marshal(rules)
	if strings.Contains(string(b), `"socks"`) {
		t.Fatalf("xrayRules still hardcodes socks inboundTag: %s", b)
	}
	if !strings.Contains(string(b), `"dns-in"`) || !strings.Contains(string(b), `"dns-out"`) {
		t.Fatalf("xrayRules missing dns-in/dns-out rules: %s", b)
	}
}

// TestClientCoresValidateGeneratedConfigs validates generated Vynel subscriptions against
// real client core binaries when their paths are provided via env vars:
// XRAY_BIN, MIHOMO_BIN, SINGBOX_BIN, KARING_SINGBOX_BIN.
func TestClientCoresValidateGeneratedConfigs(t *testing.T) {
	xrayBin := os.Getenv("XRAY_BIN")
	mihomoBin := os.Getenv("MIHOMO_BIN")
	singboxBin := os.Getenv("SINGBOX_BIN")
	karingBin := os.Getenv("KARING_SINGBOX_BIN")
	if xrayBin == "" && mihomoBin == "" && singboxBin == "" && karingBin == "" {
		t.Skip("no client core binaries configured in env")
	}

	e := newEnv(t)
	// Attach the new vless-xhttp-tls template too.
	prof, err := e.svc.CreateProfile(e.ctx, actor, service.ProfileInput{Name: "XHTTP TLS", TemplateID: "vless-xhttp-tls"})
	if err != nil {
		t.Fatal(err)
	}
	mainGroup, _ := e.svc.GroupByName(e.ctx, "Основная")
	if err := e.svc.GrantAccess(e.ctx, actor, mainGroup.ID, store.AccessProfile, prof.ID); err != nil {
		t.Fatal(err)
	}
	nodes, _ := e.svc.Nodes(e.ctx)
	if _, err := e.svc.AttachProfile(e.ctx, actor, service.AttachInput{NodeID: nodes[0].ID, ProfileID: prof.ID, PortOverride: 8444}); err != nil {
		t.Fatal(err)
	}
	r := service.DefaultClientRouting()
	r.Fragment.Enabled = true
	r.BlockAds = true
	if err := e.svc.SetClientRouting(e.ctx, actor, &r); err != nil {
		t.Fatal(err)
	}

	if xrayBin != "" {
		rec := e.get(e.path()+"/json", "KeqDroid/0.38.4", "kd-1")
		var configs []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &configs); err != nil || len(configs) == 0 {
			t.Fatalf("bad xray json: %v", err)
		}
		for i, cfg := range configs {
			raw, _ := json.Marshal(cfg)
			f := filepath.Join(t.TempDir(), "xray.json")
			_ = os.WriteFile(f, raw, 0600)
			if out, err := exec.Command(xrayBin, "run", "-test", "-c", f).CombinedOutput(); err != nil {
				t.Fatalf("xray rejected config #%d: %v\n%s\n%s", i, err, out, raw)
			}
		}
	}

	if mihomoBin != "" {
		// Rename one host to "VPN" to prove Mihomo accepts the profile without ProxyGroup loop.
		nis, _ := e.svc.UserInbounds(e.ctx, e.user.ID)
		_ = e.svc.SetHostOverride(e.ctx, actor, nis[0].ID, map[string]any{"remark": "VPN"})
		rec := e.get(e.path(), "FlClashX/0.5.8 core/1.19.34", "")
		dir := t.TempDir()
		f := filepath.Join(dir, "config.yaml")
		_ = os.WriteFile(f, rec.Body.Bytes(), 0600)
		// Strip geoip/geosite rules only if local mmdb/geo files are absent in temp home dir.
		cmd := exec.Command(mihomoBin, "-d", dir, "-t", "-f", f)
		if out, err := cmd.CombinedOutput(); err != nil {
			// If mihomo failed only due to missing geoip/geosite files in empty temp dir, test proxy/group/dns parsing without geo rules.
			if strings.Contains(string(out), "geoip") || strings.Contains(string(out), "geosite") || strings.Contains(string(out), "MMDB") {
				var doc map[string]any
				_ = yaml.Unmarshal(rec.Body.Bytes(), &doc)
				doc["rules"] = []string{"DOMAIN-SUFFIX,ru,DIRECT", "MATCH,VPN"}
				if dns, ok := doc["dns"].(map[string]any); ok {
					delete(dns, "nameserver-policy")
				}
				b, _ := yaml.Marshal(doc)
				_ = os.WriteFile(f, b, 0600)
				if out2, err2 := exec.Command(mihomoBin, "-d", dir, "-t", "-f", f).CombinedOutput(); err2 != nil {
					t.Fatalf("mihomo rejected YAML: %v\n%s\n%s", err2, out2, b)
				}
			} else {
				t.Fatalf("mihomo rejected YAML: %v\n%s\n%s", err, out, rec.Body.String())
			}
		}
	}

	if singboxBin != "" {
		rec := e.get(e.path(), "SFA/1.12.0", "")
		f := filepath.Join(t.TempDir(), "singbox.json")
		_ = os.WriteFile(f, rec.Body.Bytes(), 0600)
		if out, err := exec.Command(singboxBin, "check", "-c", f).CombinedOutput(); err != nil {
			t.Fatalf("sing-box rejected config: %v\n%s\n%s", err, out, rec.Body.String())
		}
	}

	if karingBin != "" {
		rec := e.get(e.path(), "karing/1.2.26.2934", "kr-1")
		var doc map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		// Karing's importer converts legacy "block" outbound and rule_set before running sing-box;
		// validate every generated proxy outbound (including VLESS + XHTTP) directly against KaringX/sing-box.
		var proxies []any
		foundXHTTP := false
		for _, item := range doc["outbounds"].([]any) {
			ob := item.(map[string]any)
			if ob["type"] == "vless" || ob["type"] == "hysteria2" {
				if tr, ok := ob["transport"].(map[string]any); ok && tr["type"] == "xhttp" {
					foundXHTTP = true
				}
				proxies = append(proxies, ob)
			}
		}
		if !foundXHTTP {
			t.Fatalf("Karing 1.2.26 response did not contain any XHTTP outbound:\n%s", rec.Body.String())
		}
		checkDoc, _ := json.Marshal(map[string]any{"outbounds": proxies})
		f := filepath.Join(t.TempDir(), "karing.json")
		_ = os.WriteFile(f, checkDoc, 0600)
		if out, err := exec.Command(karingBin, "check", "-c", f).CombinedOutput(); err != nil {
			t.Fatalf("KaringX/sing-box rejected XHTTP outbounds: %v\n%s\n%s", err, out, checkDoc)
		}
	}
}
