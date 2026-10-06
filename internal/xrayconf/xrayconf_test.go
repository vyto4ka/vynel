package xrayconf

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

const (
	fixturePriv = "SG1wpwXVHo3KVNFGjW-qT9Qh3k0p9_sWoJ88nyRvt30"
	fixturePub  = "T3DegLM5VyRFQ7h004W_c8AdxAUasUpIV8dZlRGUpmE" // from `xray x25519` for fixturePriv
)

func mustBase(t *testing.T) map[string]any {
	t.Helper()
	b, err := ParseBase(DefaultBaseJSON)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func normJSON(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func readFixture(t *testing.T, name string) any {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func assertJSONEqual(t *testing.T, want, got any) {
	t.Helper()
	if !reflect.DeepEqual(normJSON(t, want), normJSON(t, got)) {
		w, _ := json.MarshalIndent(want, "", "  ")
		g, _ := json.MarshalIndent(got, "", "  ")
		t.Fatalf("JSON mismatch\nwant:\n%s\ngot:\n%s", w, g)
	}
}

func realitySpec() InboundSpec {
	return InboundSpec{
		TemplateID: "vless-reality-selfsteal",
		NodeValues: map[string]any{
			"REALITY_PRIVATE_KEY": fixturePriv,
			"REALITY_SHORT_ID":    "a1b2c3d4e5f60718",
		},
		Context: NodeContext{Code: "NL", Name: "Нидерланды", Country: "nl", Domain: "nl2.vyto4ka.ru"},
	}
}

func xhttpSpec() InboundSpec {
	return InboundSpec{
		TemplateID:    "vless-xhttp-vkcdn",
		ProfileValues: map[string]any{"UPLINK_PATH": "/upload"},
		NodeValues:    map[string]any{"CDN_DOMAIN": "cdn.example.com"},
		Context:       NodeContext{Code: "NL", Name: "Нидерланды", Country: "NL", Domain: "origin.example.com"},
	}
}

func TestTemplatesLoad(t *testing.T) {
	ts, err := Templates()
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 2 {
		t.Fatalf("want 2 templates, got %d", len(ts))
	}
}

func TestX25519MatchesXray(t *testing.T) {
	pub, err := X25519Public(fixturePriv)
	if err != nil {
		t.Fatal(err)
	}
	if pub != fixturePub {
		t.Fatalf("public key %s, xray says %s", pub, fixturePub)
	}
	priv, pub2, err := NewX25519()
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := X25519Public(priv); d != pub2 {
		t.Fatal("generated pair does not match")
	}
}

// The rendered template A must equal the working Remnawave profile from the PDF.
func TestGoldenRealityMatchesPDF(t *testing.T) {
	r, err := RenderInbound(realitySpec())
	if err != nil {
		t.Fatal(err)
	}
	if r.Tag != "VLESS_NL" || r.Flow != "xtls-rprx-vision" {
		t.Fatalf("tag %q flow %q", r.Tag, r.Flow)
	}
	if r.Values["REALITY_PUBLIC_KEY"] != fixturePub {
		t.Fatalf("derived public key %v", r.Values["REALITY_PUBLIC_KEY"])
	}
	cfg, err := BuildConfig(NodeConfig{Base: mustBase(t), Inbounds: []*RenderedInbound{r}, APIAddr: "127.0.0.1:10085"})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range SystemKeys {
		if _, ok := cfg[k]; !ok {
			t.Fatalf("system section %q missing", k)
		}
		delete(cfg, k)
	}
	assertJSONEqual(t, readFixture(t, "pdf_reality.json"), cfg)
}

// The rendered template B must equal the inbound from the VK CDN guide.
func TestGoldenXHTTPMatchesGuide(t *testing.T) {
	r, err := RenderInbound(xhttpSpec())
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, readFixture(t, "guide_xhttp_inbound.json"), r.Inbound)
	if r.Values["ORIGIN_DOMAIN"] != "origin.example.com" {
		t.Fatalf("ORIGIN_DOMAIN not taken from node domain: %v", r.Values["ORIGIN_DOMAIN"])
	}
}

func TestOverridesInheritance(t *testing.T) {
	spec := realitySpec()
	spec.ProfileOverride = map[string]any{
		"streamSettings": map[string]any{"sockopt": map[string]any{"tcpKeepAliveIdle": 60, "tcpcongestion": nil}},
	}
	spec.NodeOverride = map[string]any{
		"streamSettings": map[string]any{"sockopt": map[string]any{"tcpKeepAliveIdle": 90}},
		"sniffing":       map[string]any{"enabled": false},
	}
	spec.PortOverride = 8443
	spec.Context.ListenIP = "203.0.113.12"
	r, err := RenderInbound(spec)
	if err != nil {
		t.Fatal(err)
	}
	so := r.Inbound["streamSettings"].(map[string]any)["sockopt"].(map[string]any)
	if so["tcpKeepAliveIdle"] != 90 {
		t.Fatalf("node override should win, got %v", so["tcpKeepAliveIdle"])
	}
	if _, ok := so["tcpcongestion"]; ok {
		t.Fatal("null in profile override should delete the key")
	}
	if so["tcpFastOpen"] != true {
		t.Fatal("untouched template field lost")
	}
	if r.Inbound["port"] != 8443 || r.Inbound["listen"] != "203.0.113.12" {
		t.Fatalf("port/listen: %v %v", r.Inbound["port"], r.Inbound["listen"])
	}
}

func TestMissingValues(t *testing.T) {
	spec := realitySpec()
	spec.Context.Domain = ""
	spec.NodeValues = map[string]any{}
	_, err := RenderInbound(spec)
	if err == nil || !strings.Contains(err.Error(), "NODE_DOMAIN") || !strings.Contains(err.Error(), "REALITY_PRIVATE_KEY") {
		t.Fatalf("want missing NODE_DOMAIN and REALITY_PRIVATE_KEY, got %v", err)
	}
}

func TestValidation(t *testing.T) {
	spec := xhttpSpec()
	spec.ProfileValues["UPLINK_PATH"] = "/upload/"
	if _, err := RenderInbound(spec); err == nil || !strings.Contains(err.Error(), "UPLINK_PATH") {
		t.Fatalf("trailing slash must be rejected, got %v", err)
	}
	spec = realitySpec()
	spec.Context.Domain = "not a domain"
	if _, err := RenderInbound(spec); err == nil {
		t.Fatal("bad domain must be rejected")
	}
}

func TestGenerateValues(t *testing.T) {
	tpl, _ := GetTemplate("vless-reality-selfsteal")
	vals, err := tpl.GenerateValues(ScopeNode, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(vals["REALITY_SHORT_ID"].(string)) != 16 {
		t.Fatalf("shortId %v", vals["REALITY_SHORT_ID"])
	}
	if _, err := X25519Public(vals["REALITY_PRIVATE_KEY"].(string)); err != nil {
		t.Fatal(err)
	}
	if _, ok := vals["REALITY_PUBLIC_KEY"]; ok {
		t.Fatal("derived values must not be stored")
	}
	again, _ := tpl.GenerateValues(ScopeNode, vals)
	if len(again) != 0 {
		t.Fatal("existing values must not be regenerated")
	}
	b, _ := GetTemplate("vless-xhttp-vkcdn")
	pv, _ := b.GenerateValues(ScopeProfile, nil)
	if p := pv["UPLINK_PATH"]; p != "/upload" && p != "/hls" && p != "/segment" && p != "/media" {
		t.Fatalf("path %v", p)
	}
}

func TestLintNestedExtra(t *testing.T) {
	spec := xhttpSpec()
	spec.NodeOverride = map[string]any{"streamSettings": map[string]any{"xhttpSettings": map[string]any{
		"extra": map[string]any{"extra": map[string]any{"xPaddingKey": "x"}, "uplinkHTTPMethod": "POST"},
	}}}
	_, err := RenderInbound(spec)
	if !IsLintError(err) {
		t.Fatalf("want lint error, got %v", err)
	}
	if !strings.Contains(err.Error(), "nested") || !strings.Contains(err.Error(), "GET") {
		t.Fatalf("both problems should be reported: %v", err)
	}
}

func TestEgressRouting(t *testing.T) {
	a := realitySpec()
	a.Context.ListenIP = "203.0.113.12"
	a.EgressIP = "203.0.113.12"
	ra, err := RenderInbound(a)
	if err != nil {
		t.Fatal(err)
	}
	b := xhttpSpec()
	rb, err := RenderInbound(b)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := BuildConfig(NodeConfig{
		Base:     mustBase(t),
		Inbounds: []*RenderedInbound{ra, rb},
		Clients:  map[string][]Client{"VLESS_NL": {{Email: "7", ID: "11111111-1111-4111-8111-111111111111"}}},
		APIAddr:  "127.0.0.1:10085",
	})
	if err != nil {
		t.Fatal(err)
	}
	outs := cfg["outbounds"].([]any)
	last := outs[len(outs)-1].(map[string]any)
	if last["tag"] != "DIRECT@203.0.113.12" || last["sendThrough"] != "203.0.113.12" {
		t.Fatalf("egress outbound: %v", last)
	}
	rules := cfg["routing"].(map[string]any)["rules"].([]any)
	if len(rules) != 5 {
		t.Fatalf("want 5 rules, got %d", len(rules))
	}
	egress := rules[3].(map[string]any)
	if egress["outboundTag"] != "DIRECT@203.0.113.12" {
		t.Fatalf("egress rule must sit after block rules and before DIRECT: %v", rules)
	}
	in := cfg["inbounds"].([]any)[0].(map[string]any)
	clients := in["settings"].(map[string]any)["clients"].([]any)
	if len(clients) != 1 || clients[0].(map[string]any)["flow"] != "xtls-rprx-vision" {
		t.Fatalf("clients: %v", clients)
	}
	// The base must not be mutated by BuildConfig.
	if len(mustBase(t)["outbounds"].([]any)) != 2 {
		t.Fatal("base mutated")
	}
}

func TestDuplicateListen(t *testing.T) {
	r1, _ := RenderInbound(realitySpec())
	s2 := realitySpec()
	s2.Context.Tag = "VLESS_NL_2"
	r2, _ := RenderInbound(s2)
	_, err := BuildConfig(NodeConfig{Base: mustBase(t), Inbounds: []*RenderedInbound{r1, r2}, APIAddr: "127.0.0.1:1"})
	if err == nil || !strings.Contains(err.Error(), "both listen") {
		t.Fatalf("want listen conflict, got %v", err)
	}
}

func TestParseBaseRejectsSystemSections(t *testing.T) {
	if _, err := ParseBase(`{"api":{}}`); err == nil {
		t.Fatal("api in base must be rejected")
	}
}

func TestCountryFlag(t *testing.T) {
	if CountryFlag("nl") != "🇳🇱" || CountryFlag("x") != "" {
		t.Fatal("flag")
	}
}
