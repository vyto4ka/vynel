package webapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

// The panel's own users/export and users/import: the file of one panel lands in another.
func TestUsersExportImportRoundTrip(t *testing.T) {
	src, svc, pw := newServer(t)
	if w := src.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true); w.Code != 200 {
		t.Fatal("login")
	}
	for _, name := range []string{"alice", "bob"} {
		if _, err := svc.CreateUser(t.Context(), service.ActorCLI, service.CreateUserInput{Username: name}); err != nil {
			t.Fatal(err)
		}
	}

	w := src.do("GET", "/api/users/export", "", false)
	if w.Code != 200 {
		t.Fatalf("export: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.Bytes()

	dst, _, pw2 := newServer(t)
	if w := dst.do("POST", "/api/login", `{"login":"boss","password":"`+pw2+`"}`, true); w.Code != 200 {
		t.Fatal("login dst")
	}

	// A third panel, not logged in, gets nothing.
	anon, _, _ := newServer(t)
	if w := anon.do("GET", "/api/users/export", "", false); w.Code != 401 {
		t.Fatalf("no session: %d", w.Code)
	}

	payload, _ := json.Marshal(map[string]any{"file": json.RawMessage(body), "mode": "skip", "reissueTokens": false})
	w = dst.do("POST", "/api/users/import", string(payload), true)
	if w.Code != 200 {
		t.Fatalf("import: %d %s", w.Code, w.Body.String())
	}
	var rep struct {
		Total, Created int
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil || rep.Created != 2 || rep.Total != 2 {
		t.Fatalf("report: %v (%s)", err, w.Body.String())
	}

	// Re-import skips (mode: skip); a junk file is a 400.
	w = dst.do("POST", "/api/users/import", string(payload), true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"skipped":2`) {
		t.Fatalf("re-import: %d %s", w.Code, w.Body.String())
	}
	w = dst.do("POST", "/api/users/import", `{"file": "not json"}`, true)
	if w.Code != 400 {
		t.Fatalf("junk file: %d %s", w.Code, w.Body.String())
	}

	// Bulk actions on imported users.
	w = dst.do("GET", "/api/users", "", false)
	var list []User
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 2 {
		t.Fatalf("expected 2 users on dst, got %d", len(list))
	}
	bulkPayload, _ := json.Marshal(map[string]any{
		"ids":    []int64{list[0].ID, list[1].ID},
		"action": "disable",
	})
	w = dst.do("POST", "/api/users/bulk", string(bulkPayload), true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"done":2`) {
		t.Fatalf("bulk disable: %d %s", w.Code, w.Body.String())
	}
}
