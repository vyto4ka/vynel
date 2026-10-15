package agent

import (
	"context"
	"crypto/tls"
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

	"github.com/vyto4ka/vynel/internal/caddyconf"
	"github.com/vyto4ka/vynel/internal/nginxconf"
	"github.com/vyto4ka/vynel/internal/xray/xraytest"
)

// TestWebServerSwitch moves a node from Caddy (an old panel's snapshot) to nginx and back,
// and checks that a failed start of nginx brings Caddy back: Reality needs its target.
func TestWebServerSwitch(t *testing.T) {
	caddyBin := os.Getenv("CADDY_BIN")
	nginxBin := os.Getenv("NGINX_BIN")
	if nginxBin == "" {
		nginxBin = "/usr/sbin/nginx"
	}
	if _, err := os.Stat(nginxBin); err != nil || caddyBin == "" {
		t.Skip("needs CADDY_BIN and nginx")
	}
	dir := t.TempDir()
	_ = os.Chmod(dir, 0o755)
	user := ""
	if os.Getuid() == 0 {
		user = "root"
	}
	a, err := New(Config{DataDir: dir, CaddyBin: caddyBin, NginxBin: nginxBin, NginxUser: user,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	httpsPort, httpPort, adminPort := xraytest.FreePort(t), xraytest.FreePort(t), xraytest.FreePort(t)
	caddyCfg, err := caddyconf.Build(caddyconf.Spec{AdminAddr: "127.0.0.1:" + strconv.Itoa(adminPort), Issuer: "internal",
		HTTPPort: httpPort, HTTPBinds: []string{"127.0.0.1"},
		Sites: []caddyconf.Site{{Domain: "node.example.com", Bind: "127.0.0.1", Port: httpsPort,
			Routes: []caddyconf.Route{{Kind: caddyconf.KindDecoy, Decoy: "cloud"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	nginxCfg := func(httpPort int) string {
		c, err := nginxconf.Build(nginxconf.Spec{Issuer: "internal", HTTPPort: httpPort, HTTPBinds: []string{"127.0.0.1"},
			Sites: []nginxconf.Site{{Domain: "node.example.com", Bind: "127.0.0.1", Port: httpsPort,
				Routes: []nginxconf.Route{{Kind: nginxconf.KindDecoy, Decoy: "cloud"}}}}})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	server := func() string {
		tr := &http.Transport{TLSClientConfig: &tls.Config{ServerName: "node.example.com", InsecureSkipVerify: true}} //nolint:gosec // test
		var last error
		for i := 0; i < 30; i++ {
			resp, err := (&http.Client{Transport: tr, Timeout: 3 * time.Second}).Get("https://127.0.0.1:" + strconv.Itoa(httpsPort) + "/")
			if err == nil {
				resp.Body.Close()
				if s := strings.ToLower(resp.Header.Get("Server")); s != "" {
					return s
				}
				return "caddy" // Caddy's config hides its Server header
			}
			last = err
			time.Sleep(100 * time.Millisecond)
		}
		return "down: " + last.Error()
	}
	ctx := context.Background()
	apply := func(c []byte, n string) error {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.applyWebLocked(ctx, c, n)
	}

	if err := apply(caddyCfg, ""); err != nil {
		t.Fatal(err)
	}
	if s := server(); s != "caddy" {
		t.Fatalf("caddy expected, got %s", s)
	}
	// nginx cannot start (its :80 is taken): Caddy comes back.
	busy, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(xraytest.FreePort(t)))
	if err != nil {
		t.Fatal(err)
	}
	busyPort := busy.Addr().(*net.TCPAddr).Port
	if err := apply(nil, nginxCfg(busyPort)); err == nil || !strings.HasPrefix(err.Error(), "nginx:") {
		t.Fatalf("nginx on a busy port: %v", err)
	}
	busy.Close()
	if s := server(); s != "caddy" || a.caddyCfg == nil || a.nginxCfg != "" {
		t.Fatalf("caddy not restored: %s", s)
	}
	// The real switch.
	if err := apply(nil, nginxCfg(httpPort)); err != nil {
		t.Fatal(err)
	}
	if s := server(); s != "nginx" || a.caddyCfg != nil {
		t.Fatalf("nginx expected, got %s", s)
	}
	if ports := a.FirewallPorts(); len(ports) != 0 {
		t.Fatalf("loopback listeners are not public: %v", ports)
	}
	if _, err := os.Stat(filepath.Join(dir, "web", "tls", "node.example.com", "fullchain.pem")); err != nil {
		t.Fatalf("certificate store: %v", err)
	}
	// An old panel again (rollback of the panel): back to Caddy.
	if err := apply(caddyCfg, ""); err != nil {
		t.Fatal(err)
	}
	if s := server(); s != "caddy" || a.nginxCfg != "" {
		t.Fatalf("caddy expected after the old panel's snapshot, got %s", s)
	}
}
