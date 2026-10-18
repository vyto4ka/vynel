package caddyconf

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildMergesRoutesOfOneDomain(t *testing.T) {
	cfg, err := Build(Spec{Sites: []Site{
		{Domain: "nl.example.com", LocalPort: 8443, Routes: []Route{{Kind: KindDecoy, Decoy: "cloud"}}},
		{Domain: "NL.example.com", LocalPort: 8443, Routes: []Route{{Kind: KindProxy, Upstream: "127.0.0.1:2096", PathPrefix: "/s/"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(cfg)
	if strings.Count(s, `"host":["nl.example.com"]`) != 2 { // one route + one error route
		t.Fatalf("domain must be merged: %s", s)
	}
	proxy, decoy := strings.Index(s, `"reverse_proxy"`), strings.Index(s, `"file_server","root":"{env.VYNEL_DECOY_DIR}/cloud"}`)
	if proxy < 0 || decoy < 0 || proxy > decoy {
		t.Fatalf("the prefixed proxy route must come before the catch-all decoy: %s", s)
	}
	for _, want := range []string{`"127.0.0.1:8443"`, `":80"`, `"tls-alpn":{"disabled":true}`, `"automate":["nl.example.com"]`, `"status_code":301`} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %s in %s", want, s)
		}
	}
}

func TestBuildDomainOnTwoListeners(t *testing.T) {
	// The subscription domain behind Reality and the panel on its own public port.
	cfg, err := Build(Spec{Sites: []Site{
		{Domain: "a.example.com", LocalPort: 8443, Routes: []Route{{Kind: KindDecoy, Decoy: "cloud"}}},
		{Domain: "A.example.com", Bind: "203.0.113.10", Port: 47321, Routes: []Route{{Kind: KindDecoy, Decoy: "cloud"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(cfg)
	for _, want := range []string{`"127.0.0.1:8443"`, `"203.0.113.10:47321"`, `"automate":["a.example.com"]`} {
		if !strings.Contains(s, want) {
			t.Errorf("no %s in %s", want, s)
		}
	}
}

func TestEmptySpec(t *testing.T) {
	if cfg, err := Build(Spec{}); err != nil || cfg != nil {
		t.Fatalf("%s %v", cfg, err)
	}
}

// With CADDY_BIN set, the generated config must pass `caddy validate`.
func TestCaddyValidates(t *testing.T) {
	bin := os.Getenv("CADDY_BIN")
	if bin == "" {
		t.Skip("CADDY_BIN not set")
	}
	cfg, err := Build(Spec{Issuer: "internal", HTTPPort: 18080, Sites: []Site{
		{Domain: "nl.example.com", LocalPort: 18443, Routes: []Route{{Kind: KindDecoy, Decoy: "cloud"}, {Kind: KindProxy, Upstream: "127.0.0.1:2096", PathPrefix: "/s/"}}},
		{Domain: "origin.example.com", Port: 18444, Routes: []Route{{Kind: KindStream, Upstream: "127.0.0.1:8085", PathPrefix: "/upload"}, {Kind: KindDecoy, Decoy: "docs"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var pretty map[string]any
	if err := json.Unmarshal(cfg, &pretty); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "caddy.json")
	if err := os.WriteFile(path, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "validate", "--config", path)
	cmd.Env = append(os.Environ(), "XDG_DATA_HOME="+t.TempDir(), "XDG_CONFIG_HOME="+t.TempDir(), "VYNEL_DECOY_DIR="+t.TempDir())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("caddy validate: %v\n%s", err, out)
	}
}
