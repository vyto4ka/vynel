package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	if got := s.releaseURL(); got != "https://ghfast.top/https://github.com/vyto4ka/vynel/releases/download/edge-claude-x" {
		t.Fatalf("release url %q", got)
	}
	if s.Channel() != ChannelDev {
		t.Fatalf("an edge release is the dev channel: %+v", s)
	}
}

func TestStableChannel(t *testing.T) {
	dir := t.TempDir()
	EnvFile = filepath.Join(dir, "etc", "install.env")
	// What the installer writes for a new install.
	if err := SaveSource(Source{Ref: "main", Release: ReleaseLatest, GHProxy: "https://ghfast.top/"}); err != nil {
		t.Fatal(err)
	}
	s := LoadSource()
	if s.Release != ReleaseLatest || s.Channel() != ChannelStable || s.Ref != "main" {
		t.Fatalf("stable: %+v", s)
	}
	if got := s.releaseURL(); got != "https://ghfast.top/https://github.com/vyto4ka/vynel/releases/latest/download" {
		t.Fatalf("latest url %q", got)
	}
	if got := (Source{Release: "v1.2.3"}).releaseURL(); got != "https://github.com/vyto4ka/vynel/releases/download/v1.2.3" {
		t.Fatalf("pinned url %q", got)
	}
	if (Source{Release: "v1.2.3"}).Channel() != ChannelStable {
		t.Fatal("a pinned release is stable")
	}
}

func TestWithChannel(t *testing.T) {
	stable := Source{Ref: "main", Release: ReleaseLatest, ReleaseURL: "https://mirror.example/r", GHProxy: "https://ghfast.top/"}
	dev, err := stable.WithChannel(ChannelDev, "claude/x")
	if err != nil || dev.Release != "edge-claude-x" || dev.Ref != "claude/x" || dev.ReleaseURL != "" || dev.GHProxy != stable.GHProxy {
		t.Fatalf("to dev: %+v %v", dev, err)
	}
	if again, err := dev.WithChannel(ChannelDev, ""); err != nil || again.Release != "edge-claude-x" {
		t.Fatalf("dev without a branch keeps the current one: %+v %v", again, err)
	}
	back, err := dev.WithChannel(ChannelStable, "")
	if err != nil || back.Release != ReleaseLatest || back.Channel() != ChannelStable {
		t.Fatalf("back to stable: %+v %v", back, err)
	}
	for _, c := range [][2]string{{ChannelStable, "main"}, {"nightly", ""}, {ChannelDev, "bad branch;rm"}} {
		if _, err := stable.WithChannel(c[0], c[1]); err == nil {
			t.Errorf("accepted channel %q ref %q", c[0], c[1])
		}
	}
	// What is saved is what is loaded.
	EnvFile = filepath.Join(t.TempDir(), "install.env")
	if err := SaveSource(dev); err != nil {
		t.Fatal(err)
	}
	if got := LoadSource(); got != dev {
		t.Fatalf("round trip: %+v, want %+v", got, dev)
	}
}

// A node installed before channels follows edge-main; told the panel's release, it moves there
// (keeping its mirror) and stays on it for later updates.
func TestFollowThePanel(t *testing.T) {
	old := Source{Ref: "main", Release: "edge-main", ReleaseURL: "https://mirror.example/r", GHProxy: "https://ghfast.top/"}
	s, err := old.Follow(ReleaseLatest, "main")
	if err != nil || s.Release != ReleaseLatest || s.Ref != "main" || s.ReleaseURL != "" || s.GHProxy != old.GHProxy || s.Channel() != ChannelStable {
		t.Fatalf("to stable: %+v %v", s, err)
	}
	if same, err := old.Follow("edge-main", ""); err != nil || same != old {
		t.Fatalf("the same release changes nothing: %+v %v", same, err)
	}
	dev, err := s.Follow("edge-test", "test")
	if err != nil || dev.Release != "edge-test" || dev.Ref != "test" || dev.Channel() != ChannelDev {
		t.Fatalf("to the panel's test builds: %+v %v", dev, err)
	}
	for _, c := range [][2]string{{"", ""}, {"latest;rm", ""}, {"edge-x", "bad branch"}} {
		if _, err := old.Follow(c[0], c[1]); err == nil {
			t.Errorf("accepted release %q ref %q", c[0], c[1])
		}
	}
}

// Without a release key the update still runs the installer of its release, not one from a
// branch, and the result is recorded.
func TestRunTakesTheInstallerFromTheRelease(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		if r.URL.Path == "/rel/install.sh" {
			_, _ = w.Write([]byte("#!/bin/sh\necho \"installer of the release: $*\"\n"))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	dir := t.TempDir()
	ResultFile, LogFile = filepath.Join(dir, "r", "last.json"), filepath.Join(dir, "update.log")
	var out strings.Builder
	if err := run(context.Background(), &out, Source{Ref: "main", Release: ReleaseLatest, ReleaseURL: srv.URL + "/rel"}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "installer of the release: --mode update --yes") {
		t.Fatalf("the release's installer did not run:\n%s", out.String())
	}
	if len(asked) != 1 || asked[0] != "/rel/install.sh" {
		t.Fatalf("asked %v", asked)
	}
	if r := LastResult(); r == nil || r.Running || !r.OK {
		t.Fatalf("result %+v", r)
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
