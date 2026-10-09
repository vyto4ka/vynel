package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vyto4ka/vynel/internal/caddyconf"
	"github.com/vyto4ka/vynel/internal/panel/store"
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
