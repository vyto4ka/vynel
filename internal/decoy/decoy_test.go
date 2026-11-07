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

// The headers of a real static server: Last-Modified and Etag on files, 304 to a conditional
// request, an empty body with the same headers on HEAD.
func TestServeHeaders(t *testing.T) {
	for _, n := range Names() {
		w := httptest.NewRecorder()
		Serve(w, httptest.NewRequest("GET", "/", nil), n)
		if w.Code != 200 || w.Header().Get("Last-Modified") == "" || w.Header().Get("Etag") == "" {
			t.Fatalf("%s: headers %v", n, w.Header())
		}

		// HEAD: no body, same headers, no Content-Length mismatch.
		h := httptest.NewRecorder()
		Serve(h, httptest.NewRequest("HEAD", "/", nil), n)
		if h.Code != 200 || h.Body.Len() != 0 {
			t.Fatalf("%s: HEAD %d, body %d bytes", n, h.Code, h.Body.Len())
		}
		if h.Header().Get("Etag") != w.Header().Get("Etag") {
			t.Fatalf("%s: HEAD etag %q != GET %q", n, h.Header().Get("Etag"), w.Header().Get("Etag"))
		}

		// If-Modified-Since with the file's own date: 304, no body.
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("If-Modified-Since", w.Header().Get("Last-Modified"))
		c := httptest.NewRecorder()
		Serve(c, req, n)
		if c.Code != 304 || c.Body.Len() != 0 {
			t.Fatalf("%s: conditional: %d, body %d bytes", n, c.Code, c.Body.Len())
		}

		// If-None-Match with the file's own etag: 304.
		req = httptest.NewRequest("GET", "/", nil)
		req.Header.Set("If-None-Match", w.Header().Get("Etag"))
		c = httptest.NewRecorder()
		Serve(c, req, n)
		if c.Code != 304 {
			t.Fatalf("%s: If-None-Match: %d", n, c.Code)
		}

		// Range: a real static server answers 206 with a slice.
		req = httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Range", "bytes=0-99")
		c = httptest.NewRecorder()
		Serve(c, req, n)
		if c.Code != 206 || c.Body.Len() != 100 {
			t.Fatalf("%s: range: %d, %d bytes", n, c.Code, c.Body.Len())
		}

		// The etag matches nginx's format: "<mtime-hex>-<size-hex>".
		if e := w.Header().Get("Etag"); !strings.HasPrefix(e, `"`) || !strings.Contains(e, "-") {
			t.Fatalf("%s: etag %q", n, e)
		}

		// The 404 page carries no caching headers: a real site's error page does not either.
		w = httptest.NewRecorder()
		Serve(w, httptest.NewRequest("GET", "/nope", nil), n)
		if w.Header().Get("Etag") != "" || w.Header().Get("Last-Modified") != "" {
			t.Fatalf("%s: 404 headers %v", n, w.Header())
		}
	}
}
