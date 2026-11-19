package webapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/xray/xraytest"
)

func TestXrayBaseConfigAPI(t *testing.T) {
	c, svc, pw := newServer(t)
	if w := c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true); w.Code != 200 {
		t.Fatal("login")
	}
	n, _, err := svc.CreateNode(context.Background(), service.ActorCLI, service.NodeInput{Name: "Нидерланды", Country: "nl", Domain: "nl.example.com"})
	if err != nil {
		t.Fatal(err)
	}

	w := c.do("GET", "/api/xray/base", "", false)
	var got struct {
		JSON       string   `json:"json"`
		Default    string   `json:"default"`
		Customized bool     `json:"customized"`
		PanelKeys  []string `json:"panelKeys"`
		Tester     bool     `json:"tester"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.JSON != got.Default || got.Customized || got.Tester {
		t.Fatalf("get: %d %s", w.Code, w.Body.String())
	}

	put := func(method, path, cfg string) (int, string) {
		body, _ := json.Marshal(map[string]string{"json": cfg})
		w := c.do(method, path, string(body), true)
		return w.Code, w.Body.String()
	}

	// Broken JSON, a non-object and the sections the panel owns are refused with the reason.
	for cfg, want := range map[string]string{
		`{"outbounds": [`:             "unexpected end",
		`[]`:                          "JSON-объект",
		``:                            "пустой",
		`{"inbounds": []}`:            "inbounds",
		`{"api": {"tag": "x"}}`:       "«api»",
		`{"policy": {}, "stats": {}}`: "панель задаёт сама",
	} {
		code, body := put("PUT", "/api/xray/base", cfg)
		if code != 400 || !strings.Contains(body, want) {
			t.Fatalf("%q: %d %s (want %q)", cfg, code, body, want)
		}
		if code, body := put("POST", "/api/xray/base/check", cfg); code != 200 || !strings.Contains(body, `"ok":false`) {
			t.Fatalf("check %q: %d %s", cfg, code, body)
		}
	}

	custom := `{
  "outbounds": [
    {"tag": "DIRECT", "protocol": "freedom"},
    {"tag": "WARP", "protocol": "socks", "settings": {"servers": [{"address": "127.0.0.1", "port": 40000}]}}
  ],
  "routing": {"rules": [{"domain": ["geosite:netflix"], "outboundTag": "WARP"}, {"network": "tcp,udp", "outboundTag": "DIRECT"}]}
}`
	if code, body := put("POST", "/api/xray/base/check", custom); code != 200 || !strings.Contains(body, `"ok":true`) || !strings.Contains(body, `"nodes":1`) {
		t.Fatalf("check: %d %s", code, body)
	}
	if code, body := put("PUT", "/api/xray/base", custom); code != 200 {
		t.Fatalf("save: %d %s", code, body)
	}
	if w := c.do("GET", "/api/xray/base", "", false); !strings.Contains(w.Body.String(), `"customized":true`) || !strings.Contains(w.Body.String(), "WARP") {
		t.Fatalf("saved config not returned: %s", w.Body.String())
	}

	// The node gets the saved sections, with the panel's own ones added around them.
	ds, err := svc.DesiredState(context.Background(), n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ds.Config), `"WARP"`) || !strings.Contains(string(ds.Config), `"api"`) {
		t.Fatalf("node config: %s", ds.Config)
	}

	// The preview takes an unsaved base config; the stored one is untouched.
	body, _ := json.Marshal(map[string]string{"json": `{"outbounds": [{"tag": "DIRECT", "protocol": "freedom"}, {"tag": "TRY", "protocol": "blackhole"}]}`})
	w = c.do("POST", "/api/xray/nodes/"+itoa(n.ID)+"/config", string(body), true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"TRY"`) || strings.Contains(w.Body.String(), `"WARP"`) || !strings.Contains(w.Body.String(), `"kind":"api"`) {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}
	if w := c.do("POST", "/api/xray/nodes/999/config", "", true); w.Code != 404 {
		t.Fatalf("preview of a missing node: %d", w.Code)
	}

	// Reset to the built-in config.
	if code, body := put("PUT", "/api/xray/base", got.Default); code != 200 {
		t.Fatalf("reset: %d %s", code, body)
	}
	if w := c.do("GET", "/api/xray/base", "", false); !strings.Contains(w.Body.String(), `"customized":false`) {
		t.Fatalf("not reset: %s", w.Body.String())
	}
}

func TestXrayBaseConfigTester(t *testing.T) {
	c, _, pw := newServer(t)
	if w := c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true); w.Code != 200 {
		t.Fatal("login")
	}
	if _, _, err := c.srv.svc.CreateNode(context.Background(), service.ActorCLI, service.NodeInput{Name: "A", Country: "nl", Domain: "a.example.com"}); err != nil {
		t.Fatal(err)
	}
	c.srv.cfg.XrayTest = func(_ context.Context, cfg []byte) error {
		if strings.Contains(string(cfg), "BAD") {
			return errors.New("unknown protocol")
		}
		return nil
	}
	body := func(tag string) string {
		b, _ := json.Marshal(map[string]string{"json": `{"outbounds": [{"tag": "DIRECT", "protocol": "freedom"}, {"tag": "` + tag + `", "protocol": "blackhole"}]}`})
		return string(b)
	}
	if w := c.do("POST", "/api/xray/base/check", body("BAD"), true); !strings.Contains(w.Body.String(), `"ok":false`) || !strings.Contains(w.Body.String(), "unknown protocol") {
		t.Fatalf("check: %s", w.Body.String())
	}
	if w := c.do("PUT", "/api/xray/base", body("BAD"), true); w.Code != 400 || !strings.Contains(w.Body.String(), "Xray не принял конфиг") {
		t.Fatalf("save of a rejected config: %d %s", w.Code, w.Body.String())
	}
	if w := c.do("PUT", "/api/xray/base", body("OK"), true); w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
}

