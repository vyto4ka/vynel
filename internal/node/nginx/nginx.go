// Package nginx supervises the node's nginx: decoy sites, the self-steal target behind
// Reality, the SNI router on a shared port, TLS for XHTTP origins and the panel's own sites
// (docs/ARCHITECTURE.md §4.4). Certificates come from package certs.
package nginx

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/vyto4ka/vynel/internal/certs"
	"github.com/vyto4ka/vynel/internal/decoy"
	"github.com/vyto4ka/vynel/internal/nginxconf"
)

// DefaultBin is where Debian and Ubuntu install nginx.
const DefaultBin = "/usr/sbin/nginx"

// Manager runs nginx as a child process (its own instance, not the system nginx.service).
type Manager struct {
	Bin     string // path to nginx; "" = DefaultBin
	DataDir string // config and certificates (root only)
	WWWDir  string // what nginx workers read and write: decoys, ACME webroot, temp files
	User    string // worker user; "" = www-data if it exists, else nobody
	Log     *slog.Logger
	// OnCerts is called after certificates were renewed (Xray may use them too).
	OnCerts func()
	// CaddyStorage is the storage of the Caddy this node ran before: its certificates are
	// imported instead of asking Let's Encrypt again.
	CaddyStorage string

	Certs *certs.Manager

	mu       sync.Mutex
	cmd      *exec.Cmd
	done     chan struct{}
	current  string // config as received (before Finalize)
	stopping bool
	closed   bool
	local    *nginxconf.Local
	noStream string // why the stream module is unavailable ("" = it is there)
}

func (m *Manager) bin() string {
	if m.Bin == "" {
		return DefaultBin
	}
	return m.Bin
}

// Available reports whether the nginx binary exists.
func (m *Manager) Available() bool {
	_, err := os.Stat(m.bin())
	return err == nil
}

// Version returns the nginx version ("1.24.0"), or "" if unavailable.
func (m *Manager) Version(ctx context.Context) string {
	if !m.Available() {
		return ""
	}
	out, _ := exec.CommandContext(ctx, m.bin(), "-v").CombinedOutput()
	if _, v, ok := strings.Cut(strings.TrimSpace(string(out)), "nginx/"); ok {
		return strings.Fields(v)[0]
	}
	return ""
}

// Init prepares the directories and the certificate manager; Apply calls it.
func (m *Manager) Init() error {
	if m.Log == nil {
		m.Log = slog.Default()
	}
	if m.WWWDir == "" {
		m.WWWDir = filepath.Join(m.DataDir, "www")
	}
	if m.Certs == nil {
		m.Certs = &certs.Manager{Dir: filepath.Join(m.DataDir, "tls"), Log: m.Log}
	}
	m.Certs.Webroot = m.acmeDir()
	for _, d := range []string{filepath.Join(m.DataDir, "nginx"), m.Certs.Dir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	for _, d := range []string{m.WWWDir, m.decoyDir(), m.acmeDir(), m.runDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
		_ = os.Chmod(d, 0o755)
	}
	return nil
}

func (m *Manager) confPath() string { return filepath.Join(m.DataDir, "nginx", "nginx.conf") }
func (m *Manager) decoyDir() string { return filepath.Join(m.WWWDir, "decoy") }
func (m *Manager) acmeDir() string  { return filepath.Join(m.WWWDir, "acme") }
func (m *Manager) runDir() string   { return filepath.Join(m.WWWDir, "run") }

// DecoyDir is where decoy sites are installed (Hysteria2 masquerade uses them too).
func (m *Manager) DecoyDir() string {
	if m.WWWDir == "" {
		return filepath.Join(m.DataDir, "www", "decoy")
	}
	return m.decoyDir()
}

// Running reports whether nginx runs a config.
func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cmd != nil
}

