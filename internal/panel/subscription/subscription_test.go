package subscription

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	"github.com/vyto4ka/vynel/internal/xray/xraytest"
)

var actor = service.Actor{Kind: "test"}

type env struct {
	ctx  context.Context
	svc  *service.Service
	h    *Handler
	user *store.User
}

// newEnv builds NL with Reality and XHTTP CDN inbounds and one user with access to both.
func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := service.New(st)
	if err := svc.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	nl, _, err := svc.CreateNode(ctx, actor, service.NodeInput{Name: "Нидерланды", Country: "nl", Domain: "nl.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	main, _ := svc.GroupByName(ctx, "Основная")
	for _, p := range []service.ProfileInput{
		{Name: "Reality", TemplateID: "vless-reality-selfsteal"},
		{Name: "CDN", TemplateID: "vless-xhttp-vkcdn", Values: map[string]any{"UPLINK_PATH": "/upload"}},
	} {
		prof, err := svc.CreateProfile(ctx, actor, p)
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.GrantAccess(ctx, actor, main.ID, store.AccessProfile, prof.ID); err != nil {
			t.Fatal(err)
		}
		in := service.AttachInput{NodeID: nl.ID, ProfileID: prof.ID}
		if p.Name == "CDN" {
			in.Values = map[string]any{"CDN_DOMAIN": "cdn.example.com", "ORIGIN_DOMAIN": "origin.example.com"}
		}
		if _, err := svc.AttachProfile(ctx, actor, in); err != nil {
			t.Fatal(err)
		}
	}
	for k, v := range map[string]string{service.SettingSubDomain: "sub.example.com", service.SettingHWIDLimit: "2"} {
		if err := svc.SetSetting(ctx, actor, k, v); err != nil {
			t.Fatal(err)
		}
	}
	u, err := svc.CreateUser(ctx, actor, service.CreateUserInput{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	return &env{ctx: ctx, svc: svc, h: NewHandler(svc, nil), user: u}
}

func (e *env) get(path, ua, hwid string, extra ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("User-Agent", ua)
	if hwid != "" {
		req.Header.Set("x-hwid", hwid)
		req.Header.Set("x-device-model", "Pixel 8")
	}
	for i := 0; i+1 < len(extra); i += 2 {
		req.Header.Set(extra[i], extra[i+1])
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *env) path() string { return service.DefaultSubPrefix + e.user.SubToken }

func links(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(rec.Body.String())
	if err != nil {
		t.Fatalf("not base64: %q", rec.Body.String())
	}
	return strings.Split(string(raw), "\n")
}

func TestBase64WithBothTemplates(t *testing.T) {
	e := newEnv(t)
	rec := e.get(e.path(), "Happ/3.1.0", "hw-1")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	ls := links(t, rec)
	if len(ls) != 2 {
		t.Fatalf("links %v", ls)
	}
	reality, cdn := ls[0], ls[1]
	for _, want := range []string{"vless://" + e.user.UUID + "@nl.example.com:443", "security=reality", "sni=nl.example.com", "fp=firefox", "flow=xtls-rprx-vision", "pbk=", "sid=", "#%F0%9F%87%B3%F0%9F%87%B1"} {
		if !strings.Contains(reality, want) {
			t.Fatalf("reality link lacks %q: %s", want, reality)
		}
	}
	for _, want := range []string{"@cdn.example.com:443", "type=xhttp", "security=tls", "path=%2Fupload%2F", "mode=packet-up", "alpn=h2%2Chttp%2F1.1", "extra=", "xmux", "uplinkDataPlacement"} {
		if !strings.Contains(cdn, want) {
			t.Fatalf("cdn link lacks %q: %s", want, cdn)
		}
	}
	h := rec.Header()
	if !strings.HasPrefix(h.Get("Subscription-Userinfo"), "upload=0; download=0; total=0; expire=") || h.Get("Profile-Update-Interval") != "12" {
		t.Fatalf("headers %v", h)
	}
}

func TestHWIDLimitAndMissing(t *testing.T) {
	e := newEnv(t)
	if ls := links(t, e.get(e.path(), "Happ/3", "")); !strings.Contains(ls[0], StubUUID) {
		t.Fatalf("missing hwid must give a stub: %v", ls)
	}
	e.get(e.path(), "Happ/3", "hw-1")
	e.get(e.path(), "Happ/3", "hw-2")
	ls := links(t, e.get(e.path(), "Happ/3", "hw-3"))
	if len(ls) != 1 || !strings.Contains(ls[0], StubUUID) || !strings.Contains(ls[0], "2%2F2") {
		t.Fatalf("third device must be refused with a reason: %v", ls)
	}
	if ls := links(t, e.get(e.path(), "Happ/3", "hw-1")); len(ls) != 2 {
		t.Fatal("a known device keeps working")
	}
	devs, _ := e.svc.Devices(e.ctx, e.user.ID)
	if len(devs) != 2 || devs[0].Model != "Pixel 8" {
		t.Fatalf("devices %+v", devs)
	}
	// Freeing a slot admits the new device.
	if err := e.svc.DeleteDevice(e.ctx, actor, e.user.ID, devs[0].ID); err != nil {
		t.Fatal(err)
	}
	if ls := links(t, e.get(e.path(), "Happ/3", "hw-3")); len(ls) != 2 {
		t.Fatal("freed slot not reused")
	}
	// The checkbox that lets clients without HWID through.
	if err := e.svc.SetSetting(e.ctx, actor, service.SettingHWIDAllowNone, "true"); err != nil {
		t.Fatal(err)
	}
	if ls := links(t, e.get(e.path(), "v2rayNG/1.9", "")); len(ls) != 2 {
		t.Fatal("allow_missing must admit clients without HWID")
	}
}

func TestStubsForInactiveUsers(t *testing.T) {
	e := newEnv(t)
	past := time.Now().Add(-time.Hour)
	if _, err := e.svc.SetUserExpiry(e.ctx, actor, e.user.ID, &past); err != nil {
		t.Fatal(err)
	}
	ls := links(t, e.get(e.path(), "Happ/3", "hw-1"))
	if len(ls) != 1 || !strings.Contains(ls[0], StubUUID) || !strings.Contains(ls[0], "%D0%B8%D1%81%D1%82%D0%B5%D0%BA%D0%BB%D0%B0") { // "истекла"
		t.Fatalf("expired stub: %v", ls)
	}
}

func TestFormatsByUserAgent(t *testing.T) {
	e := newEnv(t)
	// Mihomo gets only the Reality point (no XHTTP CDN extra support).
	rec := e.get(e.path(), "clash-verge/v2.0 mihomo", "hw-1")
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(rec.Body.Bytes(), &doc); err != nil || len(doc.Proxies) != 1 {
		t.Fatalf("mihomo: %v %s", err, rec.Body.String())
	}
	if p := doc.Proxies[0]; p["servername"] != "nl.example.com" || p["reality-opts"] == nil {
		t.Fatalf("mihomo proxy %v", p)
	}
	// sing-box
	rec = e.get(e.path(), "SFA/1.11.0 (sing-box 1.11.0)", "hw-1")
	var sb map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &sb); err != nil || len(sb["outbounds"].([]any)) != 3 {
		t.Fatalf("sing-box: %v %s", err, rec.Body.String())
	}
	// Explicit format in the URL wins over the UA.
	rec = e.get(e.path()+"/json", "Happ/3", "hw-1")
	var xs []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &xs); err != nil || len(xs) != 2 {
		t.Fatalf("xray json: %v %s", err, rec.Body.String())
	}
	// Browser gets the page, not configs (and no HWID needed).
	rec = e.get(e.path(), "Mozilla/5.0 (iPhone)", "", "Accept", "text/html")
	if body := rec.Body.String(); !strings.Contains(body, "happ://add/https://sub.example.com/s/") || strings.Contains(body, "vless://") {
		t.Fatalf("page: %s", body)
	}
}

func TestXrayJSONAcceptedByXray(t *testing.T) {
	if os.Getenv("XRAY_BIN") == "" {
		t.Skip("XRAY_BIN not set")
	}
	bin := xraytest.Binary(t)
	e := newEnv(t)
	rec := e.get(e.path()+"/json", "x", "hw-1")
	var configs []json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &configs); err != nil {
		t.Fatal(err)
	}
	for _, c := range configs {
		if err := bin.Test(context.Background(), c); err != nil {
			t.Fatalf("xray rejects the client config: %v\n%s", err, c)
		}
	}
}

func TestUnknownTokenLooksLikeAWebsite(t *testing.T) {
	e := newEnv(t)
	e.h.MaxMisses = 3
	for _, p := range []string{"/", "/s/wrong", "/admin", "/s/"} {
		rec := e.get(p, "curl/8", "")
		if rec.Code != 404 || !strings.Contains(rec.Body.String(), "404") || strings.Contains(rec.Body.String(), "vless") {
			t.Fatalf("%s: %d %s", p, rec.Code, rec.Body.String())
		}
	}
	// After too many misses the IP is banned even with a valid token.
	if rec := e.get(e.path(), "Happ/3", "hw-1"); rec.Code != 404 {
		t.Fatalf("banned IP got %d", rec.Code)
	}
}
