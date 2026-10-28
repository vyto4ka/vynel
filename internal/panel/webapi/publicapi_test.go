package webapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/vyto4ka/vynel/internal/panel/service"
)

// apiCall sends a request to the public API with a token.
func apiCall(t *testing.T, c *client, token, method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, c.base+"/api/v1"+path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	r.RemoteAddr = "203.0.113.5:4000"
	w := httptest.NewRecorder()
	c.srv.ServeHTTP(w, r)
	return w
}

func apiToken(t *testing.T, svc *service.Service, scopes ...string) string {
	t.Helper()
	_, secret, err := svc.CreateAPIToken(t.Context(), service.ActorCLI, service.APITokenInput{Name: "test", Scopes: scopes})
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

func errCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct {
		Error struct {
			Code      string `json:"code"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatalf("not an error body: %s", w.Body.String())
	}
	if e.Error.RequestID == "" {
		t.Errorf("no request id: %s", w.Body.String())
	}
	return e.Error.Code
}

func TestAPIAuthAndScopes(t *testing.T) {
	c, svc, _ := newServer(t)
	read := apiToken(t, svc, service.ScopeUsersRead)

	if w := apiCall(t, c, "", "GET", "/users", ""); w.Code != 401 || errCode(t, w) != "unauthenticated" {
		t.Fatalf("no token: %d %s", w.Code, w.Body.String())
	}
	if w := apiCall(t, c, "vyn_wrong", "GET", "/users", ""); w.Code != 401 || errCode(t, w) != "invalid_token" {
		t.Fatalf("wrong token: %d %s", w.Code, w.Body.String())
	}
	if w := apiCall(t, c, read, "GET", "/users", ""); w.Code != 200 {
		t.Fatalf("read: %d %s", w.Code, w.Body.String())
	}
	if w := apiCall(t, c, read, "POST", "/users", `{"username":"x"}`); w.Code != 403 || errCode(t, w) != "insufficient_scope" {
		t.Fatalf("write with a read token: %d %s", w.Code, w.Body.String())
	}
	if w := apiCall(t, c, read, "GET", "/nodes", ""); w.Code != 403 {
		t.Fatalf("other scope: %d", w.Code)
	}
	if w := apiCall(t, c, read, "GET", "/me", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"users:read"`) {
		t.Fatalf("me: %d %s", w.Code, w.Body.String())
	}
	if w := apiCall(t, c, read, "GET", "/nowhere", ""); w.Code != 404 || errCode(t, w) != "no_such_endpoint" {
		t.Fatalf("unknown path: %d %s", w.Code, w.Body.String())
	}
	if w := apiCall(t, c, read, "PUT", "/users", ""); w.Code != 405 || errCode(t, w) != "method_not_allowed" || w.Header().Get("Allow") == "" {
		t.Fatalf("wrong method: %d %s", w.Code, w.Body.String())
	}
	// The panel's cookie does not open the public API, and the token does not open the panel's.
	if w := c.do("GET", "/api/users", "", false); w.Code != 401 {
		t.Fatalf("panel api without cookie: %d", w.Code)
	}
	r := httptest.NewRequest("GET", c.base+"/api/users", nil)
	r.Header.Set("Authorization", "Bearer "+read)
	if w := httptest.NewRecorder(); func() int { c.srv.ServeHTTP(w, r); return w.Code }() != 401 {
		t.Fatal("a token must not open the panel's own API")
	}
}

func TestAPIFailedTokensBlockAddress(t *testing.T) {
	c, _, _ := newServer(t)
	for i := 0; i < authFailLimit; i++ {
		apiCall(t, c, "vyn_guess"+strings.Repeat("x", i), "GET", "/users", "")
	}
	w := apiCall(t, c, "vyn_anything", "GET", "/users", "")
	if w.Code != 429 || errCode(t, w) != "too_many_failures" || w.Header().Get("Retry-After") == "" {
		t.Fatalf("blocked: %d %s", w.Code, w.Body.String())
	}
}

func TestAPIRateLimit(t *testing.T) {
	c, svc, _ := newServer(t)
	_, secret, err := svc.CreateAPIToken(t.Context(), service.ActorCLI, service.APITokenInput{Name: "slow", Scopes: []string{service.ScopeUsersRead}, RateLimit: 3})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if w := apiCall(t, c, secret, "GET", "/users", ""); w.Code != 200 || w.Header().Get("X-RateLimit-Limit") != "3" {
			t.Fatalf("request %d: %d", i, w.Code)
		}
	}
	if w := apiCall(t, c, secret, "GET", "/users", ""); w.Code != 429 || errCode(t, w) != "rate_limited" || w.Header().Get("Retry-After") == "" {
		t.Fatalf("over the limit: %d %s", w.Code, w.Body.String())
	}
}