// Apply makes nginx run conf (as built by nginxconf.Build). An empty conf stops nginx. A
// running nginx is reloaded without dropping connections; a config nginx rejects leaves the
// old one running and returns the error.
func (m *Manager) Apply(ctx context.Context, conf string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("nginx manager closed")
	}
	if conf == "" {
		m.stopLocked()
		m.current = ""
		return nil
	}
	if m.cmd != nil && conf == m.current {
		return nil
	}
	if !m.Available() {
		return fmt.Errorf("nginx is required by this node's config but not installed at %q (apt install nginx libnginx-mod-stream)", m.bin())
	}
	if err := m.Init(); err != nil {
		return err
	}
	if err := m.installDecoys(); err != nil {
		return err
	}
	domains := nginxconf.CertDomains(conf)
	if _, err := m.Certs.Ensure(domains); err != nil {
		return err
	}
	if m.CaddyStorage != "" {
		if got := m.ImportCaddy(m.CaddyStorage, domains); len(got) > 0 {
			m.Log.Info("certificates imported from Caddy", "domains", got)
		}
	}
	final, err := m.finalize(ctx, conf)
	if err != nil {
		return err
	}
	path := m.confPath()
	if err := os.WriteFile(path+".new", []byte(final), 0o600); err != nil {
		return err
	}
	if err := m.test(ctx, path+".new"); err != nil {
		return err
	}
	if err := os.Rename(path+".new", path); err != nil {
		return err
	}
	if m.cmd != nil {
		if err := m.reloadLocked(); err != nil {
			return err
		}
	} else if err := m.startLocked(); err != nil {
		return err
	}
	m.current = conf
	return nil
}

func (m *Manager) finalize(ctx context.Context, conf string) (string, error) {
	stream := strings.Contains(conf, "\nstream {")
	// Without the stream module, look again: it may have been installed since.
	if m.local == nil || (stream && m.noStream != "") {
		l, err := m.detect(ctx)
		if err != nil {
			return "", err
		}
		m.local = l
	}
	if stream && m.noStream != "" {
		// nginx would only say: unknown directive "stream".
		return "", fmt.Errorf("SNI routing on a shared port needs the nginx stream module: %s", m.noStream)
	}
	l := *m.local
	l.Run, l.Certs, l.Decoy, l.ACME = m.runDir(), m.Certs.Dir, m.decoyDir(), m.acmeDir()
	if !l.RejectHandshake {
		var err error
		if l.DummyCert, l.DummyKey, err = m.Certs.Dummy(); err != nil {
			return "", err
		}
	}
	if !stream {
		l.Modules = nil
	}
	return nginxconf.Finalize(conf, l), nil
}

var (
	modulesPathRe  = regexp.MustCompile(`--modules-path=(\S+)`)
	staticStreamRe = regexp.MustCompile(`--with-stream(\s|$)`)
)

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// detect finds the stream module, the worker user and IPv6 support.
func (m *Manager) detect(ctx context.Context) (*nginxconf.Local, error) {
	out, err := exec.CommandContext(ctx, m.bin(), "-V").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("nginx -V: %w", err)
	}
	l := &nginxconf.Local{}
	m.noStream = ""
	// ssl_reject_handshake appeared in nginx 1.19.4 (Ubuntu 22.04 and Debian 11 ship 1.18).
	if v, _ := exec.CommandContext(ctx, m.bin(), "-v").CombinedOutput(); versionAtLeast(string(v), 1, 19, 4) {
		l.RejectHandshake = true
	}
	// The stream module is either built in (--with-stream) or a file in the modules dir. Do
	// not go by --with-stream=dynamic: Ubuntu 22.04 builds its modules in a separate pass and
	// its nginx -V does not mention stream at all.
	dir := "/usr/lib/nginx/modules"
	if mm := modulesPathRe.FindSubmatch(out); mm != nil {
		dir = string(mm[1])
	}
	mod := filepath.Join(dir, "ngx_stream_module.so")
	switch {
	case staticStreamRe.Match(out):
	case fileExists(mod):
		l.Modules = []string{mod}
	default:
		m.noStream = "no " + mod + ", install it: apt install libnginx-mod-stream"
		m.Log.Warn("nginx stream module missing: apt install libnginx-mod-stream", "path", mod)
	}
	l.User = m.User
	if l.User == "" {
		switch {
		case os.Getuid() != 0:
		case lookup("www-data"):
			l.User = "www-data"
		default:
			l.User = "nobody nogroup"
		}
	}
	if ln, err := net.Listen("tcp6", "[::1]:0"); err == nil {
		_ = ln.Close()
		l.IPv6 = true
	}
	if l.User != "" {
		// Workers write temp files (proxy buffers, request bodies) into the run dir.
		if u, err := user.Lookup(strings.Fields(l.User)[0]); err == nil {
			uid, _ := strconv.Atoi(u.Uid)
			gid, _ := strconv.Atoi(u.Gid)
			_ = os.Chown(m.runDir(), uid, gid)
		}
	}
	return l, nil
}

