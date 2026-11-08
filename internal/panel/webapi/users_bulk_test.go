package webapi

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
)

func TestBulkUsersActionsAndPartialFailures(t *testing.T) {
	c, svc, pw := newServer(t)
	if w := c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	newUser := func(name string) *store.User {
		t.Helper()
		u, err := svc.CreateUser(t.Context(), service.ActorCLI, service.CreateUserInput{Username: name})
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	a, b, other := newUser("bulk-a"), newUser("bulk-b"), newUser("unselected")
	run := func(action string, ids ...int64) ([]int64, []bulkUserFailure) {
		t.Helper()
		body, err := json.Marshal(map[string]any{"action": action, "ids": ids})
		if err != nil {
			t.Fatal(err)
		}
		w := c.do("POST", "/api/users/bulk", string(body), true)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", action, w.Code, w.Body.String())
		}
		var result struct {
			Succeeded []int64           `json:"succeeded"`
			Failed    []bulkUserFailure `json:"failed"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.Succeeded, result.Failed
	}
	load := func(id int64) *store.User {
		t.Helper()
		u, err := svc.User(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	done, failed := run("disable", a.ID, 999999, b.ID, a.ID)
	if !slices.Equal(done, []int64{a.ID, b.ID}) || len(failed) != 1 || failed[0].ID != 999999 || failed[0].Error == "" {
		t.Fatalf("outcomes: %v %+v", done, failed)
	}
	if !load(a.ID).Disabled || !load(b.ID).Disabled || load(other.ID).Disabled {
		t.Fatal("changed the wrong users")
	}
	audits, err := store.ListAudit(t.Context(), svc.Store().DB, 100)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range audits {
		if entry.Action == "user.set_enabled" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("duplicate ID applied more than once: %d entries", count)
	}
	// Enabling does not extend an expired subscription or lift its limits.
	b = load(b.ID)
	past, limit := time.Now().Add(-time.Hour).Unix(), int64(10000)
	b.ExpireAt, b.TrafficLimitBytes = &past, &limit
	if err := store.UpdateUser(t.Context(), svc.Store().DB, b); err != nil {
		t.Fatal(err)
	}
	if _, failed := run("enable", a.ID, b.ID); len(failed) != 0 {
		t.Fatal(failed)
	}
	b = load(b.ID)
	if b.Disabled || b.Status != store.StatusExpired || *b.ExpireAt != past || *b.TrafficLimitBytes != limit {
		t.Fatalf("enable changed subscription terms: %+v", b)
	}
	a = load(a.ID)
	a.TrafficUsedBytes = 1234
	if err := store.UpdateUser(t.Context(), svc.Store().DB, a); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Store().DB.ExecContext(t.Context(), `UPDATE users SET lifetime_used_bytes=? WHERE id=?`, 99000, a.ID); err != nil {
		t.Fatal(err)
	}
	other = load(other.ID)
	other.TrafficUsedBytes = 5678
	if err := store.UpdateUser(t.Context(), svc.Store().DB, other); err != nil {
		t.Fatal(err)
	}
	if _, failed := run("reset", a.ID, b.ID); len(failed) != 0 {
		t.Fatal(failed)
	}
	if got := load(a.ID); got.TrafficUsedBytes != 0 || got.LastResetAt == nil || got.LifetimeUsedBytes != 99000 {
		t.Fatalf("traffic not reset: %+v", got)
	}
	if load(other.ID).TrafficUsedBytes != 5678 {
		t.Fatal("reset unselected user")
	}
	if _, failed := run("delete", a.ID, b.ID); len(failed) != 0 {
		t.Fatal(failed)
	}
	for _, id := range []int64{a.ID, b.ID} {
		if _, err := svc.User(t.Context(), id); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("user %d not deleted: %v", id, err)
		}
	}
	if load(other.ID).Username != "unselected" {
		t.Fatal("deleted unselected user")
	}
}

func TestBulkUsersValidatesBeforeChangingAnyone(t *testing.T) {
	c, svc, pw := newServer(t)
	u, err := svc.CreateUser(t.Context(), service.ActorCLI, service.CreateUserInput{Username: "safe"})
	if err != nil {
		t.Fatal(err)
	}
	valid := `{"action":"disable","ids":[` + itoa(u.ID) + `]}`
	if w := c.do("POST", "/api/users/bulk", valid, true); w.Code != 401 {
		t.Fatalf("auth: %d", w.Code)
	}
	if w := c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := c.do("POST", "/api/users/bulk", valid, false); w.Code != 403 {
		t.Fatalf("csrf: %d", w.Code)
	}
	for _, body := range []string{
		`{"action":"disable","ids":[]}`,
		`{"action":"unknown","ids":[` + itoa(u.ID) + `]}`,
		`{"action":"disable","ids":[` + itoa(u.ID) + `,-1]}`,
		`{"action":"disable","ids":[0]}`,
		`{"action":"disable","ids":["not-an-id"]}`,
	} {
		if w := c.do("POST", "/api/users/bulk", body, true); w.Code != 400 {
			t.Fatalf("accepted %s: %d %s", body, w.Code, w.Body.String())
		}
	}
	ids := make([]int64, maxBulkUsers+1)
	for i := range ids {
		ids[i] = u.ID
	}
	body, err := json.Marshal(map[string]any{"action": "disable", "ids": ids})
	if err != nil {
		t.Fatal(err)
	}
	if w := c.do("POST", "/api/users/bulk", string(body), true); w.Code != 400 {
		t.Fatalf("oversize batch: %d", w.Code)
	}
	u, err = svc.User(t.Context(), u.ID)
	if err != nil || u.Disabled {
		t.Fatalf("invalid batch changed a user: %+v %v", u, err)
	}
}
