package webapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

func TestNodePrivateFieldsAdminOnly(t *testing.T) {
	c, svc, pw := newServer(t)
	if w := c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if err := svc.SetSetting(t.Context(), service.ActorCLI, service.SettingGatewayAddr, "panel.example.com:9443"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetSetting(t.Context(), service.ActorCLI, service.SettingGatewaySNI, "panel.example.com"); err != nil {
		t.Fatal(err)
	}
	c.srv.cfg.CAFingerprint = func() string { return "test-ca" }
	w := c.do("POST", "/api/nodes", `{"name":"Public name","manual":true,"note":"private-rack-729","xrayRelease":"26.9.30"}`, true)
	if w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var joined struct {
		ID      int64  `json:"id"`
		Command string `json:"command"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &joined); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(joined.Command, " --xray-version v26.9.30") || strings.Contains(joined.Command, "private-rack") {
		t.Fatalf("install command: %q", joined.Command)
	}
	path := "/api/nodes/" + itoa(joined.ID)
	if w := c.do("PATCH", path, `{"enabled":false}`, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = c.do("GET", "/api/nodes", "", false)
	var nodes []Node
	if err := json.Unmarshal(w.Body.Bytes(), &nodes); err != nil || len(nodes) != 1 {
		t.Fatalf("nodes: %s %v", w.Body.String(), err)
	}
	if nodes[0].Note != "private-rack-729" || nodes[0].XrayRelease != "v26.9.30" || nodes[0].Enabled || !nodes[0].Manual {
		t.Fatalf("PATCH cleared fields: %+v", nodes[0])
	}
	tok := apiToken(t, svc, service.ScopeNodesRead, service.ScopeEventsRead)
	for _, path := range []string{"/nodes", "/nodes/" + itoa(joined.ID), "/events"} {
		w := apiCall(t, c, tok, "GET", path, "")
		if w.Code != 200 || strings.Contains(w.Body.String(), "private-rack-729") || strings.Contains(w.Body.String(), "xrayRelease") {
			t.Fatalf("public %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if w := c.do("PATCH", path, `{"note":"","xrayRelease":"latest"}`, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	n, err := svc.Node(t.Context(), joined.ID)
	if err != nil || n.Note != "" || n.XrayRelease != "" || !n.Manual {
		t.Fatalf("clear: %+v %v", n, err)
	}
	c.cookie = nil
	if w := c.do("GET", "/api/nodes", "", false); w.Code != 401 || strings.Contains(w.Body.String(), "private-rack-729") {
		t.Fatalf("unauthenticated: %d %s", w.Code, w.Body.String())
	}
}