func TestAPIUsersLifecycle(t *testing.T) {
	c, svc, _ := newServer(t)
	tok := apiToken(t, svc, service.ScopeUsersRead, service.ScopeUsersWrite, service.ScopeUsersDelete, service.ScopeEventsRead)

	w := apiCall(t, c, tok, "GET", "/events?after=now", "")
	var feed struct {
		NextCursor string `json:"next_cursor"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &feed)

	w = apiCall(t, c, tok, "POST", "/users", `{"username":"alice","external_id":"order-1","telegram_id":777,"note":"hi"}`)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var u struct {
		ID         int64   `json:"id"`
		Username   string  `json:"username"`
		ExternalID *string `json:"external_id"`
		ExpireAt   *string `json:"expire_at"`
		Status     string  `json:"status"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &u)
	if u.Username != "alice" || u.ExternalID == nil || *u.ExternalID != "order-1" || u.Status != "active" {
		t.Fatalf("created: %s", w.Body.String())
	}
	if w := apiCall(t, c, tok, "POST", "/users", `{"username":"alice"}`); w.Code != 409 || errCode(t, w) != "username_taken" {
		t.Fatalf("same name: %d %s", w.Code, w.Body.String())
	}
	if w := apiCall(t, c, tok, "POST", "/users", `{"username":"bob","external_id":"order-1"}`); w.Code != 409 || errCode(t, w) != "external_id_taken" {
		t.Fatalf("same external id: %d %s", w.Code, w.Body.String())
	}
	if w := apiCall(t, c, tok, "POST", "/users", `{"username":"bob","extarnal_id":"x"}`); w.Code != 400 || errCode(t, w) != "bad_json" {
		t.Fatalf("typo in a field: %d %s", w.Code, w.Body.String())
	}
	if w := apiCall(t, c, tok, "POST", "/users", `{"username":"b b"}`); w.Code != 422 || errCode(t, w) != "validation_failed" {
		t.Fatalf("bad username: %d %s", w.Code, w.Body.String())
	}

	// Lookups by what the other system knows.
	for _, q := range []string{"external_id=order-1", "telegram_id=777", "username=alice", "search=ali"} {
		w := apiCall(t, c, tok, "GET", "/users?"+q, "")
		if !strings.Contains(w.Body.String(), `"total":1`) || !strings.Contains(w.Body.String(), `"alice"`) {
			t.Errorf("%s: %s", q, w.Body.String())
		}
	}
	if w := apiCall(t, c, tok, "GET", "/users?external_id=nobody", ""); !strings.Contains(w.Body.String(), `"total":0`) || !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Errorf("no match: %s", w.Body.String())
	}

	id := "/users/" + itoa(u.ID)
	if w := apiCall(t, c, tok, "POST", id+"/extend", `{"months":1}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"expire_at":"`) {
		t.Fatalf("extend: %d %s", w.Code, w.Body.String())
	}
	if w := apiCall(t, c, tok, "POST", id+"/extend", `{}`); w.Code != 422 {
		t.Fatalf("extend nothing: %d %s", w.Code, w.Body.String())
	}
	if w := apiCall(t, c, tok, "PATCH", id, `{"expire_at":null,"traffic_limit_bytes":1000,"note":"paid","external_id":null}`); w.Code != 200 ||
		!strings.Contains(w.Body.String(), `"expire_at":null`) || !strings.Contains(w.Body.String(), `"traffic_limit_bytes":1000`) || !strings.Contains(w.Body.String(), `"external_id":null`) {
		t.Fatalf("patch: %d %s", w.Code, w.Body.String())
	}
	if w := apiCall(t, c, tok, "PATCH", id, `{"enabled":false,"bogus":1}`); w.Code != 400 || errCode(t, w) != "unknown_field" {
		t.Fatalf("patch unknown field: %d %s", w.Code, w.Body.String())
	}
	if w := apiCall(t, c, tok, "POST", id+"/disable", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"disabled"`) {
		t.Fatalf("disable: %d %s", w.Code, w.Body.String())
	}
	if w := apiCall(t, c, tok, "GET", id+"/subscription", ""); w.Code != 200 {
		t.Fatalf("subscription: %d %s", w.Code, w.Body.String())
	}
	if w := apiCall(t, c, tok, "GET", "/users/999999", ""); w.Code != 404 || errCode(t, w) != "user_not_found" {
		t.Fatalf("missing user: %d %s", w.Code, w.Body.String())
	}
	if w := apiCall(t, c, tok, "GET", "/users/abc", ""); w.Code != 400 || errCode(t, w) != "bad_id" {
		t.Fatalf("bad id: %d %s", w.Code, w.Body.String())
	}

	// The feed says what the token itself did, and by whom.
	w = apiCall(t, c, tok, "GET", "/events?after="+feed.NextCursor, "")
	for _, want := range []string{`"user.created"`, `"user.extended"`, `"user.disabled"`, `"kind":"api"`, `"username":"alice"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("event feed lacks %s: %s", want, w.Body.String())
		}
	}
	if w := apiCall(t, c, tok, "GET", "/events?types=user.created&after="+feed.NextCursor, ""); strings.Contains(w.Body.String(), "user.extended") {
		t.Errorf("type filter: %s", w.Body.String())
	}

	if w := apiCall(t, c, tok, "DELETE", id, ""); w.Code != 204 || w.Body.Len() != 0 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if w := apiCall(t, c, tok, "GET", id, ""); w.Code != 404 {
		t.Fatalf("deleted: %d", w.Code)
	}
	noDelete := apiToken(t, svc, service.ScopeUsersWrite)
	if w := apiCall(t, c, noDelete, "DELETE", id, ""); w.Code != 403 {
		t.Fatalf("delete needs its own scope: %d", w.Code)
	}
}

