package webapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

func TestSubscriptionClientDraft(t *testing.T) {
	c, svc, pw := newServer(t)
	if w := c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true); w.Code != 200 {
		t.Fatal("login")
	}
	draft := `{"userAgent":"Happ/4.1.0","basics":{"title":"Test","updateHours":3},"routing":{"ruDirect":false,"dnsDirect":["9.9.9.9"],"dnsProxy":[],"torrent":"proxy","direct":["draft.example"],"proxy":[],"block":[]}}`
	w := c.do("POST", "/api/subscription/test", draft, true)
	var data struct {
		RoutingProfile map[string]any `json:"routingProfile"`
		Headers        []struct{ Name, Value string }
		AppLinks       []struct{ Name, Value string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil || w.Code != 200 {
		t.Fatalf("preview: %d %s %v", w.Code, w.Body.String(), err)
	}
	sites, _ := json.Marshal(data.RoutingProfile["DirectSites"])
	if string(sites) != `["domain:draft.example"]` || data.RoutingProfile["DomesticDNSIP"] != "9.9.9.9" {
		t.Fatalf("draft not used: %v", data.RoutingProfile)
	}
	interval := ""
	for _, h := range data.Headers {
		if h.Name == "Profile-Update-Interval" {
			interval = h.Value
		}
	}
	if interval != "3" {
		t.Fatalf("draft interval: %s", interval)
	}
	saved, err := svc.ClientRouting(context.Background())
	if err != nil || !saved.RUDirect || len(saved.Direct) != 0 {
		t.Fatalf("preview changed saved routing: %v %v", saved, err)
	}

	w = c.do("POST", "/api/subscription/test", `{"userAgent":"karing/1.2.24"}`, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"format":"karing"`) || !strings.Contains(w.Body.String(), "x-hwid=true") {
		t.Fatalf("Karing preview: %s", w.Body.String())
	}
	// Custom import links are preserved in the draft preview.
	w = c.do("POST", "/api/subscription/test", `{"userAgent":"karing/1.2.24","page":{"apps":[{"id":"karing","name":"Karing","link":"karing://install-config?url={url_enc}&x-hwid=false"}]}}`, true)
	if !strings.Contains(w.Body.String(), "x-hwid=false") {
		t.Fatalf("custom link replaced: %s", w.Body.String())
	}
}

func TestSubscriptionInspectionAndKaringDNS(t *testing.T) {
	c, svc, pw := newServer(t)
	if w := c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true); w.Code != 200 {
		t.Fatal("login")
	}
	ctx := context.Background()
	u, err := svc.CreateUser(ctx, service.ActorCLI, service.CreateUserInput{Username: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetSetting(ctx, service.ActorCLI, service.SettingHWIDLimit, "1"); err != nil {
		t.Fatal(err)
	}
	w := c.do("POST", "/api/subscription/test", fmt.Sprintf(`{"userId":%d,"userAgent":"Karing/1.2.24"}`, u.ID), true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"allowed":false`) || !strings.Contains(w.Body.String(), `"noHwid":true`) || !strings.Contains(w.Body.String(), "x-hwid-not-supported") {
		t.Fatalf("missing HWID: %s", w.Body.String())
	}
	w = c.do("POST", "/api/subscription/test", fmt.Sprintf(`{"userId":%d,"userAgent":"Karing/1.2.24","hwid":"preview-only"}`, u.ID), true)
	if w.Code != 200 {
		t.Fatalf("inspection: %s", w.Body.String())
	}
	devices, _ := svc.Devices(ctx, u.ID)
	after, _ := svc.User(ctx, u.ID)
	if len(devices) != 0 || after.SubLastAt != nil {
		t.Fatal("inspection modified device/fetch history")
	}
	for _, body := range []string{`{"userAgent":"Karing","ip":"not-an-ip"}`, `{"userAgent":"Karing","format":"invalid"}`} {
		if w := c.do("POST", "/api/subscription/test", body, true); w.Code != 400 {
			t.Fatalf("invalid inspection accepted: %s", w.Body.String())
		}
	}
	w = c.do("PUT", "/api/subscription", `{"basics":{"title":"DNS test","updateHours":4,"karingDns":[" 8.8.8.8 ","https://dns.google/dns-query"]}}`, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"karingDns":["udp://8.8.8.8","https://dns.google/dns-query"]`) {
		t.Fatalf("DNS save: %s", w.Body.String())
	}
	w = c.do("POST", "/api/subscription/test", `{"userAgent":"Karing/1.2.24"}`, true)
	if !strings.Contains(w.Body.String(), "outbound-dns=udp%3A%2F%2F8.8.8.8%2Chttps%3A%2F%2Fdns.google%2Fdns-query") {
		t.Fatalf("DNS import link: %s", w.Body.String())
	}
	w = c.do("PUT", "/api/subscription", `{"basics":{"title":"must not save","updateHours":4,"karingDns":["file:///bad"]}}`, true)
	if w.Code != 400 || svc.SubBasicsGet(ctx).Title != "DNS test" {
		t.Fatal("invalid DNS changed settings")
	}
}

func TestSubscriptionTestBodyAndDecodedUris(t *testing.T) {
	c, svc, pw := newServer(t)
	if w := c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true); w.Code != 200 {
		t.Fatal("login")
	}
	ctx := context.Background()
	n, _, err := svc.CreateNode(ctx, service.ActorCLI, service.NodeInput{Name: "NL", Country: "nl", Domain: "nl.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	prof, err := svc.CreateProfile(ctx, service.ActorCLI, service.ProfileInput{Name: "R", TemplateID: "vless-reality-selfsteal"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AttachProfile(ctx, service.ActorCLI, service.AttachInput{NodeID: n.ID, ProfileID: prof.ID}); err != nil {
		t.Fatal(err)
	}
	u, err := svc.CreateUser(ctx, service.ActorCLI, service.CreateUserInput{Username: "simuser"})
	if err != nil {
		t.Fatal(err)
	}

	type testResp struct {
		Format      string `json:"format"`
		Body        string `json:"body"`
		DecodedUris string `json:"decodedUris"`
	}

	// Base64 format: returns base64 body AND decodedUris with vless://
	w := c.do("POST", "/api/subscription/test", fmt.Sprintf(`{"userId":%d,"format":"base64"}`, u.ID), true)
	var b64 testResp
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &b64) != nil {
		t.Fatalf("base64 test: %d %s", w.Code, w.Body.String())
	}
	if b64.Body == "" || !strings.Contains(b64.DecodedUris, "vless://") || !strings.Contains(b64.DecodedUris, u.UUID) {
		t.Fatalf("base64 body/decodedUris unexpected: %+v", b64)
	}

	// Mihomo YAML
	w = c.do("POST", "/api/subscription/test", fmt.Sprintf(`{"userId":%d,"format":"mihomo"}`, u.ID), true)
	var mihomo testResp
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &mihomo) != nil || !strings.Contains(mihomo.Body, "proxies:") || mihomo.DecodedUris != "" {
		t.Fatalf("mihomo test: %d %+v", w.Code, mihomo)
	}

	// sing-box JSON
	w = c.do("POST", "/api/subscription/test", fmt.Sprintf(`{"userId":%d,"format":"singbox"}`, u.ID), true)
	var sb testResp
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &sb) != nil || !strings.Contains(sb.Body, `"outbounds"`) || sb.DecodedUris != "" {
		t.Fatalf("singbox test: %d %+v", w.Code, sb)
	}

	// Xray JSON
	w = c.do("POST", "/api/subscription/test", fmt.Sprintf(`{"userId":%d,"format":"xray"}`, u.ID), true)
	var xray testResp
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &xray) != nil || !strings.Contains(xray.Body, `"outbounds"`) || xray.DecodedUris != "" {
		t.Fatalf("xray test: %d %+v", w.Code, xray)
	}

	// Karing JSON
	w = c.do("POST", "/api/subscription/test", fmt.Sprintf(`{"userId":%d,"format":"karing"}`, u.ID), true)
	var karing testResp
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &karing) != nil || !strings.Contains(karing.Body, `"outbounds"`) || karing.DecodedUris != "" {
		t.Fatalf("karing test: %d %+v", w.Code, karing)
	}

	// HTML format: body must stay empty (preview uses iframe)
	w = c.do("POST", "/api/subscription/test", fmt.Sprintf(`{"userId":%d,"format":"html"}`, u.ID), true)
	var htmlResp testResp
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &htmlResp) != nil || htmlResp.Body != "" || htmlResp.DecodedUris != "" {
		t.Fatalf("html test should have empty body: %d %+v", w.Code, htmlResp)
	}
}
