package subscription

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xray/xraytest"
)

// Regression: the old KeqDroid response contained only the two local servers, because all
// external transports not understood by ParseLinks were filtered out of Xray subscriptions.
func TestKeqDroidMixedExternalServers(t *testing.T) {
	e := newEnv(t)
	external := []string{
		"vless://ext@ws.example.org:443?type=ws&security=tls&host=cdn.example.org&path=%2Fws#WS",
		"vless://ext@grpc.example.org:443?type=grpc&security=tls&serviceName=svc#GRPC",
		"trojan://p%40ss@trojan.example.org:443?security=tls#Trojan",
		"vmess://eyJ2IjoiMiIsInBzIjoiVm1lc3MiLCJhZGQiOiJ2bWVzcy5leGFtcGxlLm9yZyIsInBvcnQiOiI0NDMiLCJpZCI6IjExMTExMTExLTIyMjItNDMzMy04NDQ0LTU1NTU1NTU1NTU1NSIsIm5ldCI6IndzIiwidGxzIjoidGxzIn0=",
		"ss://YWVzLTEyOC1nY206cGFzcw@ss.example.org:443#SS",
		"hysteria2://secret@hy.example.org:443?insecure=1&obfs=salamander&obfs-password=test#HY",
		"tuic://user:pass@tuic.example.org:443#TUIC",
		"anytls://pass@anytls.example.org:443#AnyTLS",
		"socks://user:pass@socks.example.org:1080#SOCKS",
		"vless://ext@http.example.org:443?type=httpupgrade&security=tls&path=%2F#HTTPUpgrade",
	}
	ext, err := e.svc.CreateExternalSub(e.ctx, actor, service.ExternalSubInput{Name: "friend", URL: strings.Join(external, "\n"), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.PutGrant(e.ctx, actor, service.GrantInput{UserID: e.user.ID, Kind: store.GrantExternal, RefID: ext.ID}); err != nil {
		t.Fatal(err)
	}
	r := service.DefaultClientRouting()
	r.Fragment.Enabled = true
	r.Proxy = []string{"domain:youtube.com"}
	if err := e.svc.SetClientRouting(e.ctx, actor, &r); err != nil {
		t.Fatal(err)
	}
	for _, ua := range []string{"KeqDroid/0.34.1", "keqdis/0.34.0"} {
		rec := e.get(e.path(), ua, "hw-1")
		if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
			t.Fatalf("%s: status %d type %s", ua, rec.Code, rec.Header().Get("Content-Type"))
		}
		lines := strings.Split(rec.Body.String(), "\n")
		if len(lines) != 12 {
			t.Fatalf("%s: got %d entries, want 12", ua, len(lines))
		}
		for _, link := range external {
			if !slices.Contains(lines, link) {
				t.Fatalf("lost/rewrote external link: %s", link)
			}
		}
		for _, line := range lines[:2] {
			var cfg map[string]any
			if err := json.Unmarshal([]byte(line), &cfg); err != nil {
				t.Fatal(err)
			}
			if cfg["dns"] == nil || cfg["routing"] == nil || !strings.Contains(line, `"finalmask"`) || !strings.Contains(line, e.user.UUID) {
				t.Fatal("lost full local client config")
			}
		}
		if rec.Header().Get("Routing") == "" {
			t.Fatal("raw links need provider routing")
		}
	}
	for _, ua := range []string{"Happ/4.1", "INCY/3.6.5"} {
		ls := links(t, e.get(e.path(), ua, "hw-1"))
		if len(ls) != 12 {
			t.Fatalf("%s: got %d links", ua, len(ls))
		}
		for _, link := range external {
			if !slices.Contains(ls, link) {
				t.Fatal("external link lost in base64")
			}
		}
	}
	// Explicit JSON for other clients remains valid JSON, and the HWID refusal never
	// leaks either external credentials or local credentials from the mixed payload.
	if !json.Valid(e.get(e.path()+"/json", "other", "hw-1").Body.Bytes()) {
		t.Fatal("other clients' JSON changed")
	}
	if !json.Valid(e.get(e.path()+"/json", "KeqDroid/0.34.1", "hw-1").Body.Bytes()) {
		t.Fatal("explicit JSON must stay strict for KeqDroid too")
	}
	e.get(e.path(), "KeqDroid/0.34.1", "hw-3") // second allowed device
	refused := e.get(e.path(), "KeqDroid/0.34.1", "hw-4")
	if !json.Valid(refused.Body.Bytes()) || strings.Contains(refused.Body.String(), "example.org") || strings.Contains(refused.Body.String(), e.user.UUID) {
		t.Fatal("HWID refusal leaked servers")
	}
}

func decodeRouting(t *testing.T, value string) map[string]any {
	t.Helper()
	if _, payload, ok := strings.Cut(value, "/onadd/"); ok {
		value = payload
	}
	b, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestProviderRoutingHeaders(t *testing.T) {
	e := newEnv(t)
	r := service.DefaultClientRouting()
	r.Direct = []string{"domain:example.ru", "geoip:by", "process:game.exe"}
	r.Proxy = []string{"domain:youtube.com", "geoip:nl"}
	r.Block, r.BlockAds = []string{"full:ads.example.org", "1.2.3.4"}, true
	if err := e.svc.SetClientRouting(e.ctx, actor, &r); err != nil {
		t.Fatal(err)
	}
	for _, ua := range []string{"Happ/4.1", "INCY/3.6.5", "KeqDroid/0.34.1"} {
		rec := e.get(e.path()+"/base64", ua, "hw-1")
		value := rec.Header().Get("Routing")
		p := decodeRouting(t, value)
		if keqClient.MatchString(ua) {
			rules := p["rules"].([]any)
			proxyAt, ruAt := -1, -1
			for i, rule := range rules {
				text, _ := json.Marshal(rule)
				if strings.Contains(string(text), "domain:youtube.com") {
					proxyAt = i
				}
				if strings.Contains(string(text), "geosite:category-ru") {
					ruAt = i
				}
			}
			if proxyAt < 0 || ruAt <= proxyAt {
				t.Fatal("proxy override lost in KeqDroid routing")
			}
			continue
		}
		scheme := "happ"
		if strings.HasPrefix(ua, "INCY") {
			scheme = "incy"
		}
		if !strings.HasPrefix(value, scheme+"://routing/onadd/") || p["RouteOrder"] != "block-proxy-direct" ||
			p["DomesticDNSIP"] != "77.88.8.8" || p["DomesticDNSType"] != "DoU" ||
			p["RemoteDNSDomain"] != "https://1.1.1.1/dns-query" || p["RemoteDNSType"] != "DoH" {
			t.Fatalf("%s: unexpected routing: %v", ua, p)
		}
		text, _ := json.Marshal(p)
		for _, want := range []string{"geosite:category-ru", "geoip:ru", "geoip:private", "geoip:by", "domain:youtube.com", "full:ads.example.org", "geosite:category-ads-all"} {
			if !strings.Contains(string(text), want) {
				t.Fatalf("routing lacks %s", want)
			}
		}
		if strings.Contains(string(text), "process:") {
			t.Fatal("unsupported process entry in Happ profile")
		}
	}
	if got := e.get(e.path(), "v2rayNG/1.9", "hw-1").Header().Get("Routing"); got != "" {
		t.Fatal("unrelated client got routing header")
	}
	if got := e.get(e.path(), "KeqDroid/0.34.1", "hw-1").Header().Get("Routing"); got != "" {
		t.Fatal("full JSON already has per-server routing")
	}
	// Saved headers may replace, disable or limit auto routing to selected clients.
	for _, h := range []service.SubHeader{
		{Name: "Routing", Value: "happ://routing/off", Enabled: true},
		{Name: "Routing", Enabled: false},
		{Name: "Autorouting", Value: "incy://autorouting/onadd/https://example.org/routes.json", Enabled: true},
	} {
		if err := e.svc.SetSubHeaders(e.ctx, actor, []service.SubHeader{h}); err != nil {
			t.Fatal(err)
		}
		got := e.get(e.path(), "Happ/4.1", "hw-1").Header().Get("Routing")
		want := ""
		if h.Enabled && h.Name == "Routing" {
			want = h.Value
		}
		if got != want {
			t.Fatalf("custom header %v overridden: %s", h, got)
		}
	}
}

func TestKeqMixedKeepsCustomConfigs(t *testing.T) {
	r := service.DefaultClientRouting()
	custom := `{"remarks":"old","remnawave":{"x":1},"outbounds":[{"tag":"proxy"}],"routing":{"rules":[]},"dns":{"servers":["9.9.9.9"]}}`
	hosts := []service.Host{
		{Remark: "Local", Address: "local.example.org", Protocol: "vless", Network: "tcp", Port: 443, Client: &service.ProfileClient{Mode: "json", Config: custom}},
		{Raw: "trojan://pass@external.example.org:443#External"},
	}
	for _, tpl := range []string{"", custom} {
		body, err := KeqMixed(hosts, "uuid", r, tpl)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(body), "\n")
		if len(lines) != 2 || !json.Valid([]byte(lines[0])) || !strings.Contains(lines[0], "9.9.9.9") ||
			!strings.Contains(lines[0], `"remarks":"Local"`) || lines[1] != hosts[1].Raw {
			t.Fatalf("custom config lost: %s", body)
		}
	}
}

func TestExternalRoutingDoesNotReplacePanelRouting(t *testing.T) {
	e := newEnv(t)
	for _, scheme := range []string{"happ", "incy", "keqdroid"} {
		metadata := scheme + "://routing/off"
		friend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(metadata + "\n" + extRealityLink))))
		}))
		ext, err := e.svc.CreateExternalSub(e.ctx, actor, service.ExternalSubInput{Name: scheme, URL: friend.URL, Enabled: true})
		friend.Close()
		if err != nil || ext.Links != extRealityLink {
			t.Fatalf("external routing merged: %+v, %v", ext, err)
		}
		if hs := service.ParseLinks(metadata + "\n" + extRealityLink); len(hs) != 1 {
			t.Fatal("cached routing metadata still becomes a server")
		}
	}
}

