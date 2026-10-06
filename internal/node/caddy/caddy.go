// Package caddy supervises the node's Caddy: decoy sites, self-steal target behind Reality,
// TLS for XHTTP origins and the panel's subscription domain (docs/ARCHITECTURE.md §4.4).
package caddy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/vyto4ka/vpn/internal/decoy"
)

// Manager runs Caddy as a child process and reloads it through the admin API.
type Manager struct {
	Bin     string // path to caddy; "" or missing = Caddy unavailable
	DataDir string // certificates, config, decoy sites
	Log     *slog.Logger

	mu       sync.Mutex
	cmd      *exec.Cmd
	done     chan struct{}
	admin    string
	current  []byte
	stopping bool
	closed   bool
	tail     tailBuffer
}

// Available reports whether the Caddy binary exists.
func (m *Manager) Available() bool {
	if m.Bin == "" {
		return false
	}
	_, err := os.Stat(m.Bin)
	return err == nil
}

// Version returns `caddy version` (first word), or "" if unavailable.
func (m *Manager) Version(ctx context.Context) string {
	if !m.Available() {
		return ""
	}
	out, err := exec.CommandContext(ctx, m.Bin, "version").Output()
	if err != nil {
		return ""
	}
	f := strings.Fields(string(out))
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// Apply makes Caddy run cfg. An empty cfg stops Caddy. A running Caddy is reloaded without
// dropping connections; a rejected config leaves the old one running and returns the error.
func (m *Manager) Apply(ctx context.Context, cfg []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("caddy manager closed")
	}
	if len(cfg) == 0 {
		m.stopLocked()
		m.current = nil
		return nil
	}
	if m.cmd != nil && bytes.Equal(cfg, m.current) {
		return nil
	}
	if !m.Available() {
		return fmt.Errorf("caddy is required by this node's config but not installed at %q", m.Bin)
	}
	var meta struct {
		Admin struct {
			Listen string `json:"listen"`
		} `json:"admin"`
	}
	if err := json.Unmarshal(cfg, &meta); err != nil || meta.Admin.Listen == "" {
		return fmt.Errorf("caddy config without admin.listen: %w", err)
	}
	if err := m.installDecoys(); err != nil {
		return err
	}
	path := filepath.Join(m.DataDir, "caddy.json")
	if err := os.WriteFile(path+".new", cfg, 0o600); err != nil {
		return err
	}
	if m.cmd != nil && m.admin == meta.Admin.Listen {
		if err := m.load(ctx, cfg); err != nil {
			return err
		}
	} else {
		m.stopLocked()
		if err := os.Rename(path+".new", path); err != nil {
			return err
		}
		if err := m.startLocked(path, meta.Admin.Listen); err != nil {
			return err
		}
	}
	_ = os.Rename(path+".new", path)
	m.current = cfg
	return nil
}

// Close stops Caddy.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	m.stopLocked()
}

func (m *Manager) installDecoys() error {
	dir := filepath.Join(m.DataDir, "decoy")
	for _, name := range decoy.Names() {
		if _, err := decoy.Install(dir, name); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) env() []string {
	data := filepath.Join(m.DataDir, "caddy")
	return append(os.Environ(),
		"XDG_DATA_HOME="+data, "XDG_CONFIG_HOME="+data, "HOME="+data,
		"VPN_DECOY_DIR="+filepath.Join(m.DataDir, "decoy"))
}

func (m *Manager) startLocked(path, admin string) error {
	if err := os.MkdirAll(filepath.Join(m.DataDir, "caddy"), 0o700); err != nil {
		return err
	}
	m.tail.Reset()
	cmd := exec.Command(m.Bin, "run", "--config", path)
	cmd.Env = m.env()
	cmd.Stdout, cmd.Stderr = &m.tail, &m.tail
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start caddy: %w", err)
	}
	done := make(chan struct{})
	m.cmd, m.done, m.admin, m.stopping = cmd, done, admin, false
	go m.wait(cmd, done, path, admin)
	// Wait for the admin endpoint: proves the config was accepted and listeners are up.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			return fmt.Errorf("caddy exited: %s", lastLines(m.tail.String(), 5))
		default:
		}
		resp, err := http.Get("http://" + admin + "/config/")
		if err == nil {
			resp.Body.Close()
			m.Log.Info("caddy started", "pid", cmd.Process.Pid)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("caddy admin API did not come up: %s", lastLines(m.tail.String(), 5))
}

func (m *Manager) wait(cmd *exec.Cmd, done chan struct{}, path, admin string) {
	err := cmd.Wait()
	close(done)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd != cmd {
		return
	}
	m.cmd = nil
	if m.stopping || m.closed {
		return
	}
	m.Log.Warn("caddy exited unexpectedly, restarting in 2s", "err", err, "tail", lastLines(m.tail.String(), 3))
	go func() {
		time.Sleep(2 * time.Second)
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.cmd == nil && !m.closed && m.current != nil {
			if err := m.startLocked(path, admin); err != nil {
				m.Log.Error("caddy restart failed", "err", err)
			}
		}
	}()
}

func (m *Manager) stopLocked() {
	if m.cmd == nil {
		return
	}
	cmd, done := m.cmd, m.done
	m.stopping = true
	_ = cmd.Process.Signal(syscall.SIGTERM)
	m.mu.Unlock()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
	}
	m.mu.Lock()
	if m.cmd == cmd {
		m.cmd = nil
	}
}

func (m *Manager) load(ctx context.Context, cfg []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+m.admin+"/load", bytes.NewReader(cfg))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("caddy reload: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("caddy rejected the config: %s", strings.TrimSpace(string(body)))
	}
	return nil
}

// tailBuffer keeps the last 64 KiB of Caddy's output; safe for concurrent use.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 64<<10 {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-32<<10:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

func (t *tailBuffer) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}
