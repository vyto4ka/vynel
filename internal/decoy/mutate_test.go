package decoy

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMutationUniquenessAndDeterminism(t *testing.T) {
	s1 := NewSeed()
	s2 := NewSeed()
	if len(s1) != 32 || s1 == s2 {
		t.Fatalf("expected distinct 32-char hex seeds, got %q and %q", s1, s2)
	}

	for _, name := range Names() {
		w1 := httptest.NewRecorder()
		ServeWithSeed(w1, httptest.NewRequest("GET", "/", nil), name, s1)
		w2 := httptest.NewRecorder()
		ServeWithSeed(w2, httptest.NewRequest("GET", "/", nil), name, s2)
		w1b := httptest.NewRecorder()
		ServeWithSeed(w1b, httptest.NewRequest("GET", "/", nil), name, s1)

		if w1.Code != 200 || w2.Code != 200 {
			t.Fatalf("%s: codes %d / %d", name, w1.Code, w2.Code)
		}
		if w1.Body.String() != w1b.Body.String() {
			t.Fatalf("%s: same seed must be deterministic", name)
		}
		if w1.Body.String() == w2.Body.String() {
			t.Fatalf("%s: different seeds produced identical index.html", name)
		}
		if w1.Header().Get("Etag") == w2.Header().Get("Etag") {
			t.Fatalf("%s: different seeds produced identical Etag %q", name, w1.Header().Get("Etag"))
		}
		if !strings.Contains(w1.Body.String(), "hue-rotate(") {
			t.Fatalf("%s: missing hue-rotate style injection", name)
		}
		if !strings.Contains(w1.Body.String(), `/assets/site.css?v=`) {
			t.Fatalf("%s: missing cache-buster query on site.css", name)
		}

		// Favicon must be unique per seed too (different mmh3 hash).
		f1 := httptest.NewRecorder()
		ServeWithSeed(f1, httptest.NewRequest("GET", "/favicon.svg", nil), name, s1)
		f2 := httptest.NewRecorder()
		ServeWithSeed(f2, httptest.NewRequest("GET", "/favicon.svg", nil), name, s2)
		if f1.Body.String() == f2.Body.String() {
			t.Fatalf("%s: different seeds produced identical favicon.svg", name)
		}

		// Conditional request with mutated Last-Modified returns 304.
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("If-Modified-Since", w1.Header().Get("Last-Modified"))
		c := httptest.NewRecorder()
		ServeWithSeed(c, req, name, s1)
		if c.Code != 304 {
			t.Fatalf("%s: conditional request returned %d", name, c.Code)
		}
	}

	// InstallWithSeed writes mutated files matching ServeWithSeed.
	dir := t.TempDir()
	dst, err := InstallWithSeed(dir, "cloud", s1)
	if err != nil {
		t.Fatal(err)
	}
	diskIdx, err := os.ReadFile(filepath.Join(dst, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	ServeWithSeed(w, httptest.NewRequest("GET", "/", nil), "cloud", s1)
	if string(diskIdx) != w.Body.String() {
		t.Fatal("InstallWithSeed index.html differs from ServeWithSeed")
	}
	if strings.Contains(string(diskIdx), "Nimbus Storage") {
		t.Fatal("default brand leaked into mutated cloud index.html")
	}
}

func TestLoadOrCreateSeed(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "decoy.seed")
	s1 := LoadOrCreateSeed(p)
	s2 := LoadOrCreateSeed(p)
	if len(s1) < 16 || s1 != s2 {
		t.Fatalf("seed not persisted: %q vs %q", s1, s2)
	}
}

func TestCustomPathValidationAndServing(t *testing.T) {
	for _, bad := range []string{
		"relative/path",
		"/",
		"/etc/passwd",
		"/etc",
		"/var/lib/vynel/data",
		"/root/index.html",
		"/var/www/../etc",
		"/var/www/bad;name",
		"/var/www/with space",
	} {
		if _, err := ValidateCustomPath(bad); err == nil {
			t.Fatalf("ValidateCustomPath(%q) should have failed", bad)
		}
	}

	tmp := t.TempDir()
	// Single HTML file.
	single := filepath.Join(tmp, "stub.html")
	if err := os.WriteFile(single, []byte("<!doctype html><h1>My Stub</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	cleaned, isFile, err := CheckCustomPath(single)
	if err != nil || !isFile || cleaned != single {
		t.Fatalf("CheckCustomPath(file): cleaned=%q isFile=%v err=%v", cleaned, isFile, err)
	}
	w := httptest.NewRecorder()
	ServeCustom(w, httptest.NewRequest("GET", "/anything", nil), single, "cloud", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "My Stub") {
		t.Fatalf("ServeCustom(file): %d %q", w.Code, w.Body.String())
	}
	if nf := NotFoundCustom(single, "cloud", ""); !strings.Contains(string(nf), "My Stub") {
		t.Fatalf("NotFoundCustom(file): %q", nf)
	}

	// Non-HTML file rejected.
	txt := filepath.Join(tmp, "note.txt")
	if err := os.WriteFile(txt, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := CheckCustomPath(txt); err == nil {
		t.Fatal("CheckCustomPath accepted .txt file")
	}

	// Directory without index.html rejected.
	emptyDir := filepath.Join(tmp, "empty")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := CheckCustomPath(emptyDir); err == nil {
		t.Fatal("CheckCustomPath accepted directory without index.html")
	}

	// Directory with index.html, about.html and 404.html.
	siteDir := filepath.Join(tmp, "site")
	if err := os.MkdirAll(siteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(siteDir, "index.html"), []byte("<h1>Custom Home</h1>"), 0o644)
	_ = os.WriteFile(filepath.Join(siteDir, "about.html"), []byte("<h1>Custom About</h1>"), 0o644)
	_ = os.WriteFile(filepath.Join(siteDir, "404.html"), []byte("<h1>Custom 404</h1>"), 0o644)

	cleaned, isFile, err = CheckCustomPath(siteDir)
	if err != nil || isFile || cleaned != siteDir {
		t.Fatalf("CheckCustomPath(dir): cleaned=%q isFile=%v err=%v", cleaned, isFile, err)
	}
	w = httptest.NewRecorder()
	ServeCustom(w, httptest.NewRequest("GET", "/", nil), siteDir, "cloud", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Custom Home") {
		t.Fatalf("ServeCustom(dir /): %d %q", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	ServeCustom(w, httptest.NewRequest("GET", "/about", nil), siteDir, "cloud", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Custom About") {
		t.Fatalf("ServeCustom(dir /about): %d %q", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	ServeCustom(w, httptest.NewRequest("GET", "/missing", nil), siteDir, "cloud", "")
	if w.Code != 404 || !strings.Contains(w.Body.String(), "Custom 404") {
		t.Fatalf("ServeCustom(dir /missing): %d %q", w.Code, w.Body.String())
	}
}
