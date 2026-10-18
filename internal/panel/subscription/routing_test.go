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
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xray/xraytest"
)

func rulesOf(t *testing.T, cfg map[string]any) []map[string]any {
	t.Helper()
	raw, _ := json.Marshal(cfg["routing"].(map[string]any)["rules"])
	var rs []map[string]any
	if err := json.Unmarshal(raw, &rs); err != nil {
		t.Fatal(err)
	}
	return rs
}

// The default routing: Russian sites direct and resolved by Yandex, the rest through the tunnel
// with DoH; the last rule sends everything else to the proxy.
func TestXrayJSONRouting(t *testing.T) {
	e := newEnv(t)
	var configs []map[string]any
	if err := json.Unmarshal(e.get(e.path()+"/json", "x", "hw-1").Body.Bytes(), &configs); err != nil {
		t.Fatal(err)
	}
	cfg := configs[0]
	rs := rulesOf(t, cfg)
	if last := rs[len(rs)-1]; last["outboundTag"] != "proxy" || last["network"] != "tcp,udp" {
		t.Fatalf("last rule %v", last)
	}
	text, _ := json.Marshal(rs)
	for _, want := range []string{`"geoip:ru"`, `"geosite:category-ru"`, `"domain:xn--p1ai"`, `"bittorrent"`, `"dns-out"`} {
		if !strings.Contains(string(text), want) {
			t.Fatalf("rules lack %s: %s", want, text)
		}
	}
	dns, _ := json.Marshal(cfg["dns"])
	if !strings.Contains(string(dns), `"address":"77.88.8.8"`) || !strings.Contains(string(dns), `"skipFallback":true`) ||
		!strings.Contains(string(dns), `"https://1.1.1.1/dns-query"`) {
		t.Fatalf("dns %s", dns)
	}
	tags := []string{}
	for _, o := range cfg["outbounds"].([]any) {
		tags = append(tags, o.(map[string]any)["tag"].(string))
	}
	if !slices.Equal(tags, []string{"proxy", "direct", "block", "dns-out"}) {
		t.Fatalf("outbounds %v", tags)
	}

	// Without RU direct the Russian rules and the split DNS go away.
	r := service.DefaultClientRouting()
	r.RUDirect, r.Torrent, r.Proxy = false, "block", []string{"youtube.com", "geoip:nl"}
	if err := e.svc.SetClientRouting(e.ctx, actor, &r); err != nil {
		t.Fatal(err)
	}
	configs = nil
	_ = json.Unmarshal(e.get(e.path()+"/json", "x", "hw-1").Body.Bytes(), &configs)
	text, _ = json.Marshal(rulesOf(t, configs[0]))
	if strings.Contains(string(text), "geoip:ru") || !strings.Contains(string(text), `"outboundTag":"block","protocol":["bittorrent"]`) ||
		!strings.Contains(string(text), `"domain":["domain:youtube.com"],"outboundTag":"proxy"`) || !strings.Contains(string(text), `"ip":["geoip:nl"]`) {
		t.Fatalf("rules %s", text)
	}
	if dns, _ := json.Marshal(configs[0]["dns"]); strings.Contains(string(dns), "skipFallback") {
		t.Fatalf("split dns without RU direct: %s", dns)
	}
}

