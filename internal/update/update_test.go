package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSource(t *testing.T) {
	dir := t.TempDir()
	EnvFile = filepath.Join(dir, "install.env")
	if s := LoadSource(); s.Ref != "main" || s.Release != "edge-main" {
		t.Fatalf("defaults: %+v", s)
	}
	env := "# comment\nVYNEL_REF=claude/x\nVYNEL_GH_PROXY=https://ghfast.top/\nVYNEL_RELEASE_URL=javascript:alert(1)\nVYNEL_RELEASE=\nEVIL=1\n"
	if err := os.WriteFile(EnvFile, []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	s := LoadSource()
	if s.Ref != "claude/x" || s.Release != "edge-claude-x" || s.GHProxy != "https://ghfast.top/" || s.ReleaseURL != "" {
		t.Fatalf("parsed: %+v", s)
	}
	if got := s.scriptURL(); got != "https://ghfast.top/https://raw.githubusercontent.com/vyto4ka/vynel/claude/x/scripts/install.sh" {
		t.Fatalf("script url %q", got)
	}
}

func TestLatest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/good/VERSION":
			_, _ = w.Write([]byte("abc1234\n"))
		case "/bad/VERSION":
			_, _ = w.Write([]byte("<html>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	if v, err := Latest(ctx, Source{ReleaseURL: srv.URL + "/good"}); err != nil || v != "abc1234" {
		t.Fatalf("good: %q %v", v, err)
	}
	if _, err := Latest(ctx, Source{ReleaseURL: srv.URL + "/bad"}); err == nil {
		t.Fatal("a page that is not a version must be rejected")
	}
	if _, err := Latest(ctx, Source{ReleaseURL: srv.URL + "/none"}); err == nil {
		t.Fatal("404 must be an error")
	}
}

func TestResultAndTail(t *testing.T) {
	dir := t.TempDir()
	ResultFile = filepath.Join(dir, "r", "last.json")
	if LastResult() != nil {
		t.Fatal("no result yet")
	}
	log := filepath.Join(dir, "log")
	_ = os.WriteFile(log, []byte("a\n\x1b[31m  ✗ boom\x1b[0m\r\n\nb\nc\n"), 0o600)
	saveResult(Result{OK: false, Error: tail(log, 3)})
	r := LastResult()
	if r == nil || r.OK || r.Error != "✗ boom\nb\nc" {
		t.Fatalf("result %+v", r)
	}
}
