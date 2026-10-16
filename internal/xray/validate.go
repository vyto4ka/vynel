// Package xray runs the Xray binary and talks to its gRPC API.
package xray

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Binary locates the Xray executable and its geo assets.
type Binary struct {
	Path     string // path to xray
	AssetDir string // directory with geoip.dat/geosite.dat (XRAY_LOCATION_ASSET)
}

func (b Binary) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, b.Path, args...)
	cmd.Env = os.Environ()
	if b.AssetDir != "" {
		cmd.Env = append(cmd.Env, "XRAY_LOCATION_ASSET="+b.AssetDir)
	}
	return cmd
}

// Version returns the Xray version, e.g. "26.3.27".
func (b Binary) Version(ctx context.Context) (string, error) {
	out, err := b.command(ctx, "version").Output()
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(string(out), "\n")
	if f := strings.Fields(line); len(f) >= 2 && f[0] == "Xray" {
		return f[1], nil
	}
	return strings.TrimSpace(line), nil
}

// Test checks a config with `xray run -test` without starting anything.
func (b Binary) Test(ctx context.Context, cfg []byte) error {
	dir, err := os.MkdirTemp("", "xray-test-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, cfg, 0o600); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var out bytes.Buffer
	cmd := b.command(ctx, "run", "-test", "-c", path)
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("xray rejected the config: %s", lastLines(out.String(), 5))
	}
	return nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

// SetRules replaces the routing rules of a running Xray through its API (`xray api adrules`,
// which builds them the same way a config file is read). routing is a config "routing" section:
// its rules and balancers replace the running ones; the domain strategy stays.
func (b Binary) SetRules(ctx context.Context, apiAddr string, routing map[string]any) error {
	raw, err := json.Marshal(map[string]any{"routing": routing})
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "xray-rules-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "rules.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var out bytes.Buffer
	cmd := b.command(ctx, "api", "adrules", "-s", apiAddr, "-t", "20", path)
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("xray api adrules: %w: %s", err, lastLines(out.String(), 5))
	}
	return nil
}