// A profile's own client config: its routing replaces the subscription's, or a whole config
// gets the server as its proxy outbound.
func TestProfileClientConfig(t *testing.T) {
	e := newEnv(t)
	prof, err := e.svc.ProfileByName(e.ctx, "Reality")
	if err != nil {
		t.Fatal(err)
	}
	own := `{"log":{"loglevel":"none"},"outbounds":[{"tag":"direct","protocol":"freedom"},{"tag":"proxy","protocol":"vless"}],
		"routing":{"rules":[{"type":"field","domain":["geosite:google"],"outboundTag":"direct"}]}}`
	if _, err := e.svc.UpdateProfile(e.ctx, actor, prof.ID, service.ProfileInput{Client: &service.ProfileClient{Custom: true, Mode: "json", Config: "[" + own + "]"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdateProfile(e.ctx, actor, prof.ID, service.ProfileInput{Client: &service.ProfileClient{Custom: true, Mode: "json", Config: "{"}}); err == nil {
		t.Fatal("broken JSON accepted")
	}
	var configs []map[string]any
	_ = json.Unmarshal(e.get(e.path()+"/json", "x", "hw-1").Body.Bytes(), &configs)
	var mine, other map[string]any
	for _, c := range configs {
		if c["log"].(map[string]any)["loglevel"] == "none" {
			mine = c
		} else {
			other = c
		}
	}
	if mine == nil || other == nil {
		t.Fatalf("configs %v", configs)
	}
	obs := mine["outbounds"].([]any)
	proxy := obs[1].(map[string]any)
	if len(obs) != 2 || obs[0].(map[string]any)["tag"] != "direct" || proxy["tag"] != "proxy" || proxy["settings"] == nil {
		t.Fatalf("custom outbounds %v", obs)
	}
	if mine["remarks"] == "" || len(rulesOf(t, mine)) != 1 {
		t.Fatalf("custom config %v", mine)
	}
	// The other profile keeps the subscription routing.
	if len(rulesOf(t, other)) < 5 {
		t.Fatalf("other profile routing %v", other["routing"])
	}

	// Own routing (not a whole config).
	r := service.DefaultClientRouting()
	r.RUDirect, r.Block = false, []string{"full:bad.example.com"}
	if _, err := e.svc.UpdateProfile(e.ctx, actor, prof.ID, service.ProfileInput{Client: &service.ProfileClient{Custom: true, Mode: "routing", Routing: &r}}); err != nil {
		t.Fatal(err)
	}
	configs = nil
	_ = json.Unmarshal(e.get(e.path()+"/json", "x", "hw-1").Body.Bytes(), &configs)
	found := false
	for _, c := range configs {
		text, _ := json.Marshal(rulesOf(t, c))
		if strings.Contains(string(text), "full:bad.example.com") {
			found = true
			if strings.Contains(string(text), "geoip:ru") {
				t.Fatalf("own routing kept RU: %s", text)
			}
		}
	}
	if !found {
		t.Fatal("own routing not used")
	}
	// Switching it off goes back to the subscription routing.
	if _, err := e.svc.UpdateProfile(e.ctx, actor, prof.ID, service.ProfileInput{Client: &service.ProfileClient{}}); err != nil {
		t.Fatal(err)
	}
	p, _ := e.svc.ProfileByName(e.ctx, "Reality")
	if p.Client != "" {
		t.Fatalf("client kept: %s", p.Client)
	}
}

func TestMihomoAndSingBoxRouting(t *testing.T) {
	e := newEnv(t)
	var doc struct {
		Rules []string       `yaml:"rules"`
		DNS   map[string]any `yaml:"dns"`
	}
	if err := yaml.Unmarshal(e.get(e.path(), "clash-verge/v2.0 mihomo", "hw-1").Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"GEOSITE,category-ru,DIRECT", "DOMAIN-SUFFIX,xn--p1ai,DIRECT", "GEOIP,ru,DIRECT", "GEOIP,private,DIRECT,no-resolve"} {
		if !slices.Contains(doc.Rules, want) {
			t.Fatalf("mihomo rules lack %s: %v", want, doc.Rules)
		}
	}
	if doc.Rules[len(doc.Rules)-1] != "MATCH,VPN" {
		t.Fatalf("mihomo last rule %v", doc.Rules)
	}
	policy, _ := doc.DNS["nameserver-policy"].(map[string]any)
	ns, _ := doc.DNS["nameserver"].([]any)
	if policy["geosite:category-ru"] == nil || len(ns) != 2 || ns[0] != "https://1.1.1.1/dns-query#VPN" {
		t.Fatalf("mihomo dns %v", doc.DNS)
	}

	var sb map[string]any
	if err := json.Unmarshal(e.get(e.path(), "SFA/1.12.0 (sing-box 1.12.0)", "hw-1").Body.Bytes(), &sb); err != nil {
		t.Fatal(err)
	}
	route, _ := json.Marshal(sb["route"])
	for _, want := range []string{`"geosite-category-ru"`, `"geoip-ru"`, `"domain_suffix":["ru","su","xn--p1ai"]`, `"protocol":"bittorrent"`, `"final":"proxy"`} {
		if !strings.Contains(string(route), want) {
			t.Fatalf("sing-box route lacks %s: %s", want, route)
		}
	}
	dns, _ := json.Marshal(sb["dns"])
	if !strings.Contains(string(dns), `"server":"dns-direct"`) || !strings.Contains(string(dns), `"detour":"proxy"`) {
		t.Fatalf("sing-box dns %s", dns)
	}
}

func TestCheckClientRouting(t *testing.T) {
	for _, bad := range []service.ClientRouting{
		{Torrent: "maybe"},
		{DNSProxy: []string{"localhost"}},
		{DNSDirect: []string{"dns.example"}},
		{Direct: []string{"regexp:("}},
		{Block: []string{"geosite:"}},
		{Direct: []string{`process:C:\\Games\\game.exe`}},
		{Direct: []string{"process:a,DIRECT"}},
		{Direct: []string{"process:"}},
	} {
		if err := service.CheckClientRouting(&bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	// Errors name the field and the line as the panel shows them.
	for in, want := range map[*service.ClientRouting]string{
		{Proxy: []string{"youtube.com", "", "regexp:("}}:               "«Всегда через туннель», строка 3: \"regexp:(\" — неверное регулярное выражение",
		{DNSProxy: []string{"https://1.1.1.1/dns-query", "localhost"}}: "«DNS через туннель», строка 2: localhost",
		{Block: []string{"a b"}}:                                       "«Блокировать», строка 1: \"a b\" — непонятная запись",
	} {
		if err := service.CheckClientRouting(in); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("error %v, want %q", err, want)
		}
	}
	if _, err := service.ParseClientConfig("{\n  \"outbounds\": [\n    {\"tag\": \"proxy\",}\n  ]\n}"); err == nil ||
		!strings.Contains(err.Error(), "«Конфиг JSON», строка 3:") {
		t.Fatalf("json error %v", err)
	}
	r := service.ClientRouting{Direct: []string{"", " example.ru ", "10.0.0.0/8"}, DNSDirect: []string{"https://dns.yandex.ru/dns-query"}}
	if err := service.CheckClientRouting(&r); err != nil || len(r.Direct) != 2 || r.Torrent != "proxy" {
		t.Fatalf("%v %+v", err, r)
	}
}

// A KeqDroid rule saved with the old default (links) moves to Xray JSON; an edited one stays.
func TestKeqDroidRuleMigration(t *testing.T) {
	e := newEnv(t)
	old := `[{"pattern":"(?i)keqdroid|keqdis","format":"base64","enabled":true,"note":"KeqDroid лучше всего читает ссылки vless://"},` +
		`{"pattern":"(?i)happ","format":"base64","enabled":true}]`
	if err := e.svc.SetSetting(e.ctx, actor, service.SettingSubRules, old); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.EnsureDefaults(e.ctx); err != nil {
		t.Fatal(err)
	}
	rs, _ := e.svc.SubRules(e.ctx)
	if rs[0].Format != "xray" || rs[1].Format != "base64" {
		t.Fatalf("rules %+v", rs)
	}
	edited := strings.Replace(old, "ссылки vless://", "ссылки", 1)
	_ = e.svc.SetSetting(e.ctx, actor, service.SettingSubRules, edited)
	_ = e.svc.EnsureDefaults(e.ctx)
	if rs, _ := e.svc.SubRules(e.ctx); rs[0].Format != "base64" {
		t.Fatalf("edited rule changed: %+v", rs)
	}
	_ = store.StatusActive
}

// The real apps take the profiles (SINGBOX_BIN: `sing-box check`; MIHOMO_BIN: `mihomo -t`,
// which downloads the geo databases it needs).
func TestRoutingAcceptedByApps(t *testing.T) {
	e := newEnv(t)
	r := service.DefaultClientRouting()
	r.BlockAds, r.Direct, r.Proxy, r.Block = true, []string{"geoip:by", "full:a.example.com", "keyword:yandex", "10.1.0.0/16",
		"process:FortniteClient-Win64-Shipping.exe", "process:com.epicgames.fortnite"},
		[]string{"geosite:telegram", "regexp:^x\\.example\\.com$"}, []string{"1.2.3.4"}
	if err := e.svc.SetClientRouting(e.ctx, actor, &r); err != nil {
		t.Fatal(err)
	}
	if bin := os.Getenv("SINGBOX_BIN"); bin != "" {
		f := filepath.Join(t.TempDir(), "sb.json")
		_ = os.WriteFile(f, e.get(e.path(), "SFA/1.12.0", "hw-1").Body.Bytes(), 0o600)
		if out, err := exec.Command(bin, "check", "-c", f).CombinedOutput(); err != nil {
			t.Fatalf("sing-box check: %v\n%s", err, out)
		}
	}
	if bin := os.Getenv("MIHOMO_BIN"); bin != "" {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "config.yaml"), e.get(e.path(), "clash-verge/v2.0 mihomo", "hw-1").Body.Bytes(), 0o600)
		if out, err := exec.Command(bin, "-t", "-d", dir).CombinedOutput(); err != nil {
			t.Fatalf("mihomo -t: %v\n%s", err, out)
		}
	}
}

// Apps by name (process:): matches games and other traffic without a site name in every format
// with routing, and never leaks into the DNS split or the domain lists.
func TestProcessEntries(t *testing.T) {
	e := newEnv(t)
	r := service.DefaultClientRouting()
	r.Direct = []string{"geosite:epicgames", "process:FortniteClient-Win64-Shipping.exe", "process:Epic Games Launcher.exe"}
	r.Block = []string{"process:spy.exe"}
	if err := e.svc.SetClientRouting(e.ctx, actor, &r); err != nil {
		t.Fatal(err)
	}

	var doc struct {
		Rules []string       `yaml:"rules"`
		DNS   map[string]any `yaml:"dns"`
	}
	if err := yaml.Unmarshal(e.get(e.path(), "FlClashX/0.9 clash-verge", "hw-1").Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"PROCESS-NAME,FortniteClient-Win64-Shipping.exe,DIRECT", "PROCESS-NAME,Epic Games Launcher.exe,DIRECT",
		"PROCESS-NAME,spy.exe,REJECT", "GEOSITE,epicgames,DIRECT"} {
		if !slices.Contains(doc.Rules, want) {
			t.Fatalf("mihomo rules lack %s: %v", want, doc.Rules)
		}
	}
	policy, _ := doc.DNS["nameserver-policy"].(map[string]any)
	for k := range policy {
		if strings.Contains(k, "process") || strings.Contains(k, ".exe") {
			t.Fatalf("an app in the mihomo dns policy: %v", policy)
		}
	}
	if policy["geosite:epicgames"] == nil {
		t.Fatalf("mihomo dns policy %v", policy)
	}

	var sb map[string]any
	if err := json.Unmarshal(e.get(e.path(), "SFA/1.12.0 (sing-box 1.12.0)", "hw-1").Body.Bytes(), &sb); err != nil {
		t.Fatal(err)
	}
	route, _ := json.Marshal(sb["route"])
	dns, _ := json.Marshal(sb["dns"])
	if !strings.Contains(string(route), `{"outbound":"direct","process_name":["FortniteClient-Win64-Shipping.exe","Epic Games Launcher.exe"]}`) ||
		!strings.Contains(string(route), `{"action":"reject","process_name":["spy.exe"]}`) || strings.Contains(string(dns), "process") {
		t.Fatalf("sing-box route %s\ndns %s", route, dns)
	}

	var configs []map[string]any
	if err := json.Unmarshal(e.get(e.path()+"/json", "x", "hw-1").Body.Bytes(), &configs); err != nil {
		t.Fatal(err)
	}
	rules, _ := json.Marshal(rulesOf(t, configs[0]))
	xdns, _ := json.Marshal(configs[0]["dns"])
	if !strings.Contains(string(rules), `{"outboundTag":"direct","process":["FortniteClient-Win64-Shipping.exe","Epic Games Launcher.exe"],"type":"field"}`) ||
		!strings.Contains(string(rules), `{"outboundTag":"block","process":["spy.exe"],"type":"field"}`) ||
		strings.Contains(string(xdns), "process") || strings.Contains(string(rules), `"process:`) {
		t.Fatalf("xray rules %s\ndns %s", rules, xdns)
	}
	if os.Getenv("XRAY_BIN") != "" {
		for _, c := range configs {
			raw, _ := json.Marshal(c)
			if err := xraytest.Binary(t).Test(context.Background(), raw); err != nil {
				t.Fatalf("xray rejects process rules: %v", err)
			}
		}
	}
}
