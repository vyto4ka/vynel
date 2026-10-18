package xray

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Process supervises one Xray process: writes its config, restarts it on change or crash
// and keeps the tail of its output for diagnostics.
type Process struct {
	Bin        Binary
	ConfigPath string
	Log        *slog.Logger

	mu       sync.Mutex
	cmd      *exec.Cmd
	done     chan struct{}
	stopping bool
	tail     *ring
	closed   bool
}

// NewProcess creates a supervisor; nothing runs until Apply.
func NewProcess(bin Binary, configPath string, log *slog.Logger) *Process {
	if log == nil {
		log = slog.Default()
	}
	p := &Process{Bin: bin, ConfigPath: configPath, Log: log, tail: newRing(200)}
	// Xray's own log goes to the journal too: its level comes from the panel (node.xray_log).
	p.tail.emit = func(line string) { p.Log.Info(line, "src", "xray") }
	return p
}

// Apply validates cfg, writes it atomically and (re)starts Xray. On validation failure the
// running process and the old config are left untouched.
func (p *Process) Apply(ctx context.Context, cfg []byte) error {
	if err := p.Bin.Test(ctx, cfg); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.ConfigPath), 0o700); err != nil {
		return err
	}
	tmp := p.ConfigPath + ".new"
	if err := os.WriteFile(tmp, cfg, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, p.ConfigPath); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errors.New("process closed")
	}
	p.stopLocked()
	return p.startLocked()
}

// Running reports whether Xray is up.
func (p *Process) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cmd != nil
}

// Tail returns the last lines Xray printed.
func (p *Process) Tail() []string { return p.tail.lines() }

// Close stops Xray for good.
func (p *Process) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.stopLocked()
}

func (p *Process) startLocked() error {
	cmd := p.Bin.command(context.Background(), "run", "-c", p.ConfigPath)
	cmd.Stdout, cmd.Stderr = p.tail, p.tail
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start xray: %w", err)
	}
	done := make(chan struct{})
	p.cmd, p.done, p.stopping = cmd, done, false
	p.Log.Info("xray started", "pid", cmd.Process.Pid)
	go p.wait(cmd, done)
	return nil
}

func (p *Process) wait(cmd *exec.Cmd, done chan struct{}) {
	err := cmd.Wait()
	close(done)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != cmd {
		return
	}
	p.cmd = nil
	if p.stopping || p.closed {
		return
	}
	p.Log.Warn("xray exited unexpectedly, restarting in 2s", "err", err, "tail", lastLines(joinLines(p.tail.lines()), 3))
	go func() {
		time.Sleep(2 * time.Second)
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.cmd == nil && !p.closed {
			if err := p.startLocked(); err != nil {
				p.Log.Error("xray restart failed", "err", err)
			}
		}
	}()
}

// stopLocked sends SIGTERM and waits up to 5s before killing.
func (p *Process) stopLocked() {
	if p.cmd == nil {
		return
	}
	cmd, done := p.cmd, p.done
	p.stopping = true
	_ = cmd.Process.Signal(syscall.SIGTERM)
	p.mu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
	}
	p.mu.Lock()
	if p.cmd == cmd {
		p.cmd = nil
	}
}

func joinLines(ls []string) string {
	out := ""
	for _, l := range ls {
		out += l + "\n"
	}
	return out
}

// ring is a line-oriented ring buffer used as Xray's stdout/stderr.
type ring struct {
	mu   sync.Mutex
	buf  []string
	max  int
	part []byte
	emit func(line string) // called for every complete line, under mu
}

func newRing(n int) *ring { return &ring{max: n} }

func (r *ring) Write(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range b {
		if c == '\n' {
			if r.emit != nil {
				r.emit(string(r.part))
			}
			r.buf = append(r.buf, string(r.part))
			r.part = r.part[:0]
			if len(r.buf) > r.max {
				r.buf = r.buf[len(r.buf)-r.max:]
			}
			continue
		}
		r.part = append(r.part, c)
	}
	return len(b), nil
}

func (r *ring) lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.buf...)
}