// With the real Xray behind the check, a config Xray cannot read is refused and a good one saved.
func TestXrayBaseConfigRealXray(t *testing.T) {
	bin := xraytest.Binary(t)
	c, svc, pw := newServer(t)
	if w := c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true); w.Code != 200 {
		t.Fatal("login")
	}
	if _, _, err := svc.CreateNode(context.Background(), service.ActorCLI, service.NodeInput{Name: "A", Country: "nl", Domain: "a.example.com"}); err != nil {
		t.Fatal(err)
	}
	c.srv.cfg.XrayTest = bin.Test
	body := func(outbound string) string {
		b, _ := json.Marshal(map[string]string{"json": `{"outbounds": [{"tag": "DIRECT", "protocol": "freedom"}, ` + outbound + `]}`})
		return string(b)
	}
	bad := `{"tag": "X", "protocol": "no-such-protocol"}`
	if w := c.do("POST", "/api/xray/base/check", body(bad), true); !strings.Contains(w.Body.String(), `"ok":false`) {
		t.Fatalf("check of a bad outbound: %s", w.Body.String())
	}
	if w := c.do("PUT", "/api/xray/base", body(bad), true); w.Code != 400 {
		t.Fatalf("saved a config Xray rejects: %d %s", w.Code, w.Body.String())
	}
	good := `{"tag": "WARP", "protocol": "socks", "settings": {"servers": [{"address": "127.0.0.1", "port": 40000}]}}`
	if w := c.do("POST", "/api/xray/base/check", body(good), true); !strings.Contains(w.Body.String(), `"ok":true`) || !strings.Contains(w.Body.String(), `"tested":true`) {
		t.Fatalf("check: %s", w.Body.String())
	}
	if w := c.do("PUT", "/api/xray/base", body(good), true); w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
}

// Before the first node exists the base config alone is what Xray reads.
func TestXrayBaseConfigWithoutNodes(t *testing.T) {
	c, _, pw := newServer(t)
	if w := c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true); w.Code != 200 {
		t.Fatal("login")
	}
	var read int
	c.srv.cfg.XrayTest = func(_ context.Context, cfg []byte) error {
		read++
		if !strings.Contains(string(cfg), `"api"`) {
			t.Errorf("not a whole config: %s", cfg)
		}
		return nil
	}
	w := c.do("POST", "/api/xray/base/check", `{"json":"{\"outbounds\":[{\"tag\":\"DIRECT\",\"protocol\":\"freedom\"}]}"}`, true)
	if !strings.Contains(w.Body.String(), `"ok":true`) || !strings.Contains(w.Body.String(), `"tested":true`) || !strings.Contains(w.Body.String(), `"nodes":0`) || read != 1 {
		t.Fatalf("check: %s (read %d)", w.Body.String(), read)
	}
}

