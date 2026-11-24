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

func TestShellStripsLineBreaksAndSubPathRoot(t *testing.T) {
	a := &Answers{Mode: "panel", Domain: "panel.example.com", SubDomain: "sub.example.com", SubPath: "/", Name: "line1\nline2\r\nline3"}
	var buf bytes.Buffer
	if err := a.Shell(&buf); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != 25 {
		t.Fatalf("expected 25 single-line assignments, got %d:\n%s", len(lines), buf.String())
	}
	if cleanSubPath("/") != "/" || cleanSubPath("sub") != "/sub/" || cleanSubPath("") != "" {
		t.Fatalf("cleanSubPath: %q %q %q", cleanSubPath("/"), cleanSubPath("sub"), cleanSubPath(""))
	}
	cache := &dnsCache{m: map[string]DNSStatus{
		"panel.example.com@203.0.113.10": {OK: true, Message: "✓ 203.0.113.10"},
		"sub.example.com@203.0.113.10":   {OK: true, Message: "✓ 203.0.113.10"},
	}}
	sum := summary(a, "panel.example.com", true, []string{"firewall"}, cache, func(string) string { return "203.0.113.10" })
	for _, want := range []string{
		"Домен        panel.example.com",
		"Домен ссылок sub.example.com",
		"Домен панели panel.example.com",
		"Подписки     https://sub.example.com/<токен>",
	} {
		if !strings.Contains(sum, want) {
			t.Fatalf("summary missing %q:\n%s", want, sum)
		}
	}
}

func TestValidSystemHostname(t *testing.T) {
	good := map[string]string{
		"nl.example.com":     "nl.example.com",
		"NL.Example.COM.\n":  "nl.example.com",
		"  node-1.my.org.  ": "node-1.my.org",
	}
	for raw, want := range good {
		got, ok := validSystemHostname(raw)
		if !ok || got != want {
			t.Fatalf("validSystemHostname(%q) = (%q, %v), want (%q, true)", raw, got, ok, want)
		}
	}
	longLabel := strings.Repeat("a", 64) + ".example.com"
	longHost := strings.Repeat("a.", 128) + "com"
	bad := []string{
		"",
		"ubuntu",
		"localhost",
		"vps.localdomain",
		"server.local",
		"host.internal",
		"box.lan",
		"host.home.arpa",
		"foo.localhost",
		"bad_host.example.com",
		"https://nl.example.com/path",
		"nl.example.com/extra",
		"-bad.example.com",
		"bad-.example.com",
		"a..b.example.com",
		"1.2.3.4",
		"node.example.com..",
		longLabel,
		longHost,
	}
	for _, raw := range bad {
		if got, ok := validSystemHostname(raw); ok {
			t.Fatalf("validSystemHostname(%q) unexpectedly accepted as %q", raw, got)
		}
	}
	if validDomain(longLabel) == nil || validDomain(longHost) == nil {
		t.Fatal("validDomain must reject overlong labels and hostnames")
	}
}
