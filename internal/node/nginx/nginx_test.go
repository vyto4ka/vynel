package nginx

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vyto4ka/vynel/internal/certs"
	"github.com/vyto4ka/vynel/internal/nginxconf"
)

func nginxBin(t *testing.T) string {
	bin := os.Getenv("NGINX_BIN")
	if bin == "" {
		bin = DefaultBin
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skip("nginx not found (set NGINX_BIN)")
	}
	return bin
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestManager(t *testing.T) {
	bin := nginxBin(t)
	dir := t.TempDir()
	_ = os.Chmod(dir, 0o755)
	user := ""
	if os.Getuid() == 0 {
		user = "root" // temp dirs are not readable by www-data
	}
	renewed := 0
	m := &Manager{Bin: bin, DataDir: filepath.Join(dir, "data"), WWWDir: filepath.Join(dir, "www"), User: user,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), OnCerts: func() { renewed++ }}
	defer m.Close()
	if v := m.Version(context.Background()); v == "" {
		t.Fatal("no version")
	}
	httpsPort, httpPort := freePort(t), freePort(t)
	build := func(decoy string, extra ...nginxconf.Route) string {
		c, err := nginxconf.Build(nginxconf.Spec{Issuer: "internal", HTTPPort: httpPort, HTTPBinds: []string{"127.0.0.1"},
			Sites: []nginxconf.Site{{Domain: "node.example.com", Bind: "127.0.0.1", Port: httpsPort,
				Routes: append(extra, nginxconf.Route{Kind: nginxconf.KindDecoy, Decoy: decoy})}}})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	get := func(pool *x509.CertPool) (string, error) {
		tr := &http.Transport{TLSClientConfig: &tls.Config{ServerName: "node.example.com", RootCAs: pool, InsecureSkipVerify: pool == nil}} //nolint:gosec // first request sees the placeholder
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", "127.0.0.1:"+strconv.Itoa(httpsPort))
		}
		resp, err := (&http.Client{Transport: tr, Timeout: 5 * time.Second}).Get("https://node.example.com/")
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b), nil
	}

	ctx := context.Background()
	if err := m.Apply(ctx, build("cloud")); err != nil {
		t.Fatal(err)
	}
	if body, err := get(nil); err != nil || !strings.Contains(body, "<html") {
		t.Fatalf("decoy over the placeholder: %v %.80q", err, body)
	}
	// The master's pid file and error log are in the root-only state dir, not in the run dir
	// the workers own.
	for _, f := range []string{"nginx.pid", "error.log"} {
		if _, err := os.Stat(filepath.Join(m.stateDir(), f)); err != nil {
			t.Fatalf("%s not in the state dir: %v", f, err)
		}
		if _, err := os.Stat(filepath.Join(m.runDir(), f)); err == nil {
			t.Fatalf("%s in the workers' run dir", f)
		}
	}
	if st, _ := os.Stat(m.stateDir()); st == nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("state dir mode: %v", st)
	}
	// Renewal replaces the placeholder with a certificate of the internal CA and reloads.
	if err := m.RenewCerts(ctx); err != nil || renewed != 1 {
		t.Fatalf("renew: %v (callbacks %d)", err, renewed)
	}
	caPEM, _ := m.Certs.InternalCA()
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	var err error
	for i := 0; i < 20; i++ {
		if _, err = get(pool); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("real certificate not served after reload: %v", err)
	}

	// A config nginx rejects leaves the old one running.
	bad := build("cloud", nginxconf.Route{Kind: nginxconf.KindRaw, PathPrefix: "/x", Raw: "no_such_directive on;"})
	if err := m.Apply(ctx, bad); err == nil || !strings.Contains(err.Error(), "no_such_directive") {
		t.Fatalf("bad config: %v", err)
	}
	if _, err := get(pool); err != nil {
		t.Fatalf("old config stopped serving: %v", err)
	}
	// Reload with another decoy.
	if err := m.Apply(ctx, build("studio")); err != nil {
		t.Fatal(err)
	}
	if !m.Running() {
		t.Fatal("not running")
	}
	if err := m.Apply(ctx, ""); err != nil || m.Running() {
		t.Fatalf("stop: %v", err)
	}
}

func TestImportCaddy(t *testing.T) {
	dir := t.TempDir()
	src := &certs.Manager{Dir: filepath.Join(dir, "src")}
	if _, err := src.Renew(context.Background(), []string{"a.example.com"}, "", "internal"); err != nil {
		t.Fatal(err)
	}
	crt, key := src.Paths("a.example.com")
	storage := filepath.Join(dir, "caddy")
	d := filepath.Join(storage, "certificates", "acme-v02.api.letsencrypt.org-directory", "a.example.com")
	_ = os.MkdirAll(d, 0o700)
	b, _ := os.ReadFile(crt)
	_ = os.WriteFile(filepath.Join(d, "a.example.com.crt"), b, 0o600)
	b, _ = os.ReadFile(key)
	_ = os.WriteFile(filepath.Join(d, "a.example.com.key"), b, 0o600)

	m := &Manager{DataDir: filepath.Join(dir, "data")}
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	_, _ = m.Certs.Ensure([]string{"a.example.com", "b.example.com"})
	got := m.ImportCaddy(storage, []string{"a.example.com", "b.example.com"})
	if len(got) != 1 || got[0] != "a.example.com" {
		t.Fatalf("imported %v", got)
	}
	if st, _ := m.Certs.Status("a.example.com"); st.Placeholder {
		t.Fatal("still a placeholder")
	}
}