var nginxVersionRe = regexp.MustCompile(`nginx/(\d+)\.(\d+)\.(\d+)`)

func versionAtLeast(out string, major, minor, patch int) bool {
	m := nginxVersionRe.FindStringSubmatch(out)
	if m == nil {
		return false
	}
	v := [3]int{}
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	want := [3]int{major, minor, patch}
	for i := range v {
		if v[i] != want[i] {
			return v[i] > want[i]
		}
	}
	return true
}

func lookup(name string) bool {
	_, err := user.Lookup(name)
	return err == nil
}

func (m *Manager) test(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, m.bin(), "-t", "-q", "-p", m.runDir(), "-c", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("nginx rejected the config: %s", lastLines(string(out), 4))
	}
	return nil
}

func (m *Manager) startLocked() error {
	errLog := filepath.Join(m.runDir(), "error.log")
	cmd := exec.Command(m.bin(), "-p", m.runDir(), "-c", m.confPath(), "-g", "daemon off;")
	tail := &lineLog{log: m.Log}
	cmd.Stdout, cmd.Stderr = tail, tail
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	off := fileSize(errLog)
	pidFile := filepath.Join(m.runDir(), "nginx.pid")
	_ = os.Remove(pidFile)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start nginx: %w", err)
	}
	done := make(chan struct{})
	m.cmd, m.done, m.stopping = cmd, done, false
	go m.wait(cmd, done)
	// nginx writes its pid file once every listener is bound; a busy port makes it retry for a
	// few seconds and exit.
	want := strconv.Itoa(cmd.Process.Pid)
	deadline := time.After(15 * time.Second)
	for {
		select {
		case <-done:
			return fmt.Errorf("nginx exited: %s", lastLines(tail.String()+"\n"+readFrom(errLog, off), 4))
		case <-deadline:
			return fmt.Errorf("nginx did not start in 15s: %s", lastLines(tail.String()+"\n"+readFrom(errLog, off), 4))
		case <-time.After(50 * time.Millisecond):
		}
		if b, err := os.ReadFile(pidFile); err == nil && strings.TrimSpace(string(b)) == want {
			m.Log.Info("nginx started", "pid", cmd.Process.Pid)
			return nil
		}
	}
}

// reloadLocked sends SIGHUP: the master re-reads the config (already tested) and replaces the
// workers gracefully. Problems nginx finds only now (a port in use) go to the error log.
func (m *Manager) reloadLocked() error {
	errLog := filepath.Join(m.runDir(), "error.log")
	off := fileSize(errLog)
	if err := m.cmd.Process.Signal(syscall.SIGHUP); err != nil {
		return fmt.Errorf("reload nginx: %w", err)
	}
	time.Sleep(500 * time.Millisecond)
	for _, line := range strings.Split(readFrom(errLog, off), "\n") {
		if strings.Contains(line, "[emerg]") {
			return fmt.Errorf("nginx kept the old config: %s", strings.TrimSpace(line))
		}
	}
	return nil
}

// Reload re-reads the config, e.g. after certificates changed.
func (m *Manager) Reload() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd == nil {
		return nil
	}
	return m.reloadLocked()
}

// Restart stops nginx and starts it again with its config (admin action).
func (m *Manager) Restart() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("nginx manager closed")
	}
	if m.current == "" {
		return errors.New("this node's config does not use nginx")
	}
	m.stopLocked()
	return m.startLocked()
}

// Close stops nginx.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	m.stopLocked()
}

