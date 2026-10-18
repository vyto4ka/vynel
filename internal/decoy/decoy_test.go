package decoy

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSites(t *testing.T) {
	names := Names()
	if len(names) != 5 {
		t.Fatalf("five sites expected: %v", names)
	}
	for _, n := range names {
		for _, p := range []string{"/", "/favicon.svg", "/assets/site.css", "/robots.txt"} {
			w := httptest.NewRecorder()
			Serve(w, httptest.NewRequest("GET", p, nil), n)
			if w.Code != 200 {
				t.Fatalf("%s%s: %d", n, p, w.Code)
			}
		}
		// Every link on the home page opens (clean URLs, no .html).
		w := httptest.NewRecorder()
		Serve(w, httptest.NewRequest("GET", "/", nil), n)
		home := w.Body.String()
		if strings.Contains(home, ".html\"") {
			t.Fatalf("%s: a link ends in .html", n)
		}
		pages := 0
		for _, part := range strings.Split(home, `href="/`)[1:] {
			link := "/" + part[:strings.IndexByte(part, '"')]
			w := httptest.NewRecorder()
			Serve(w, httptest.NewRequest("GET", link, nil), n)
			if w.Code != 200 {
				t.Fatalf("%s: link %s gives %d", n, link, w.Code)
			}
			if !strings.Contains(link, ".") {
				pages++
			}
		}
		if pages < 4 {
			t.Fatalf("%s: only %d pages linked", n, pages)
		}
		w = httptest.NewRecorder()
		Serve(w, httptest.NewRequest("GET", "/wp-admin", nil), n)
		if w.Code != 404 || !strings.Contains(w.Body.String(), "404") {
			t.Fatalf("%s: 404 page: %d", n, w.Code)
		}
	}
	if !Valid(Random()) {
		t.Fatal("random site is not valid")
	}
}
