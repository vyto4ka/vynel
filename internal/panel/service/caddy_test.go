package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vyto4ka/vynel/internal/caddyconf"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

// layout returns "domain -> listen" for every site of the node's Caddy config.
func layout(t *testing.T, raw []byte) map[string]string {
	t.Helper()
	if raw == nil {
		return map[string]string{}
	}
	var cfg struct {
		Apps struct {
			HTTP struct {
				Servers map[string]struct {
					Listen []string `json:"listen"`
					Routes []struct {
						Match []struct {
							Host []string `json:"host"`
						} `json:"match"`
					} `json:"routes"`
				} `json:"servers"`
			} `json:"http"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, s := range cfg.Apps.HTTP.Servers {
		for _, r := range s.Routes {
			for _, m := range r.Match {
				for _, h := range m.Host {
					out[h] = s.Listen[0]
				}
			}
		}
	}
	return out
}

func (f *fixture) aio(t *testing.T, subDomain string) (*store.Node, *store.Profile) {
	t.Helper()
	n := must(f.s.EnsureLocalNode(f.ctx, NodeInput{Name: "NL", Country: "nl", Domain: "nl.example.com"}))
	prof := must(f.s.CreateProfile(f.ctx, ActorCLI, ProfileInput{Name: "Reality", TemplateID: "vless-reality-selfsteal"}))
	must(f.s.AttachProfile(f.ctx, ActorCLI, AttachInput{NodeID: n.ID, ProfileID: prof.ID}))
	if subDomain != "" {
		if err := f.s.SetSetting(f.ctx, ActorCLI, SettingSubDomain, subDomain); err != nil {
			t.Fatal(err)
		}
	}
	return n, prof
}

func TestCaddyAllInOneOneIP(t *testing.T) {
	f := newFixture(t)
	n, _ := f.aio(t, "sub.example.com")
	ds := must(f.s.DesiredState(f.ctx, n.ID))
	if len(ds.Problems) > 0 {
		t.Fatal(ds.Problems)
	}
	got := layout(t, ds.Caddy)
	// Reality owns :443, so both the self-steal site and the subscription domain live behind it.
	if got["nl.example.com"] != "127.0.0.1:8443" || got["sub.example.com"] != "127.0.0.1:8443" {
		t.Fatalf("layout %v", got)
	}
}

func TestCaddyAllInOneSingleDomain(t *testing.T) {
	f := newFixture(t)
	n, _ := f.aio(t, "nl.example.com") // the minimum: one domain for everything
	ds := must(f.s.DesiredState(f.ctx, n.ID))
	if len(ds.Problems) > 0 {
		t.Fatal(ds.Problems)
	}
	s := string(ds.Caddy)
	if strings.Count(s, `"host":["nl.example.com"]`) != 2 || !strings.Contains(s, `"path":["/s/*"]`) {
		t.Fatalf("one domain must serve the decoy and the subscription prefix: %s", s)
	}
}

func TestCaddyPanelAndSubsOnOneIPVLESSOnAnother(t *testing.T) {
	f := newFixture(t)
	n := must(f.s.EnsureLocalNode(f.ctx, NodeInput{Name: "NL", Country: "nl", Domain: "nl.example.com"}))
	ip1 := must(f.s.AddAddress(f.ctx, ActorCLI, n.ID, "203.0.113.10", ""))
	ip2 := must(f.s.AddAddress(f.ctx, ActorCLI, n.ID, "203.0.113.12", ""))
	prof := must(f.s.CreateProfile(f.ctx, ActorCLI, ProfileInput{Name: "Reality", TemplateID: "vless-reality-selfsteal"}))
	must(f.s.AttachProfile(f.ctx, ActorCLI, AttachInput{NodeID: n.ID, ProfileID: prof.ID, ListenAddressID: &ip2.ID}))
	for k, v := range map[string]string{SettingSubDomain: "sub.example.com", SettingSubAddress: ip1.IP} {
		if err := f.s.SetSetting(f.ctx, ActorCLI, k, v); err != nil {
			t.Fatal(err)
		}
	}
	ds := must(f.s.DesiredState(f.ctx, n.ID))
	got := layout(t, ds.Caddy)
	if got["sub.example.com"] != "203.0.113.10:443" || got["nl.example.com"] != "127.0.0.1:8443" || len(ds.Problems) > 0 {
		t.Fatalf("layout %v problems %v", got, ds.Problems)
	}
	var cfg map[string]any
	_ = json.Unmarshal(ds.Config, &cfg)
	if !strings.Contains(string(ds.Config), `"sendThrough":"203.0.113.12"`) {
		t.Fatal("VLESS traffic must leave through its own IP")
	}
}

func TestCaddyForeignRealityTargetConflicts(t *testing.T) {
	f := newFixture(t)
	n := must(f.s.EnsureLocalNode(f.ctx, NodeInput{Name: "NL", Country: "nl", Domain: "nl.example.com"}))
	prof := must(f.s.CreateProfile(f.ctx, ActorCLI, ProfileInput{Name: "R", TemplateID: "vless-reality-selfsteal",
		Override: map[string]any{"streamSettings": map[string]any{"realitySettings": map[string]any{"target": "www.microsoft.com:443"}}}}))
	must(f.s.AttachProfile(f.ctx, ActorCLI, AttachInput{NodeID: n.ID, ProfileID: prof.ID}))
	if err := f.s.SetSetting(f.ctx, ActorCLI, SettingSubDomain, "sub.example.com"); err != nil {
		t.Fatal(err)
	}
	ds := must(f.s.DesiredState(f.ctx, n.ID))
	if len(ds.Problems) != 1 || !strings.Contains(ds.Problems[0], "not local") {
		t.Fatalf("problems %v", ds.Problems)
	}
	if _, ok := layout(t, ds.Caddy)["nl.example.com"]; ok {
		t.Fatal("no self-steal site when Reality points at a foreign site")
	}
}

func TestCaddyCDNOriginBehindReality(t *testing.T) {
	f := newFixture(t)
	n, _ := f.aio(t, "")
	src, _ := xrayconf.RetiredTemplate("vless-xhttp-vkcdn")
	must(f.s.SaveTemplate(f.ctx, ActorCLI, "", src))
	cdn := must(f.s.CreateProfile(f.ctx, ActorCLI, ProfileInput{Name: "CDN", TemplateID: "vless-xhttp-vkcdn", Values: map[string]any{"UPLINK_PATH": "/upload"}}))
	must(f.s.AttachProfile(f.ctx, ActorCLI, AttachInput{NodeID: n.ID, ProfileID: cdn.ID, Values: map[string]any{"CDN_DOMAIN": "cdn.example.com", "ORIGIN_DOMAIN": "origin.example.com"}}))
	ds := must(f.s.DesiredState(f.ctx, n.ID))
	got := layout(t, ds.Caddy)
	if got["origin.example.com"] != "127.0.0.1:8443" || len(ds.Problems) > 0 {
		t.Fatalf("A+B on one IP: %v %v", got, ds.Problems)
	}
	if !strings.Contains(string(ds.Caddy), `"flush_interval":-1`) || !strings.Contains(string(ds.Caddy), `"/upload*"`) {
		t.Fatal("XHTTP origin must stream and only proxy its path")
	}
	_ = caddyconf.KindStream
}

func TestCaddyPanelOnOwnPort(t *testing.T) {
	f := newFixture(t)
	n, _ := f.aio(t, "sub.example.com")
	if err := f.s.EnsureWebDefaults(f.ctx); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{SettingWebPort: "47321", SettingWebDecoy: "cloud"} {
		if err := f.s.SetSetting(f.ctx, ActorCLI, k, v); err != nil {
			t.Fatal(err)
		}
	}
	ds := must(f.s.DesiredState(f.ctx, n.ID))
	if len(ds.Problems) > 0 {
		t.Fatal(ds.Problems)
	}
	path := must(f.s.WebPath(f.ctx))
	var cfg struct {
		Apps struct {
			HTTP struct {
				Servers map[string]json.RawMessage `json:"servers"`
			} `json:"http"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(ds.Caddy, &cfg); err != nil {
		t.Fatal(err)
	}
	var behind, own string
	for _, s := range cfg.Apps.HTTP.Servers {
		switch {
		case strings.Contains(string(s), `"127.0.0.1:8443"`):
			behind = string(s)
		case strings.Contains(string(s), `":47321"`):
			own = string(s)
		}
	}
	if behind == "" || own == "" {
		t.Fatalf("want the subscriptions behind Reality and the panel on :47321: %s", ds.Caddy)
	}
	if strings.Contains(behind, path) || !strings.Contains(own, path) || !strings.Contains(own, "/cloud") {
		t.Fatalf("the panel path and its decoy belong to its own port only:\nbehind %s\nown %s", behind, own)
	}
	if u := must(f.s.WebURL(f.ctx)); u != "https://sub.example.com:47321"+path {
		t.Fatalf("web URL %s", u)
	}
}

func TestSetupTwoIPs(t *testing.T) {
	f := newFixture(t)
	res := must(f.s.SetupAllInOne(f.ctx, ActorCLI, SetupInput{Domain: "nl.example.com", SubDomain: "sub.example.com", VPNIP: "203.0.113.12", SubIP: "203.0.113.10"}))
	if err := f.s.EnsureWebDefaults(f.ctx); err != nil {
		t.Fatal(err)
	}
	ds := must(f.s.DesiredState(f.ctx, res.Node.ID))
	got := layout(t, ds.Caddy)
	if got["sub.example.com"] != "203.0.113.10:443" || got["nl.example.com"] != "127.0.0.1:8443" || len(ds.Problems) > 0 {
		t.Fatalf("layout %v problems %v", got, ds.Problems)
	}
	if !strings.Contains(string(ds.Config), `"listen":"203.0.113.12"`) {
		t.Fatal("the VPN must listen on its own IP")
	}
	// Again without IPs: back to every address.
	res = must(f.s.SetupAllInOne(f.ctx, ActorCLI, SetupInput{Domain: "nl.example.com"}))
	if res.Inbound.ListenAddressID != nil {
		t.Fatal("listen address kept")
	}
}

// dialsByHost maps every Caddy host to the upstreams of its routes (recursing into subroutes).
func dialsByHost(t *testing.T, raw []byte) map[string][]string {
	t.Helper()
	if raw == nil {
		return nil
	}
	type route struct {
		Handle []struct {
			Upstreams []struct {
				Dial string `json:"dial"`
			} `json:"upstreams"`
			Routes []route `json:"routes"`
		} `json:"handle"`
		Match []struct {
			Host []string `json:"host"`
		} `json:"match"`
	}
	var cfg struct {
		Apps struct {
			HTTP struct {
				Servers map[string]struct {
					Routes []route `json:"routes"`
				} `json:"servers"`
			} `json:"http"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	var walk func(r route) []string
	walk = func(r route) []string {
		var dials []string
		for _, hd := range r.Handle {
			for _, u := range hd.Upstreams {
				dials = append(dials, u.Dial)
			}
			for _, sub := range hd.Routes {
				dials = append(dials, walk(sub)...)
			}
		}
		for _, m := range r.Match {
			for _, h := range m.Host {
				out[h] = append(out[h], dials...)
			}
		}
		return dials
	}
	for _, s := range cfg.Apps.HTTP.Servers {
		for _, r := range s.Routes {
			walk(r)
		}
	}
	return out
}

// A separate subscription domain keeps the panel on the server's main domain: the
// subscription domain serves subscriptions only. With a dedicated subscription IP the panel
// cannot answer the main domain (its A record points at the VPN address), so it stays on the
// subscription domain.
func TestSetupSeparateSubDomain(t *testing.T) {
	f := newFixture(t)
	res := must(f.s.SetupAllInOne(f.ctx, ActorCLI, SetupInput{Domain: "nl.example.com", SubDomain: "sub.example.com"}))
	if err := f.s.EnsureWebDefaults(f.ctx); err != nil {
		t.Fatal(err)
	}
	if d, _ := f.s.Setting(f.ctx, SettingWebDomain, ""); d != "nl.example.com" {
		t.Fatalf("web.domain %q, want the main domain", d)
	}
	if u := must(f.s.WebURL(f.ctx)); !strings.HasPrefix(u, "https://nl.example.com/") {
		t.Fatalf("web URL %s", u)
	}
	ds := must(f.s.DesiredState(f.ctx, res.Node.ID))
	if len(ds.Problems) > 0 {
		t.Fatal(ds.Problems)
	}
	dials := dialsByHost(t, ds.Caddy)
	if !slices.Contains(dials["sub.example.com"], DefaultSubListen) || slices.Contains(dials["sub.example.com"], DefaultWebListen) {
		t.Fatalf("the sub domain must serve subscriptions only, dials %v", dials["sub.example.com"])
	}
	if !slices.Contains(dials["nl.example.com"], DefaultWebListen) {
		t.Fatalf("the main domain must serve the panel, dials %v", dials["nl.example.com"])
	}

	// One domain for everything: the panel shares it, web.domain stays empty.
	f = newFixture(t)
	must(f.s.SetupAllInOne(f.ctx, ActorCLI, SetupInput{Domain: "nl.example.com"}))
	if d, _ := f.s.Setting(f.ctx, SettingWebDomain, ""); d != "" {
		t.Fatalf("one domain, web.domain %q", d)
	}

	// A dedicated subscription IP keeps the panel on the subscription domain.
	f = newFixture(t)
	must(f.s.SetupAllInOne(f.ctx, ActorCLI, SetupInput{Domain: "nl.example.com", SubDomain: "sub.example.com", VPNIP: "203.0.113.12", SubIP: "203.0.113.10"}))
	if d, _ := f.s.Setting(f.ctx, SettingWebDomain, ""); d != "" {
		t.Fatalf("dedicated sub IP, web.domain %q", d)
	}
}

// At the domain root the panel's route must precede the subscription catch-all, otherwise
// Caddy would send the panel's secret path to subscriptions too.
func TestCaddyRootPrefixPanelFirst(t *testing.T) {
	f := newFixture(t)
	n, _ := f.aio(t, "nl.example.com")
	if err := f.s.EnsureWebDefaults(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.s.SetSetting(f.ctx, ActorCLI, SettingSubPrefix, "/"); err != nil {
		t.Fatal(err)
	}
	ds := must(f.s.DesiredState(f.ctx, n.ID))
	if len(ds.Problems) > 0 {
		t.Fatal(ds.Problems)
	}
	cfg := string(ds.Caddy)
	sub := strings.Index(cfg, `"handler":"subroute"`)
	if sub < 0 {
		t.Fatal(cfg)
	}
	site := cfg[sub:]
	panel := strings.Index(site, `"dial":"`+DefaultWebListen+`"`)
	root := strings.Index(site, `"match":[{"path":["/*"]}]`)
	if panel < 0 || root < 0 || panel > root {
		t.Fatalf("the panel's route must come before the root subscription route (panel %d, root %d):\n%s", panel, root, site)
	}
}

func TestCaddyNodeCustomDecoy(t *testing.T) {
	f := newFixture(t)
	n := must(f.s.EnsureLocalNode(f.ctx, NodeInput{Name: "NL", Country: "nl", Domain: "nl.example.com"}))
	prof := must(f.s.CreateProfile(f.ctx, ActorCLI, ProfileInput{Name: "Reality", TemplateID: "vless-reality-selfsteal"}))
	must(f.s.AttachProfile(f.ctx, ActorCLI, AttachInput{NodeID: n.ID, ProfileID: prof.ID, Values: map[string]any{"DECOY_PATH": "/var/www/site"}}))
	ds := must(f.s.DesiredState(f.ctx, n.ID))
	if len(ds.Problems) > 0 {
		t.Fatal(ds.Problems)
	}
	contains(t, string(ds.Caddy), `"root":"/var/www/site"`)
}

func TestCaddyMergedSiteCustomDecoyWins(t *testing.T) {
	f := newFixture(t)
	n := must(f.s.EnsureLocalNode(f.ctx, NodeInput{Name: "NL", Country: "nl", Domain: "nl.example.com"}))
	hy := must(f.s.CreateProfile(f.ctx, ActorCLI, ProfileInput{Name: "Hy", TemplateID: "hysteria2"}))
	must(f.s.AttachProfile(f.ctx, ActorCLI, AttachInput{NodeID: n.ID, ProfileID: hy.ID}))
	re := must(f.s.CreateProfile(f.ctx, ActorCLI, ProfileInput{Name: "Reality", TemplateID: "vless-reality-selfsteal"}))
	must(f.s.AttachProfile(f.ctx, ActorCLI, AttachInput{NodeID: n.ID, ProfileID: re.ID, Values: map[string]any{"DECOY_PATH": "/var/www/site"}}))
	ds := must(f.s.DesiredState(f.ctx, n.ID))
	if len(ds.Problems) > 0 {
		t.Fatal(ds.Problems)
	}
	var cfg struct {
		Apps struct {
			HTTP struct {
				Servers map[string]struct {
					Listen []string         `json:"listen"`
					Routes []any            `json:"routes"`
					Errors map[string][]any `json:"errors"`
				} `json:"servers"`
			} `json:"http"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(ds.Caddy, &cfg); err != nil {
		t.Fatal(err)
	}
	// fileRoots lists file_server roots in the order Caddy tries them (routes, then subroutes).
	var fileRoots func(routes []any) []string
	fileRoots = func(routes []any) (out []string) {
		for _, r := range routes {
			hs, _ := r.(map[string]any)["handle"].([]any)
			for _, h := range hs {
				hm := h.(map[string]any)
				switch hm["handler"] {
				case "file_server":
					out = append(out, hm["root"].(string))
				case "subroute":
					sub, _ := hm["routes"].([]any)
					out = append(out, fileRoots(sub)...)
				}
			}
		}
		return out
	}
	found := false
	for _, srv := range cfg.Apps.HTTP.Servers {
		if !slices.Contains(srv.Listen, "127.0.0.1:8443") {
			continue
		}
		found = true
		// The first file_server is the served page; the built-in decoy stays behind it.
		if roots := fileRoots(srv.Routes); len(roots) == 0 || roots[0] != "/var/www/site" {
			t.Fatalf("served roots %v, want the custom one first", roots)
		}
		if roots := fileRoots(srv.Errors["routes"]); len(roots) != 1 || roots[0] != "/var/www/site" {
			t.Fatalf("error roots %v, want the custom one", roots)
		}
	}
	if !found {
		t.Fatalf("no 8443 server:\n%s", ds.Caddy)
	}
}

// Local node DecoyPath applies to panel and subscription sites as well as node sites,
// and an inbound's DECOY_PATH overrides the node-level path.
func TestCaddyNodeLevelDecoyPathAndPanelSites(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html><h1>Panel</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	n := must(f.s.EnsureLocalNode(f.ctx, NodeInput{Name: "Panel", Country: "nl", Domain: "panel.example.com", DecoyPath: &dir}))
	if err := f.s.SetSetting(f.ctx, ActorCLI, SettingSubDomain, "sub.example.com"); err != nil {
		t.Fatal(err)
	}
	re := must(f.s.CreateProfile(f.ctx, ActorCLI, ProfileInput{Name: "Reality", TemplateID: "vless-reality-selfsteal"}))
	ni := must(f.s.AttachProfile(f.ctx, ActorCLI, AttachInput{NodeID: n.ID, ProfileID: re.ID}))

	ds := must(f.s.DesiredState(f.ctx, n.ID))
	if len(ds.Problems) > 0 {
		t.Fatal(ds.Problems)
	}
	if !strings.Contains(string(ds.Caddy), dir) {
		t.Fatalf("local node DecoyPath %q missing from Caddy config:\n%s", dir, ds.Caddy)
	}
	if got := f.s.EffectiveWebDecoyPath(f.ctx); got != dir {
		t.Fatalf("EffectiveWebDecoyPath = %q, want %q", got, dir)
	}
	if got := f.s.EffectiveSubDecoyPath(f.ctx); got != dir {
		t.Fatalf("EffectiveSubDecoyPath = %q, want %q", got, dir)
	}

	// Inbound DECOY_PATH overrides the node-level path for that inbound's site.
	must(f.s.UpdateNodeInbound(f.ctx, ActorCLI, ni.ID, NodeInboundInput{Values: map[string]any{"DECOY_PATH": "/var/www/inbound-only"}}))
	ds = must(f.s.DesiredState(f.ctx, n.ID))
	if len(ds.Problems) > 0 {
		t.Fatal(ds.Problems)
	}
	if !strings.Contains(string(ds.Caddy), `"/var/www/inbound-only"`) {
		t.Fatalf("inbound DECOY_PATH override missing from Caddy config:\n%s", ds.Caddy)
	}
}
