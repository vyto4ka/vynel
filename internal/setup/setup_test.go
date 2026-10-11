package setup

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestShellAnswersAreSafe(t *testing.T) {
	a := &Answers{Mode: "aio", Domain: "nl.example.com", Name: "It's $(rm -rf /) `x`", BotToken: "1:AA'b"}
	var buf bytes.Buffer
	if err := a.Shell(&buf); err != nil {
		t.Fatal(err)
	}
	// Source it in bash and read the values back: nothing runs, everything round-trips.
	f := filepath.Join(t.TempDir(), "a.env")
	if err := os.WriteFile(f, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("bash", "-c", `set -eu; source "$1"; printf '%s|%s|%s' "$MODE" "$NAME" "$BOT_TOKEN"`, "_", f).Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "aio|It's $(rm -rf /) `x`|1:AA'b" {
		t.Fatalf("round trip: %q", out)
	}
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if !strings.Contains(l, "='") {
			t.Fatalf("unquoted line %q", l)
		}
	}
}

func TestCloudflareAndDomains(t *testing.T) {
	if !IsCloudflare("104.21.5.9") || !IsCloudflare("172.67.1.1") || IsCloudflare("176.125.254.200") {
		t.Fatal("cloudflare ranges")
	}
	if validDomain("https://FI.Kequing.tech/") != nil || validDomain("localhost") == nil {
		t.Fatal("domain check")
	}
	if !isPrivate("10.0.0.5") || !isPrivate("100.64.1.1") || isPrivate("203.0.113.7") {
		t.Fatal("private ranges")
	}
}

func TestStateKeepsAnswersButFlagsWin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	saveState(path, &state{Answers: Answers{Mode: "aio", Domain: "old.example.com", BotToken: "1:AAx", SubPath: "/sub/"}, Security: []string{"port"}})
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("state file must be root-only: %v %v", fi, err)
	}
	s := loadState(path)
	if s == nil || s.BotToken != "1:AAx" || len(s.Security) != 1 {
		t.Fatalf("round trip: %+v", s)
	}
	a := &Answers{Domain: "flag.example.com", AdminLogin: "admin"}
	a.restore(&s.Answers, Options{Domain: "flag.example.com"})
	if a.Domain != "flag.example.com" || a.BotToken != "1:AAx" || a.SubPath != "/sub/" || a.AdminLogin != "admin" {
		t.Fatalf("restore: %+v", a)
	}
	if loadState(filepath.Join(t.TempDir(), "none")) != nil {
		t.Fatal("missing file")
	}
}
