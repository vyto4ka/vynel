package webapi

import (
	"strings"
	"testing"
)

// With X-Lang: en the answers that carry the panel's own words come out in English; without it,
// in Russian.
func TestAnswersFollowTheLanguage(t *testing.T) {
	c, _, pw := newServer(t)
	c.lang = "en"
	if w := c.do("POST", "/api/login", `{"login":"boss","password":"nope"}`, true); w.Code != 401 || !strings.Contains(w.Body.String(), "wrong login or password") {
		t.Fatalf("login error in English: %d %s", w.Code, w.Body.String())
	}
	c.lang = ""
	if w := c.do("POST", "/api/login", `{"login":"boss","password":"nope"}`, true); !strings.Contains(w.Body.String(), "неверный логин или пароль") {
		t.Fatalf("login error in Russian: %s", w.Body.String())
	}
	if w := c.do("POST", "/api/login", `{"login":"boss","password":"`+pw+`"}`, true); w.Code != 200 {
		t.Fatal("login")
	}

	// Validation errors keep their parts: a field name and a line are translated inside the message.
	c.lang = "en"
	w := c.do("PUT", "/api/xray/base", `{"json":"{\"inbounds\": []}"}`, true)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "must not be here") {
		t.Fatalf("base config error in English: %d %s", w.Code, w.Body.String())
	}
	w = c.do("PUT", "/api/subscription/routing", `{"dnsDirect":["localhost"],"dnsProxy":["localhost"]}`, true)
	if strings.ContainsAny(w.Body.String(), "абвгдеёжзийклмнопрстуфхцчшщыэюя") && w.Code == 400 {
		t.Fatalf("a routing error is half Russian: %s", w.Body.String())
	}

	// Settings: titles, hints and help.
	w = c.do("GET", "/api/settings", "", false)
	if !strings.Contains(w.Body.String(), `"title":"Secret panel path"`) || !strings.Contains(w.Body.String(), `"section":"Panel access"`) {
		t.Fatalf("settings in English: %s", w.Body.String()[:300])
	}
	c.lang = ""
	if w := c.do("GET", "/api/settings", "", false); !strings.Contains(w.Body.String(), `"title":"Секретный путь панели"`) {
		t.Fatalf("settings in Russian: %s", w.Body.String()[:300])
	}

	// Built-in template hints.
	c.lang = "en"
	if w := c.do("GET", "/api/profile-templates", "", false); strings.Contains(w.Body.String(), `"description":"Приватный ключ Reality`) || !strings.Contains(w.Body.String(), "The Reality private key, its own on each node.") {
		t.Fatalf("template hints in English: %s", w.Body.String()[:400])
	}
}