func TestVersionAtLeast(t *testing.T) {
	for out, want := range map[string]bool{
		"nginx version: nginx/1.18.0 (Ubuntu)": false, "nginx version: nginx/1.19.4": true, "nginx version: nginx/1.19.3": false,
		"nginx version: nginx/1.24.0 (Ubuntu)": true, "nginx version: nginx/1.22.1": true, "nginx version: nginx/2.0.0": true, "garbage": false,
	} {
		if got := versionAtLeast(out, 1, 19, 4); got != want {
			t.Errorf("%q: %v", out, got)
		}
	}
}

// Check needs no free or privileged ports and no local addresses: the panel checks configs
// for other hosts, often not as root.
func TestCheckWithoutPorts(t *testing.T) {
	bin := nginxBin(t)
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	conf, err := nginxconf.Build(nginxconf.Spec{Issuer: "internal", HTTPPort: 80,
		Streams: []nginxconf.Stream{{Bind: "203.0.113.7", Port: 443, Default: "127.0.0.1:8443",
			Backends: []nginxconf.Backend{{Names: []string{"r.example.com"}, Addr: "127.0.0.1:10443"}}}},
		Sites: []nginxconf.Site{
			{Domain: "a.example.com", Bind: "127.0.0.1", Port: 8443, ProxyProtocol: true, Routes: []nginxconf.Route{{Kind: nginxconf.KindDecoy, Decoy: "cloud"}}},
			{Domain: "b.example.com", Bind: "127.0.0.1", Port: busy.Addr().(*net.TCPAddr).Port,
				Routes: []nginxconf.Route{{Kind: nginxconf.KindDecoy, Decoy: "docs"}}},
		}})
	if err != nil {
		t.Fatal(err)
	}
	if err := Check(context.Background(), bin, conf); err != nil {
		t.Fatal(err)
	}
	bad := strings.Replace(conf, "ssl_protocols", "ssl_protocolz", 1)
	if err := Check(context.Background(), bin, bad); err == nil || !strings.Contains(err.Error(), "ssl_protocolz") {
		t.Fatalf("broken config passed: %v", err)
	}
}

func TestCheckListens(t *testing.T) {
	in := "  listen 443 ssl http2 default_server;\n  # listen [::]:443;\n  listen 443 ssl http2;\n  listen 10.0.0.1:443 reuseport;\n"
	want := "  listen unix:/d/l0.sock ssl http2 default_server;\n  # listen [::]:443;\n  listen unix:/d/l0.sock ssl http2;\n  listen unix:/d/l1.sock;\n"
	if got := checkListens(in, "/d"); got != want {
		t.Fatalf("got\n%s", got)
	}
}

// A missing stream module is named in the error, and found once installed, without a restart.
func TestStreamModuleMissing(t *testing.T) {
	// Ubuntu 22.04's nginx -V names no stream flag at all, Debian 12's says --with-stream=dynamic.
	for name, flags := range map[string]string{"jammy": "--with-threads", "bookworm": "--with-stream=dynamic --with-threads"} {
		t.Run(name, func(t *testing.T) { testStreamModuleMissing(t, flags) })
	}
}

func testStreamModuleMissing(t *testing.T, flags string) {
	dir := t.TempDir()
	mods := filepath.Join(dir, "modules")
	bin := filepath.Join(dir, "nginx")
	script := "#!/bin/sh\necho 'nginx version: nginx/1.24.0' >&2\n" +
		"[ \"$1\" = -V ] && echo 'configure arguments: --modules-path=" + mods + " " + flags + "' >&2\nexit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	m := &Manager{Bin: bin, DataDir: filepath.Join(dir, "data"), WWWDir: filepath.Join(dir, "www"), User: "nobody",
		Log: slog.New(slog.DiscardHandler)}
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	conf, err := nginxconf.Build(nginxconf.Spec{Issuer: "internal",
		Streams: []nginxconf.Stream{{Port: 443, Default: "127.0.0.1:8443",
			Backends: []nginxconf.Backend{{Names: []string{"r.example.com"}, Addr: "127.0.0.1:10443"}}}},
		Sites: []nginxconf.Site{{Domain: "a.example.com", Bind: "127.0.0.1", Port: 8443, ProxyProtocol: true,
			Routes: []nginxconf.Route{{Kind: nginxconf.KindDecoy, Decoy: "cloud"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := m.finalize(ctx, conf); err == nil || !strings.Contains(err.Error(), "libnginx-mod-stream") {
		t.Fatalf("missing module: %v", err)
	}
	_ = os.MkdirAll(mods, 0o755)
	if err := os.WriteFile(filepath.Join(mods, "ngx_stream_module.so"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	final, err := m.finalize(ctx, conf)
	if err != nil || !strings.Contains(final, "load_module "+filepath.Join(mods, "ngx_stream_module.so")) {
		t.Fatalf("installed module not picked up: %v\n%.300s", err, final)
	}
}
