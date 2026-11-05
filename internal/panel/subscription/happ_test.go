package subscription

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

func decodeHapp(t *testing.T, link string) map[string]any {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, "happ://routing/onadd/"))
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHappRoutingSubscription(t *testing.T) {
	e := newEnv(t)
	r := service.DefaultClientRouting()
	r.BlockAds = true
	r.Direct = []string{"example.com", "192.0.2.0/24", "process:game.exe"}
	r.Proxy = []string{"full:example.ru", "geoip:nl"}
	r.Block = []string{"keyword:ads", "198.51.100.1"}
	if err := e.svc.SetClientRouting(e.ctx, actor, &r); err != nil {
		t.Fatal(err)
	}
	w := e.get(e.path(), "Happ/4.1.0", "phone")
	p := decodeHapp(t, w.Header().Get("Routing"))
	for key, want := range map[string][]string{
		"DirectSites": {"geosite:category-ru", "domain:ru", "domain:su", "domain:xn--p1ai", "domain:example.com"},
		"DirectIp":    {"geoip:private", "192.0.2.0/24", "geoip:ru"},
		"ProxySites":  {"full:example.ru"}, "ProxyIp": {"geoip:nl"},
		"BlockSites": {"geosite:category-ads-all", "keyword:ads"}, "BlockIp": {"198.51.100.1"},
	} {
		var got []string
		for _, v := range p[key].([]any) {
			got = append(got, v.(string))
		}
		if !slices.Equal(got, want) {
			t.Fatalf("%s = %v, want %v", key, got, want)
		}
	}
	if p["DomesticDNSIP"] != "77.88.8.8" || p["RemoteDNSDomain"] != "https://1.1.1.1/dns-query" {
		t.Fatalf("DNS: %v", p)
	}
	if len(decodeB64Links(t, w.Body.String())) == 0 {
		t.Fatal("no servers")
	}
	r.RUDirect, r.BlockAds = false, false
	r.Direct = nil
	if err := e.svc.SetClientRouting(e.ctx, actor, &r); err != nil {
		t.Fatal(err)
	}
	p2 := decodeHapp(t, e.get(e.path(), "Happ/4.1.0", "phone").Header().Get("Routing"))
	if p2["Name"] != p["Name"] || len(p2["DirectSites"].([]any)) != 0 {
		t.Fatalf("profile not updated: %v", p2)
	}
	for _, req := range []struct{ path, ua string }{{e.path() + "/json", "Happ/4"}, {e.path(), "karing/1.2"}, {e.path(), "v2rayNG/2"}} {
		if got := e.get(req.path, req.ua, "phone").Header().Get("Routing"); got != "" {
			t.Fatalf("unexpected routing for %s %s", req.ua, req.path)
		}
	}
	// A manual profile or disabling command is never replaced by the generated one.
	for _, value := range []string{"happ://routing/off", "happ://routing/add/eyJOYW1lIjoiT3duIn0="} {
		if err := e.svc.SetSubHeaders(e.ctx, actor, []service.SubHeader{{Name: "Routing", Value: value, Enabled: true}}); err != nil {
			t.Fatal(err)
		}
		if got := e.get(e.path(), "Happ/4", "phone").Header().Get("Routing"); got != value {
			t.Fatalf("manual header replaced: %s", got)
		}
	}
}

func TestHappDNSConversion(t *testing.T) {
	r := service.DefaultClientRouting()
	r.DNSDirect = []string{"localhost"}
	r.DNSProxy = []string{"https://unresolved.example/dns-query", "https://dns.google/dns-query"}
	p := decodeHapp(t, HappRouting(r))
	if _, ok := p["DomesticDNSIP"]; ok {
		t.Fatal("localhost became an invalid DNS IP")
	}
	if p["RemoteDNSIP"] != "8.8.8.8" || p["RemoteDNSType"] != "DoH" {
		t.Fatalf("DNS: %v", p)
	}
}

func TestKaringImportHWID(t *testing.T) {
	e := newEnv(t)
	p := service.DefaultSubPage()
	for i := range p.Apps {
		if p.Apps[i].ID == "karing" {
			p.Apps[i].Enabled = true
			// An existing page with the old built-in link is upgraded too.
			p.Apps[i].Link = "karing://install-config?url={url_enc}&name={title_url}"
		}
	}
	if err := e.svc.SetSubPage(e.ctx, actor, &p); err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.SubPage(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range p.Apps {
		if a.ID != "karing" {
			continue
		}
		vars := UserVars(e.user, "https://sub.example/s/token?a=b&c=d", service.SubBasics{Title: "VPN & test"}, time.Now())
		u, err := url.Parse(vars.Expand(a.Link))
		if err != nil {
			t.Fatal(err)
		}
		if u.Query().Get("x-hwid") != "true" || u.Query().Get("url") != vars["url"] || u.Query().Get("name") != "VPN & test" {
			t.Fatalf("import: %s", u)
		}
	}
}

func TestClientMetadataAndDisabledSubscription(t *testing.T) {
	for _, ua := range []string{"Happ/4.1.0", "karing/1.2.24"} {
		t.Run(ua, func(t *testing.T) {
			e := newEnv(t)
			expiry := time.Now().Add(24 * time.Hour).Truncate(time.Second)
			if _, err := e.svc.SetUserExpiry(e.ctx, actor, e.user.ID, &expiry); err != nil {
				t.Fatal(err)
			}
			limit := int64(1024)
			if _, err := e.svc.SetUserTrafficLimit(e.ctx, actor, e.user.ID, &limit); err != nil {
				t.Fatal(err)
			}
			for _, hours := range []int{1, 6, 0} {
				if err := e.svc.SetSubBasics(e.ctx, actor, service.SubBasics{Title: "VPN", UpdateHours: hours}); err != nil {
					t.Fatal(err)
				}
				w := e.get(e.path(), ua, "phone")
				want := fmt.Sprint(hours)
				if hours == 0 {
					want = ""
				}
				if w.Header().Get("Profile-Update-Interval") != want {
					t.Fatalf("interval %d: %v", hours, w.Header())
				}
				info := w.Header().Get("Subscription-Userinfo")
				if !strings.Contains(info, "total=1024") || !strings.Contains(info, fmt.Sprintf("expire=%d", expiry.Unix())) {
					t.Fatalf("metadata: %s", info)
				}
			}
			if _, err := e.svc.SetUserEnabled(e.ctx, actor, e.user.ID, false); err != nil {
				t.Fatal(err)
			}
			w := e.get(e.path(), ua, "phone")
			if w.Code != 200 || strings.Contains(w.Body.String(), e.user.UUID) {
				t.Fatalf("disabled user received credentials: %s", w.Body.String())
			}
		})
	}
}
