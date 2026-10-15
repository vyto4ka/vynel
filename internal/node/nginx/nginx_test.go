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
