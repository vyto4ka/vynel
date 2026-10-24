package subscription

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/xray/xraytest"
)

// A Mihomo template made for Remnawave: the servers join the proxies and the groups, except where
// a group opts out; a provider that opts in gets them as payload; the "remnawave" keys are gone.
const remnawaveMihomo = `mixed-port: 7890
mode: rule
remnawave:
  includeHiddenHosts: false
proxies:
  - {name: friend, type: socks5, server: 10.0.0.1, port: 1080}
proxy-groups:
  - name: 🚀 Proxy
    type: select
    proxies: [⚡ Auto]
  - name: ⚡ Auto
    type: url-test
    url: https://www.gstatic.com/generate_204
    interval: 300
  - name: 🇷🇺 Direct
    type: select
    proxies: [DIRECT]
    remnawave:
      include-proxies: false
proxy-providers:
  chain:
    type: inline
    remnawave:
      include-proxies: true
rules:
  - DOMAIN-SUFFIX,epicgames.com,DIRECT
  - PROCESS-NAME,FortniteClient-Win64-Shipping.exe,DIRECT
  - GEOSITE,category-ru,DIRECT
  - GEOSITE,category-ads-all,REJECT
  - GEOSITE,youtube,🚀 Proxy
  - DST-PORT,6881,DIRECT
  - MATCH,🚀 Proxy
dns:
  enable: true
  nameserver: [https://1.1.1.1/dns-query#🚀 Proxy]
  proxy-server-nameserver: [system, 77.88.8.8]
`

