package webapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An ordinary API request over maxBody is refused before the handler sees it.
func TestBodyLimit(t *testing.T) {
	c, _, pw := newServer(t)
	if w := c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true); w.Code != 200 {
		t.Fatalf("login: %d", w.Code)
	}
	huge := `{"username":"` + strings.Repeat("a", maxBody+1) + `"}`
	if w := c.do("POST", "/api/users", huge, true); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: %d %s", w.Code, w.Body.String())
	}
}

// An import may be larger than an ordinary request but still has its own ceiling.
func TestImportBodyLimit(t *testing.T) {
	c, _, pw := newServer(t)
	if w := c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true); w.Code != 200 {
		t.Fatalf("login: %d", w.Code)
	}
	tooBig, _ := json.Marshal(map[string]any{"file": strings.Repeat("a", maxImportBody+1)})
	if w := c.do("POST", "/api/users/import", string(tooBig), true); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized import: %d %s", w.Code, w.Body.String())
	}
	// A body between the two limits reaches the handler: the import fails on its contents,
	// not on its size.
	between, _ := json.Marshal(map[string]any{"file": strings.Repeat("a", maxBody+1)})
	if w := c.do("POST", "/api/users/import", string(between), true); w.Code == http.StatusRequestEntityTooLarge {
		t.Fatalf("an import under maxImportBody was refused as too large: %s", w.Body.String())
	}
}

// decodeLimit reports a body over its limit as 413 even when called without a ResponseWriter.
func TestDecodeLimit(t *testing.T) {
	body := strings.NewReader(`{"a":"` + strings.Repeat("x", 64) + `"}`)
	r := httptest.NewRequest("POST", "/", body)
	var v map[string]any
	err := decodeLimit(r, &v, 8)
	he, ok := err.(*httpError)
	if !ok || he.code != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %T %v, want 413", err, err)
	}
}