func TestNodeXrayBaseAndCompiledAPI(t *testing.T) {
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
	u, err := svc.CreateUser(ctx, service.ActorCLI, service.CreateUserInput{Username: "bob"})
	if err != nil {
		t.Fatal(err)
	}

	// GET default (inherit)
	w := c.do("GET", "/api/nodes/"+itoa(n.ID)+"/xray-base", "", false)
	var baseResp struct {
		Mode       string `json:"mode"`
		NodeJSON   string `json:"nodeJson"`
		GlobalJSON string `json:"globalJson"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &baseResp) != nil || baseResp.Mode != "inherit" || baseResp.NodeJSON != "" || baseResp.GlobalJSON == "" {
		t.Fatalf("get xray-base: %d %s", w.Code, w.Body.String())
	}

	// Invalid mode / invalid JSON / forbidden section -> 400
	for _, body := range []string{
		`{"mode":"bad","json":"{}"}`,
		`{"mode":"custom","json":"{bad"}`,
		`{"mode":"custom","json":"{\"inbounds\":[]}"}`,
	} {
		if w := c.do("PUT", "/api/nodes/"+itoa(n.ID)+"/xray-base", body, true); w.Code != 400 {
			t.Fatalf("expected 400 for %s, got %d %s", body, w.Code, w.Body.String())
		}
	}

	// PUT custom
	customPayload, _ := json.Marshal(map[string]string{
		"mode": "custom",
		"json": `{"log":{"loglevel":"info","access":"none"},"outbounds":[{"tag":"DIRECT","protocol":"freedom"},{"tag":"NODE_OUT","protocol":"blackhole"}]}`,
	})
	if w := c.do("PUT", "/api/nodes/"+itoa(n.ID)+"/xray-base", string(customPayload), true); w.Code != 200 || !strings.Contains(w.Body.String(), `"mode":"custom"`) {
		t.Fatalf("put custom: %d %s", w.Code, w.Body.String())
	}
	w = c.do("GET", "/api/nodes/"+itoa(n.ID)+"/xray-base", "", false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "NODE_OUT") {
		t.Fatalf("get custom: %d %s", w.Code, w.Body.String())
	}

	// GET compiled (secrets=0 vs secrets=1)
	w = c.do("GET", "/api/nodes/"+itoa(n.ID)+"/compiled?secrets=0", "", false)
	var comp struct {
		XrayConfig      string `json:"xrayConfig"`
		NginxConfig     string `json:"nginxConfig"`
		Revision        string `json:"revision"`
		AppliedRevision string `json:"appliedRevision"`
		LastError       string `json:"lastError"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &comp) != nil {
		t.Fatalf("compiled masked: %d %s", w.Code, w.Body.String())
	}
	if comp.Revision == "" || !strings.Contains(comp.XrayConfig, "NODE_OUT") || !strings.Contains(comp.XrayConfig, `"loglevel": "info"`) {
		t.Fatalf("compiled xrayConfig unexpected: %s", comp.XrayConfig)
	}
	if strings.Contains(comp.XrayConfig, u.UUID) || !strings.Contains(comp.XrayConfig, `"id": "•••"`) || !strings.Contains(comp.XrayConfig, `"privateKey": "•••"`) {
		t.Fatalf("compiled secrets not masked: %s", comp.XrayConfig)
	}
	if comp.NginxConfig != "# nginx is not configured for this node" {
		t.Fatalf("unexpected nginxConfig: %q", comp.NginxConfig)
	}

	w = c.do("GET", "/api/nodes/"+itoa(n.ID)+"/compiled?secrets=1", "", false)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &comp) != nil {
		t.Fatalf("compiled full: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(comp.XrayConfig, u.UUID) || strings.Contains(comp.XrayConfig, `"privateKey": "•••"`) {
		t.Fatalf("compiled secrets=1 should expose real secrets: %s", comp.XrayConfig)
	}

	// PUT inherit
	if w := c.do("PUT", "/api/nodes/"+itoa(n.ID)+"/xray-base", `{"mode":"inherit"}`, true); w.Code != 200 || !strings.Contains(w.Body.String(), `"mode":"inherit"`) {
		t.Fatalf("put inherit: %d %s", w.Code, w.Body.String())
	}

	// Missing node -> 404
	if w := c.do("GET", "/api/nodes/999/xray-base", "", false); w.Code != 404 {
		t.Fatalf("missing node xray-base: %d", w.Code)
	}
	if w := c.do("GET", "/api/nodes/999/compiled", "", false); w.Code != 404 {
		t.Fatalf("missing node compiled: %d", w.Code)
	}
}
