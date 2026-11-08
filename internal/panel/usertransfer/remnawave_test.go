package usertransfer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// A Remnawave panel answers pages of users; the fetch walks them into the transfer format.
func TestFetchRemnawave(t *testing.T) {
	restore := RnSetPrivate(true)
	defer restore()

	// Two pages of two, as GetUsersCommand answers: {response: {users: [...], total}}.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/users", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		start, _ := strconv.Atoi(r.URL.Query().Get("start"))
		size, _ := strconv.Atoi(r.URL.Query().Get("size"))
		if size != 100 {
			t.Errorf("page size: %d", size)
		}
		all := []rnUser{
			{Username: "alice", VlessUUID: "uuid-a", ShortUUID: "short-alice", Status: "ACTIVE", TrafficLimitBytes: 1073741824, TrafficLimitStrat: "MONTH",
				ExpireAt: "2027-01-01T00:00:00Z", HWIDDeviceLimit: i64(3), TelegramID: i64(777), Description: "vip",
				ActiveInternalSquad: []struct {
					Name string `json:"name"`
				}{{Name: "Default"}, {Name: "Team"}},
				UserTraffic: struct {
					UsedTrafficBytes int64 `json:"usedTrafficBytes"`
				}{UsedTrafficBytes: 123456789}},
			{Username: "bob", VlessUUID: "uuid-b", Status: "DISABLED", TrafficLimitBytes: 0, TrafficLimitStrat: "NO_RESET"},
			{Username: "carl", VlessUUID: "uuid-c", Status: "EXPIRED", TrafficLimitStrat: "WEEK"},
			{Username: "dana", VlessUUID: "uuid-d", Status: "LIMITED", TrafficLimitStrat: "MONTH_ROLLING", ActiveInternalSquad: []struct {
				Name string `json:"name"`
			}{{Name: "Default"}}},
		}
		end := start + size
		if end > len(all) {
			end = len(all)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"response": map[string]any{"users": all[start:end], "total": len(all)},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f, err := FetchRemnawave(context.Background(), RemnawavePanel{BaseURL: srv.URL, Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Meta.Source != SourceRemnawave || f.Meta.Users != 4 {
		t.Fatalf("meta: %+v", f.Meta)
	}
	byName := map[string]TransferUser{}
	for _, u := range f.Users {
		byName[u.Username] = u
	}
	a := byName["alice"]
	if a.UUID != "uuid-a" || a.SubToken != "short-alice" || a.Status != "active" || a.ResetStrategy != "month" || a.HWIDLimit == nil || *a.HWIDLimit != 3 {
		t.Errorf("alice: %+v", a)
	}
	if a.ExpireAt == nil || *a.ExpireAt != 1798761600 {
		t.Errorf("alice expireAt: %v", a.ExpireAt)
	}
	if a.TrafficLimitBytes == nil || *a.TrafficLimitBytes != 1073741824 || a.TrafficUsedBytes != 123456789 {
		t.Errorf("alice traffic: %+v", a)
	}
	if len(a.Groups) != 2 || a.Groups[0] != "Default" {
		t.Errorf("alice groups: %v", a.Groups)
	}
	if a.Note != "vip" || a.TelegramID == nil || *a.TelegramID != 777 {
		t.Errorf("alice note/tg: %+v", a)
	}
	if b := byName["bob"]; b.Status != "disabled" || b.ResetStrategy != "no" || b.TrafficLimitBytes != nil {
		t.Errorf("bob: %+v", b)
	}
	if d := byName["dana"]; d.Status != "limited" || d.ResetStrategy != "month" {
		t.Errorf("dana: %+v", d)
	}
}

// Wrong token, an unreachable panel and a bad address are errors, not empty imports.
func TestFetchRemnawaveErrors(t *testing.T) {
	restore := RnSetPrivate(true)
	defer restore()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	if _, err := FetchRemnawave(context.Background(), RemnawavePanel{BaseURL: srv.URL, Token: "wrong"}); err == nil {
		t.Error("wrong token accepted")
	}
	if _, err := FetchRemnawave(context.Background(), RemnawavePanel{BaseURL: "http://127.0.0.1:1", Token: "tok"}); err == nil {
		t.Error("unreachable panel accepted")
	}
	if _, err := FetchRemnawave(context.Background(), RemnawavePanel{BaseURL: "not a url", Token: "tok"}); err == nil {
		t.Error("bad address accepted")
	}
	if _, err := FetchRemnawave(context.Background(), RemnawavePanel{BaseURL: srv.URL, Token: ""}); err == nil {
		t.Error("empty token accepted")
	}
}

// The file of a Remnawave fetch imports: tokens minted or preserved, statuses kept, groups by name.
func TestRemnawaveFileImports(t *testing.T) {
	f := &ExportFile{Meta: ExportMeta{Format: Format, Source: SourceRemnawave, Users: 2}, Users: []TransferUser{
		{Username: "alice", UUID: "uuid-a", Status: "active"},
		{Username: "bob", UUID: "uuid-b", Status: "disabled"},
	}}
	st := testStore(t)
	im := NewImporter(st, ImportOptions{})
	n := 0
	im.NewTokenFn = func() (string, error) { n++; return "minted-token-" + strconv.Itoa(n), nil }
	rep, err := im.Import(context.Background(), f)
	if err != nil || rep.Created != 2 {
		t.Fatalf("import: %v %+v", err, rep)
	}
	u := mustUser(t, st, "alice")
	if u.SubToken == "" {
		t.Errorf("no token minted for a Remnawave user")
	}
	b := mustUser(t, st, "bob")
	if b.Status != "disabled" || !b.Disabled {
		t.Errorf("status/disabled not kept: %+v", b)
	}
}

// A raw JSON dump from Remnawave's /api/users unmarshals directly and imports with
// preserved shortUuid, traffic, auto-created squads, and duplicate TelegramID handling.
func TestRawRemnawaveUnmarshalAndImport(t *testing.T) {
	raw := []byte(`{
		"response": {
			"users": [
				{
					"username": "u1",
					"vlessUuid": "uuid-1",
					"shortUuid": "short-1",
					"status": "ACTIVE",
					"telegramId": 999999,
					"activeInternalSquads": [{"name": "Squad-A"}],
					"userTraffic": {"usedTrafficBytes": 5000}
				},
				{
					"username": "u2",
					"vlessUuid": "uuid-2",
					"shortUuid": "short-2",
					"status": "ACTIVE",
					"telegramId": 999999,
					"activeInternalSquads": [{"name": "Squad-A"}],
					"usedTrafficBytes": 7000
				}
			],
			"total": 2
		}
	}`)
	f, err := Unmarshal(raw)
	if err != nil {
		t.Fatalf("unmarshal raw Remnawave JSON: %v", err)
	}
	if f.Meta.Source != SourceRemnawave || len(f.Users) != 2 {
		t.Fatalf("meta/users: %+v %d", f.Meta, len(f.Users))
	}
	st := testStore(t)
	im := NewImporter(st, ImportOptions{WithTraffic: true, CreateGroups: true})
	im.NewTokenFn = func() (string, error) { return "fallback-tok", nil }
	rep, err := im.Import(context.Background(), f)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if rep.Created != 2 || rep.Failed != 0 {
		t.Fatalf("report: %+v", rep)
	}
	if cg := rep.CreatedGroups(); len(cg) != 1 || cg[0] != "Squad-A" {
		t.Errorf("created groups: %v", cg)
	}
	u1 := mustUser(t, st, "u1")
	if u1.SubToken != "short-1" || u1.TrafficUsedBytes != 5000 || u1.LifetimeUsedBytes != 5000 {
		t.Errorf("u1: %+v", u1)
	}
	u2 := mustUser(t, st, "u2")
	if u2.SubToken != "short-2" || u2.TrafficUsedBytes != 7000 || u2.TelegramID != nil || u2.Note != "tg:999999" {
		t.Errorf("u2 duplicate tg collision not handled: %+v", u2)
	}
}