// A final network catch-all used to prevent IPIfNonMatch from resolving domains,
// so even an explicit IP/direct rule did not apply to requests made by hostname.
func TestXrayRoutingResolvesBeforeFinalProxy(t *testing.T) {
	bin := xraytest.Binary(t)
	origin := xraytest.Origin(t)
	r := service.ClientRouting{Torrent: "proxy", Direct: []string{"127.0.0.1/32"}}
	cfg := xrayClient(Stub("routing"), StubUUID, r)
	cfg["dns"] = map[string]any{"hosts": map[string]any{"routing.example.test": "127.0.0.1"}, "servers": []string{"localhost"}, "queryStrategy": "UseIPv4"}
	// The default outbound cannot connect: success proves DNS resolved the domain
	// and an IP rule selected direct before the final proxy rule.
	cfg["outbounds"].([]any)[0] = map[string]any{"protocol": "blackhole", "tag": "proxy"}
	// Keep the direct dial independent of the OS resolver: only Xray's routing DNS
	// knows this test hostname. The selected outbound is what this test exercises.
	cfg["outbounds"].([]any)[1] = map[string]any{"protocol": "freedom", "tag": "direct",
		"settings": map[string]any{"redirect": strings.TrimSuffix(strings.TrimPrefix(origin, "http://"), "/")}}
	socks := xraytest.FreePort(t)
	inbound := cfg["inbounds"].([]any)[0].(map[string]any)
	inbound["port"] = socks
	cfg["inbounds"] = []any{inbound}
	raw, _ := json.Marshal(cfg)
	xraytest.StartClient(t, bin, raw, socks)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	body, err := xraytest.Fetch(ctx, socks, strings.Replace(origin, "127.0.0.1", "routing.example.test", 1))
	if err != nil || len(body) != xraytest.OriginSize {
		t.Fatalf("domain request did not follow the IP/direct rule: %v (%d bytes)", err, len(body))
	}
}