func TestAPIIdempotency(t *testing.T) {
	c, svc, _ := newServer(t)
	tok := apiToken(t, svc, service.ScopeUsersRead, service.ScopeUsersWrite)
	body := `{"username":"carol"}`
	first := apiCall(t, c, tok, "POST", "/users", body, "Idempotency-Key", "k-1")
	if first.Code != 201 {
		t.Fatalf("first: %d %s", first.Code, first.Body.String())
	}
	again := apiCall(t, c, tok, "POST", "/users", body, "Idempotency-Key", "k-1")
	if again.Code != 201 || again.Body.String() != first.Body.String() || again.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("repeat must be the first answer: %d %s", again.Code, again.Body.String())
	}
	// Without the key the same request is a conflict, which shows the repeat did not run twice.
	if w := apiCall(t, c, tok, "POST", "/users", body); w.Code != 409 {
		t.Fatalf("no key: %d", w.Code)
	}
	if w := apiCall(t, c, tok, "POST", "/users", `{"username":"dave"}`, "Idempotency-Key", "k-1"); w.Code != 422 || errCode(t, w) != "idempotency_key_reused" {
		t.Fatalf("the same key for another request: %d %s", w.Code, w.Body.String())
	}
	// Another token has its own keys.
	other := apiToken(t, svc, service.ScopeUsersWrite)
	if w := apiCall(t, c, other, "POST", "/users", `{"username":"erin"}`, "Idempotency-Key", "k-1"); w.Code != 201 {
		t.Fatalf("keys are per token: %d %s", w.Code, w.Body.String())
	}
	var n struct{ Total int }
	_ = json.Unmarshal(apiCall(t, c, tok, "GET", "/users", "").Body.Bytes(), &n)
	if n.Total != 2 {
		t.Fatalf("carol once and erin: %d users", n.Total)
	}
}

func TestAPIReadsNodesGroupsStats(t *testing.T) {
	c, svc, _ := newServer(t)
	tok := apiToken(t, svc, service.ScopeNodesRead, service.ScopeGroupsRead, service.ScopeTemplatesRead, service.ScopeStatsRead)
	for _, p := range []string{"/nodes", "/groups", "/user-templates", "/stats/overview"} {
		if w := apiCall(t, c, tok, "GET", p, ""); w.Code != 200 {
			t.Errorf("%s: %d %s", p, w.Code, w.Body.String())
		}
	}
	if w := apiCall(t, c, tok, "GET", "/groups", ""); !strings.Contains(w.Body.String(), `"member_count"`) {
		t.Errorf("groups: %s", w.Body.String())
	}
}

// The spec lists exactly the routes the server has, with the same permissions.
func TestOpenAPIMatchesRoutes(t *testing.T) {
	c, _, _ := newServer(t)
	var spec struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal([]byte(openAPISpec), &spec); err != nil {
		t.Fatal(err)
	}
	have := map[string]string{}
	for _, r := range c.srv.v1State.routes {
		have[r.Method+" "+r.Path] = r.Scope
	}
	for path, ops := range spec.Paths {
		for method, op := range ops {
			m := strings.ToUpper(method)
			opm, isOp := op.(map[string]any)
			if !isOp { // "parameters" shared by the operations of a path
				continue
			}
			scope, ok := have[m+" "+path]
			if !ok {
				t.Errorf("the spec has %s %s, the server does not", m, path)
				continue
			}
			if got, ok := opm["x-required-scope"].(string); !ok || got != scope {
				t.Errorf("%s %s: the spec says scope %v, the server %q", m, path, opm["x-required-scope"], scope)
			}
			delete(have, m+" "+path)
		}
	}
	for k := range have {
		t.Errorf("the server has %s, the spec does not", k)
	}
}

func TestOpenAPIServed(t *testing.T) {
	c, _, _ := newServer(t)
	w := apiCall(t, c, "", "GET", "/openapi.json", "")
	if w.Code != http.StatusOK {
		t.Fatalf("%d", w.Code)
	}
	var doc struct {
		OpenAPI string `json:"openapi"`
		Servers []struct {
			URL string `json:"url"`
		} `json:"servers"`
		Paths map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil || doc.OpenAPI == "" || len(doc.Paths) < 10 {
		t.Fatalf("spec json: %v %s", err, w.Body.String()[:200])
	}
	if len(doc.Servers) != 1 || !strings.HasSuffix(doc.Servers[0].URL, "/api/v1") || strings.Contains(doc.Servers[0].URL, "{{") {
		t.Fatalf("server url: %+v", doc.Servers)
	}
	if w := apiCall(t, c, "", "GET", "/openapi.yaml", ""); w.Code != 200 || !strings.HasPrefix(w.Body.String(), "openapi:") {
		t.Fatalf("yaml: %d", w.Code)
	}
}
