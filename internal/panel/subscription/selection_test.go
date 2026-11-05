package subscription

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
)

func TestKaringWithoutHWIDKeepsRouting(t *testing.T) {
	e := newEnv(t)
	if err := e.svc.SetSetting(e.ctx, actor, service.SettingHWIDLimit, "0"); err != nil {
		t.Fatal(err)
	}
	rules := service.DefaultSubRules()
	for i := range rules {
		if rules[i].Note == "Karing" {
			rules[i].Template = "log-level: silent\nproxies: []\nrules: [MATCH,DIRECT]\n"
		}
	}
	if err := e.svc.SetSubRules(e.ctx, actor, rules); err != nil {
		t.Fatal(err)
	}
	w := e.get(e.path(), "karing/1.2.24", "")
	if !strings.Contains(w.Body.String(), "proxies:") || !strings.Contains(w.Body.String(), "log-level: silent") {
		t.Fatalf("Karing lost its format/template: %s", w.Body.String())
	}
	if ds, _ := e.svc.Devices(e.ctx, e.user.ID); len(ds) != 0 {
		t.Fatal("registered a device without HWID")
	}
	// A device limit must still deny requests without HWID.
	if err := e.svc.SetSetting(e.ctx, actor, service.SettingHWIDLimit, "1"); err != nil {
		t.Fatal(err)
	}
	w = e.get(e.path(), "karing/1.2.24", "")
	if strings.Contains(w.Body.String(), e.user.UUID) || !strings.Contains(w.Body.String(), StubUUID) || w.Header().Get("x-hwid-not-supported") != "true" {
		t.Fatalf("HWID limit bypassed: %s", w.Body.String())
	}
}

func TestInspectionMatchesDeliveryAndDoesNotRegister(t *testing.T) {
	e := newEnv(t)
	for _, hwid := range []string{"", "new-device"} {
		req := httptest.NewRequest("GET", e.path(), nil)
		req.Header.Set("User-Agent", "karing/1.2.24")
		req.Header.Set("x-hwid", hwid)
		before, _ := e.svc.Devices(e.ctx, e.user.ID)
		inspection, err := Inspect(e.ctx, e.svc, e.user, req, "", FormatMihomo)
		if err != nil {
			t.Fatal(err)
		}
		after, _ := e.svc.Devices(e.ctx, e.user.ID)
		if len(after) != len(before) {
			t.Fatal("inspection registered a device")
		}
		actual, err := selectHosts(e.ctx, e.svc, e.user, req, FormatMihomo, true, true)
		if err != nil {
			t.Fatal(err)
		}
		if inspection.Allowed != actual.Allowed || inspection.Reason != actual.Reason || inspection.Format != actual.Format || len(inspection.Hosts) != len(actual.Hosts) {
			t.Fatalf("inspection %+v differs from actual %+v", inspection, actual)
		}
	}
	// A full device table denies an unknown HWID but permits an existing one.
	if err := e.svc.SetSetting(e.ctx, actor, service.SettingHWIDLimit, "1"); err != nil {
		t.Fatal(err)
	}
	for _, hwid := range []string{"new-device", "other-device"} {
		req := httptest.NewRequest("GET", e.path(), nil)
		req.Header.Set("x-hwid", hwid)
		req.Header.Set("User-Agent", "Karing/1.2")
		s, err := Inspect(e.ctx, e.svc, e.user, req, "", FormatMihomo)
		if err != nil {
			t.Fatal(err)
		}
		if s.HWID.OK != (hwid == "new-device") || s.HWID.Full != (hwid != "new-device") {
			t.Fatalf("wrong device verdict: %+v", s.HWID)
		}
	}
}

func TestInspectionExplainsFilteredServers(t *testing.T) {
	e := newEnv(t)
	if err := e.svc.SetSetting(e.ctx, actor, service.SettingHWIDLimit, "0"); err != nil {
		t.Fatal(err)
	}
	raw := "vmess://eyJhZGQiOiJ4In0=#External"
	ext, err := e.svc.CreateExternalSub(e.ctx, actor, service.ExternalSubInput{Name: "raw", URL: raw, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.PutGrant(e.ctx, actor, service.GrantInput{UserID: e.user.ID, Kind: store.GrantExternal, RefID: ext.ID}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", e.path(), nil)
	req.Header.Set("User-Agent", "Karing/1.2")
	s, err := Inspect(e.ctx, e.svc, e.user, req, "", FormatMihomo)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]int{}
	for _, server := range s.Servers {
		found[server.Excluded]++
	}
	if found["xhttp"] != 1 || found["raw"] != 1 || found[""] != 1 {
		t.Fatalf("exclusions: %+v", s.Servers)
	}
	data, _ := json.Marshal(s)
	if strings.Contains(string(data), e.user.UUID) || strings.Contains(string(data), raw) || strings.Contains(string(data), "PublicKey") {
		t.Fatalf("inspection exposed credentials: %s", data)
	}
	// Explicit links keep the external raw server, but still exclude Karing's XHTTP.
	s, err = Inspect(e.ctx, e.svc, e.user, req, "base64", FormatBase64)
	if err != nil || len(s.Hosts) != 2 {
		t.Fatalf("explicit links: %v %+v", err, s.Servers)
	}
}