func (m *Manager) wait(cmd *exec.Cmd, done chan struct{}) {
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
	m.Log.Warn("nginx exited unexpectedly, restarting in 2s", "err", err)
	go func() {
		time.Sleep(2 * time.Second)
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.cmd == nil && !m.closed && m.current != "" {
			if err := m.startLocked(); err != nil {
				m.Log.Error("nginx restart failed", "err", err)
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
	_ = cmd.Process.Signal(syscall.SIGQUIT) // graceful: finish requests
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

func (m *Manager) installDecoys() error {
	for _, name := range decoy.Names() {
		if _, err := decoy.Install(m.decoyDir(), name); err != nil {
			return err
		}
	}
	return nil
}

// RenewCerts issues the certificates the running config needs and reloads nginx when any
// changed. Called periodically by the agent.
func (m *Manager) RenewCerts(ctx context.Context) error {
	m.mu.Lock()
	conf := m.current
	running := m.cmd != nil
	m.mu.Unlock()
	if conf == "" || !running {
		return nil
	}
	email, issuer := nginxconf.ACME(conf)
	done, err := m.Certs.Renew(ctx, nginxconf.CertDomains(conf), email, issuer)
	if len(done) > 0 {
		if rerr := m.Reload(); rerr != nil {
			err = errors.Join(err, rerr)
		}
		if m.OnCerts != nil {
			m.OnCerts()
		}
	}
	return err
}

// ImportCaddy copies certificates Caddy already obtained (its storage dir) for the domains
// that only have placeholders, so moving to nginx does not ask Let's Encrypt again.
func (m *Manager) ImportCaddy(storage string, domains []string) []string {
	var out []string
	for _, d := range domains {
		matches, _ := filepath.Glob(filepath.Join(storage, "certificates", "*", d, d+".crt"))
		for _, crtPath := range matches {
			crt, err1 := os.ReadFile(crtPath)
			key, err2 := os.ReadFile(strings.TrimSuffix(crtPath, ".crt") + ".key")
			if err1 != nil || err2 != nil {
				continue
			}
			if ok, err := m.Certs.Import(d, crt, key); err == nil && ok {
				out = append(out, d)
				break
			}
		}
	}
	return out
}

// lineLog passes nginx's output to the log and keeps the last lines for error messages.
type lineLog struct {
	log  *slog.Logger
	mu   sync.Mutex
	buf  []string
	part []byte
}

func (l *lineLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range p {
		if c != '\n' {
			l.part = append(l.part, c)
			continue
		}
		line := string(l.part)
		l.part = l.part[:0]
		l.log.Info(line, "src", "nginx")
		l.buf = append(l.buf, line)
		if len(l.buf) > 50 {
			l.buf = l.buf[len(l.buf)-50:]
		}
	}
	return len(p), nil
}

func (l *lineLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.buf, "\n")
}

func fileSize(p string) int64 {
	fi, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return fi.Size()
}

func readFrom(p string, off int64) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	if _, err := f.Seek(off, 0); err != nil {
		return ""
	}
	var b strings.Builder
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		b.WriteString(sc.Text())
		b.WriteByte('\n')
	}
	return b.String()
}

func lastLines(s string, n int) string {
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

// Check runs `nginx -t` on a config built by nginxconf, with placeholder certificates in a
// temporary directory (the panel checks profile templates this way).
func Check(ctx context.Context, bin, conf string) error {
	if conf == "" {
		return nil
	}
	dir, err := os.MkdirTemp("", "vynel-nginx-check-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	m := &Manager{Bin: bin, DataDir: filepath.Join(dir, "data"), WWWDir: filepath.Join(dir, "www"), User: "nobody nogroup", Log: slog.New(slog.DiscardHandler)}
	if err := m.Init(); err != nil {
		return err
	}
	if _, err := m.Certs.Ensure(nginxconf.CertDomains(conf)); err != nil {
		return err
	}
	final, err := m.finalize(ctx, conf)
	if err != nil {
		return err
	}
	path := filepath.Join(m.DataDir, "nginx", "nginx.conf")
	if err := os.WriteFile(path, []byte(checkListens(final, dir)), 0o600); err != nil {
		return err
	}
	return m.test(ctx, path)
}

var checkListenRe = regexp.MustCompile(`(?m)^(\s*listen )(\S+?)([ ;].*)$`)

// checkListens moves every listener to a unix socket in dir: nginx -t binds them, and the
// ports may be privileged (an unprivileged checker) or the addresses another host's (a node's
// IP checked on the panel). The same address gets the same socket, so nginx still sees
// duplicate and conflicting listen options.
func checkListens(conf, dir string) string {
	socks := map[string]string{}
	return checkListenRe.ReplaceAllStringFunc(conf, func(line string) string {
		m := checkListenRe.FindStringSubmatch(line)
		sock, ok := socks[m[2]]
		if !ok {
			sock = filepath.Join(dir, fmt.Sprintf("l%d.sock", len(socks)))
			socks[m[2]] = sock
		}
		return m[1] + "unix:" + sock + strings.Replace(m[3], " reuseport", "", 1)
	})
}
