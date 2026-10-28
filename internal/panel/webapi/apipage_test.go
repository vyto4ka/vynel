package webapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIPageTokensAndWebhooks(t *testing.T) {
	c, _, pw := newServer(t)
	if w := c.do("GET", "/api/integrations", "", false); w.Code != 401 {
		t.Fatalf("the page needs a session: %d", w.Code)
	}
	if w := c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true); w.Code != 200 {
		t.Fatalf("login: %d", w.Code)
	}
	w := c.do("POST", "/api/integrations/tokens", `{"name":"billing","scopes":["users:read","users:write"],"cidrs":["203.0.113.5"]}`, true)
	if w.Code != 200 {
		t.Fatalf("create token: %d %s", w.Code, w.Body.String())
	}
	var made struct {
		Token  tokenDTO `json:"token"`
		Secret string   `json:"secret"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &made)
	if !strings.HasPrefix(made.Secret, "vyn_") || made.Token.Prefix != made.Secret[:8] || made.Token.State != "active" {
		t.Fatalf("token: %+v", made)
	}
	if w := c.do("POST", "/api/integrations/tokens", `{"name":"x","scopes":[]}`, true); w.Code != 400 {
		t.Fatalf("no scopes: %d %s", w.Code, w.Body.String())
	}

	// The secret works against the public API from its network, and from nowhere else.
	if w := apiCall(t, c, made.Secret, "GET", "/users", ""); w.Code != 200 {
		t.Fatalf("token from its network: %d %s", w.Code, w.Body.String())
	}
	r := httptest.NewRequest("GET", c.base+"/api/v1/users", nil)
	r.Header.Set("Authorization", "Bearer "+made.Secret)
	r.RemoteAddr = "198.51.100.1:1"
	rec := httptest.NewRecorder()
	c.srv.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden || errCode(t, rec) != "ip_not_allowed" {
		t.Fatalf("token from another network: %d %s", rec.Code, rec.Body.String())
	}

	w = c.do("GET", "/api/integrations", "", false)
	if strings.Contains(w.Body.String(), made.Secret) || strings.Contains(w.Body.String(), `"hash"`) {
		t.Fatalf("the list must not hold the secret: %s", w.Body.String())
	}
	id := itoa(made.Token.ID)
	if w := c.do("POST", "/api/integrations/tokens/"+id+"/revoke", "", true); w.Code != 200 {
		t.Fatalf("revoke: %d", w.Code)
	}
	if w := apiCall(t, c, made.Secret, "GET", "/users", ""); w.Code != 401 || errCode(t, w) != "token_revoked" {
		t.Fatalf("revoked token: %d %s", w.Code, w.Body.String())
	}

	w = c.do("POST", "/api/integrations/webhooks", `{"name":"bot","url":"https://example.com/hook","events":["user.*"],"enabled":true}`, true)
	var hook webhookDTO
	_ = json.Unmarshal(w.Body.Bytes(), &hook)
	if w.Code != 200 || !strings.HasPrefix(hook.Secret, "whsec_") {
		t.Fatalf("create webhook: %d %s", w.Code, w.Body.String())
	}
	if w := c.do("GET", "/api/integrations", "", false); strings.Contains(w.Body.String(), hook.Secret) || !strings.Contains(w.Body.String(), hook.SecretHint) {
		t.Fatalf("a webhook secret is shown once: %s", w.Body.String())
	}
	if w := c.do("POST", "/api/integrations/webhooks/"+itoa(hook.ID)+"/rotate", "", true); w.Code != 200 || strings.Contains(w.Body.String(), hook.Secret) {
		t.Fatalf("rotate: %d %s", w.Code, w.Body.String())
	}
	if w := c.do("POST", "/api/integrations/webhooks", `{"name":"bad","url":"http://localhost:1/x"}`, true); w.Code != 400 {
		t.Fatalf("a local address needs allowing: %d %s", w.Code, w.Body.String())
	}
	if w := c.do("DELETE", "/api/integrations/webhooks/"+itoa(hook.ID), "", true); w.Code != 200 {
		t.Fatalf("delete: %d", w.Code)
	}
}