func TestMihomoFromRemnawaveTemplate(t *testing.T) {
	e := newEnv(t)
	hosts, err := e.svc.SubscriptionHosts(e.ctx, e.user, 0)
	if err != nil {
		t.Fatal(err)
	}
	var mihomoHosts []service.Host
	for _, h := range hosts {
		if FormatMihomo.Supports(h) {
			mihomoHosts = append(mihomoHosts, h)
		}
	}
	out, err := MihomoFromTemplate(mihomoHosts, e.user.UUID, remnawaveMihomo)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "remnawave") {
		t.Fatalf("remnawave keys left:\n%s", out)
	}
	var doc struct {
		Proxies   []map[string]any          `yaml:"proxies"`
		Groups    []map[string]any          `yaml:"proxy-groups"`
		Providers map[string]map[string]any `yaml:"proxy-providers"`
		Rules     []string                  `yaml:"rules"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Proxies) != len(mihomoHosts)+1 || doc.Proxies[0]["name"] != "friend" {
		t.Fatalf("proxies %v", doc.Proxies)
	}
	server := doc.Proxies[1]["name"]
	proxyNames := func(i int) []any { l, _ := doc.Groups[i]["proxies"].([]any); return l }
	if g := proxyNames(0); len(g) != len(mihomoHosts)+1 || g[0] != "⚡ Auto" || g[1] != server {
		t.Fatalf("select group %v", g)
	}
	if g := proxyNames(1); len(g) != len(mihomoHosts) {
		t.Fatalf("url-test group %v", g)
	}
	if g := proxyNames(2); len(g) != 1 || g[0] != "DIRECT" {
		t.Fatalf("an opted-out group got servers: %v", g)
	}
	if p, _ := doc.Providers["chain"]["payload"].([]any); len(p) != len(mihomoHosts) {
		t.Fatalf("provider payload %v", doc.Providers["chain"])
	}
	if !slices.Contains(doc.Rules, "PROCESS-NAME,FortniteClient-Win64-Shipping.exe,DIRECT") {
		t.Fatalf("the template's rules lost: %v", doc.Rules)
	}
	if bin := os.Getenv("MIHOMO_BIN"); bin != "" {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "config.yaml"), out, 0o600)
		if o, err := exec.Command(bin, "-t", "-d", dir).CombinedOutput(); err != nil {
			t.Fatalf("mihomo -t: %v\n%s", err, o)
		}
	}
}

// The template's routing goes to the routing fields: lists, RU, ads, DNS; what has no field is listed.
func TestReadTemplate(t *testing.T) {
	info, err := ReadTemplate("mihomo", remnawaveMihomo)
	if err != nil || info.Routing == nil {
		t.Fatalf("%+v %v", info, err)
	}
	r := info.Routing
	if !r.RUDirect || !r.BlockAds || !slices.Equal(r.Direct, []string{"epicgames.com", "process:FortniteClient-Win64-Shipping.exe"}) ||
		!slices.Equal(r.Proxy, []string{"geosite:youtube"}) || !slices.Equal(r.DNSProxy, []string{"https://1.1.1.1/dns-query"}) ||
		!slices.Equal(r.DNSDirect, []string{"localhost", "77.88.8.8"}) {
		t.Fatalf("routing %+v", r)
	}
	if !slices.Equal(info.Skipped, []string{"DST-PORT,6881,DIRECT"}) {
		t.Fatalf("skipped %v", info.Skipped)
	}
	if _, err := ReadTemplate("mihomo", "proxies:\n  - a\n b: c"); err == nil || !strings.Contains(err.Error(), "line") {
		t.Fatalf("a broken YAML: %v", err)
	}
	if _, err := ReadTemplate("singbox", "{\n\"outbounds\": [\n}"); err == nil || !strings.Contains(err.Error(), "строка 3") {
		t.Fatalf("a broken JSON: %v", err)
	}
	if _, err := ReadTemplate("base64", "x"); err == nil {
		t.Fatal("a template for links accepted")
	}
	sb := `{"outbounds":[{"type":"selector","tag":"proxy"},{"type":"urltest","tag":"auto"},{"type":"direct","tag":"direct"}],
		"route":{"rules":[{"action":"sniff"},{"domain_suffix":["epicgames.com"],"outbound":"direct"},{"rule_set":["geosite-category-ru"],"outbound":"direct"},
		{"process_name":["game.exe"],"action":"reject"}]}}`
	info, err = ReadTemplate("singbox", sb)
	if err != nil || info.Routing == nil || !info.Routing.RUDirect || !slices.Equal(info.Routing.Direct, []string{"epicgames.com"}) ||
		!slices.Equal(info.Routing.Block, []string{"process:game.exe"}) {
		t.Fatalf("sing-box: %+v %v", info, err)
	}
}

func TestSingBoxAndXrayFromTemplate(t *testing.T) {
	e := newEnv(t)
	hosts, _ := e.svc.SubscriptionHosts(e.ctx, e.user, 0)
	var tcp []service.Host
	for _, h := range hosts {
		if FormatSingBox.Supports(h) {
			tcp = append(tcp, h)
		}
	}
	sb := `{"remnawave":{"x":1},"outbounds":[{"type":"selector","tag":"proxy","outbounds":[]},{"type":"urltest","tag":"auto"},
		{"type":"selector","tag":"fixed","outbounds":["direct"],"remnawave":{"includeProxies":false}},{"type":"direct","tag":"direct"}]}`
	out, err := SingBoxFromTemplate(tcp, e.user.UUID, sb)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Outbounds []struct {
			Type, Tag string
			Outbounds []string
		} `json:"outbounds"`
	}
	_ = json.Unmarshal(out, &doc)
	if strings.Contains(string(out), "remnawave") || len(doc.Outbounds) != 4+len(tcp) ||
		len(doc.Outbounds[0].Outbounds) != len(tcp)+1 || doc.Outbounds[0].Outbounds[0] != "auto" || // as Remnawave: in outbound order
		len(doc.Outbounds[1].Outbounds) != len(tcp) || !slices.Equal(doc.Outbounds[2].Outbounds, []string{"direct"}) {
		t.Fatalf("sing-box:\n%s", out)
	}

	xt := `{"remarks":"x","dns":{"servers":["1.1.1.1"]},"outbounds":[{"tag":"proxy","protocol":"freedom"},{"tag":"direct","protocol":"freedom"}],
		"routing":{"rules":[{"type":"field","domain":["geosite:category-ru"],"outboundTag":"direct"}]}}`
	out, err = XrayJSONFromTemplate(hosts, e.user.UUID, xt)
	if err != nil {
		t.Fatal(err)
	}
	var cfgs []map[string]any
	_ = json.Unmarshal(out, &cfgs)
	if len(cfgs) != len(hosts) {
		t.Fatalf("xray configs %d", len(cfgs))
	}
	for i, c := range cfgs {
		obs, _ := c["outbounds"].([]any)
		first, _ := obs[0].(map[string]any)
		if c["remarks"] != hosts[i].Remark || len(obs) != 2 || first["protocol"] == "freedom" || first["tag"] != "proxy" {
			t.Fatalf("xray config %d: %v", i, c)
		}
	}
	if os.Getenv("XRAY_BIN") != "" {
		raw, _ := json.Marshal(cfgs[0])
		if err := xraytest.Binary(t).Test(context.Background(), raw); err != nil {
			t.Fatalf("xray rejects a config from the template: %v", err)
		}
	}
}

// The client rule's template is used for its app; another app and a link with a format suffix
// for another format do not get it.
func TestClientTemplateByUserAgent(t *testing.T) {
	e := newEnv(t)
	rules, _ := e.svc.SubRules(e.ctx)
	for i := range rules {
		if rules[i].Format == "mihomo" {
			rules[i].Template = remnawaveMihomo
		}
	}
	if err := e.svc.SetSubRules(e.ctx, actor, rules); err != nil {
		t.Fatal(err)
	}
	if body := e.get(e.path(), "FlClashX/0.9", "hw-1").Body.String(); !strings.Contains(body, "⚡ Auto") {
		t.Fatalf("FlClashX did not get the template:\n%.300s", body)
	}
	if body := e.get(e.path()+"/json", "FlClashX/0.9", "hw-1").Body.String(); strings.Contains(body, "⚡ Auto") {
		t.Fatal("the Mihomo template went to another format")
	}
	// KeqDroid set to links: a checkbox in the panel is just the rule's format.
	for i := range rules {
		if strings.Contains(rules[i].Pattern, "keqdroid") {
			rules[i].Format = "base64"
		}
	}
	_ = e.svc.SetSubRules(e.ctx, actor, rules)
	if body := e.get(e.path(), "KeqDroid/1.0", "hw-1").Body.String(); strings.HasPrefix(strings.TrimSpace(body), "[") {
		t.Fatal("KeqDroid still gets Xray JSON")
	}
	// A bad template is refused with where it is.
	rules[0].Template = "{"
	rules[0].Format = "xray"
	if err := e.svc.SetSubRules(e.ctx, actor, rules); err == nil || !strings.Contains(err.Error(), "«Свой шаблон»") {
		t.Fatalf("a bad template: %v", err)
	}
}

func TestUniqueNames(t *testing.T) {
	hosts := []service.Host{{Remark: "NL"}, {Remark: "NL 2"}, {Remark: "NL"}, {Remark: "NL"}, {Tag: "VLESS_DE"}, {Tag: "VLESS_DE"}}
	got := uniqueNames(hosts)
	want := []string{"NL", "NL 2", "NL 3", "NL 4", "VLESS_DE", "VLESS_DE 2"}
	if !slices.Equal(got, want) {
		t.Fatalf("%v, want %v", got, want)
	}
}
