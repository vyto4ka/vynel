package certs

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vyto4ka/vynel/internal/xray/xraytest"
)

func TestPlaceholderInternalImport(t *testing.T) {
	m := &Manager{Dir: t.TempDir(), Webroot: t.TempDir()}
	if changed, err := m.Ensure([]string{"a.example.com"}); err != nil || !changed {
		t.Fatalf("ensure: %v %v", changed, err)
	}
	if changed, _ := m.Ensure([]string{"a.example.com"}); changed {
		t.Fatal("placeholder written twice")
	}
	if st, err := m.Status("a.example.com"); err != nil || !st.Placeholder {
		t.Fatalf("status %+v %v", st, err)
	}
	if !m.Due("a.example.com", time.Now()) {
		t.Fatal("a placeholder is due")
	}
	if _, err := m.Ensure([]string{"../etc"}); err == nil {
		t.Fatal("bad domain accepted")
	}

	done, err := m.Renew(context.Background(), []string{"a.example.com"}, "", "internal")
	if err != nil || len(done) != 1 {
		t.Fatalf("internal: %v %v", done, err)
	}
	if m.Due("a.example.com", time.Now()) {
		t.Fatal("fresh certificate is due")
	}
	caPEM, _ := m.InternalCA()
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	certFile, keyFile := m.Paths("a.example.com")
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(pair.Certificate[0])
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "a.example.com"}); err != nil {
		t.Fatalf("verify: %v", err)
	}

	// Import: a valid certificate replaces a placeholder only.
	m2 := &Manager{Dir: t.TempDir()}
	_, _ = m2.Ensure([]string{"a.example.com"})
	certPEM, _ := os.ReadFile(certFile)
	keyPEM, _ := os.ReadFile(keyFile)
	if ok, err := m2.Import("a.example.com", certPEM, keyPEM); err != nil || !ok {
		t.Fatalf("import over a placeholder: %v %v", ok, err)
	}
	if st, _ := m2.Status("a.example.com"); st.Placeholder {
		t.Fatal("still a placeholder after import")
	}
	if ok, _ := m2.Import("a.example.com", certPEM, keyPEM); ok {
		t.Fatal("a real certificate was replaced by an import")
	}
	if ok, _ := m2.Import("b.example.com", certPEM, keyPEM); ok {
		t.Fatal("imported a certificate for another name")
	}
}

// dnsStub answers every A query with 127.0.0.1 and everything else with an empty answer.
func dnsStub(t *testing.T) string {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			q := buf[:n]
			if n < 12 {
				continue
			}
			// question ends after the name (labels) + type + class
			i := 12
			for i < n && q[i] != 0 {
				i += int(q[i]) + 1
			}
			end := i + 5
			if end > n {
				continue
			}
			qtype := binary.BigEndian.Uint16(q[i+1:])
			resp := append([]byte{}, q[:end]...)
			resp[2] = 0x84 | (q[2] & 0x01) // response, authoritative, keep RD
			resp[3] = 0
			binary.BigEndian.PutUint16(resp[6:], 0) // answers
			binary.BigEndian.PutUint16(resp[8:], 0)
			binary.BigEndian.PutUint16(resp[10:], 0)
			if qtype == 1 {
				binary.BigEndian.PutUint16(resp[6:], 1)
				resp = append(resp, 0xc0, 12, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 127, 0, 0, 1)
			}
			_, _ = pc.WriteTo(resp, addr)
		}
	}()
	return pc.LocalAddr().String()
}

func freePort(t *testing.T) int { return xraytest.FreePort(t) }

// TestACMEWithPebble issues a certificate from Pebble (Let's Encrypt's test server), which
// really fetches the HTTP-01 token from the webroot over HTTP.
func TestACMEWithPebble(t *testing.T) {
	bin := os.Getenv("PEBBLE_BIN")
	if bin == "" {
		t.Skip("PEBBLE_BIN not set")
	}
	dir := t.TempDir()
	// Pebble's own TLS certificate.
	tlsMgr := &Manager{Dir: filepath.Join(dir, "pebble-tls")}
	if _, err := tlsMgr.Renew(context.Background(), []string{"pebble.localhost.test"}, "", "internal"); err != nil {
		t.Fatal(err)
	}
	tlsCert, tlsKey := tlsMgr.Paths("pebble.localhost.test")
	// Pebble connects to the name, not to 127.0.0.1: give it an IP SAN via a separate cert.
	caPEM, _ := tlsMgr.InternalCA()

	acmePort, mgmtPort, httpPort := freePort(t), freePort(t), freePort(t)
	cfg := map[string]any{"pebble": map[string]any{
		"listenAddress": "127.0.0.1:" + strconv.Itoa(acmePort), "managementListenAddress": "127.0.0.1:" + strconv.Itoa(mgmtPort),
		"certificate": tlsCert, "privateKey": tlsKey, "httpPort": httpPort, "tlsPort": freePort(t),
		"ocspResponderURL": "", "externalAccountBindingRequired": false,
	}}
	raw, _ := json.Marshal(cfg)
	cfgFile := filepath.Join(dir, "pebble.json")
	_ = os.WriteFile(cfgFile, raw, 0o644)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-config", cfgFile, "-dnsserver", dnsStub(t))
	cmd.Env = append(os.Environ(), "PEBBLE_VA_NOSLEEP=1", "PEBBLE_WFE_NONCEREJECT=0")
	out := &strings.Builder{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()

	m := &Manager{Dir: filepath.Join(dir, "store"), Webroot: filepath.Join(dir, "webroot")}
	// "nginx" on :httpPort serving the webroot.
	srv := &http.Server{Handler: http.FileServer(http.Dir(m.Webroot)), ReadHeaderTimeout: 5 * time.Second}
	hl, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(httpPort))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(hl) }()
	defer func() { _ = srv.Close() }()

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	m.HTTPClient = &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "pebble.localhost.test"},
	}}
	m.DirectoryURL = "https://127.0.0.1:" + strconv.Itoa(acmePort) + "/dir"
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(acmePort)); err == nil {
			c.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, err := m.Ensure([]string{"node.example.com"}); err != nil {
		t.Fatal(err)
	}
	done, err := m.Renew(context.Background(), []string{"node.example.com"}, "admin@example.com", "acme")
	if err != nil || len(done) != 1 {
		t.Fatalf("renew: %v %v\npebble:\n%s", done, err, out)
	}
	st, err := m.Status("node.example.com")
	if err != nil || st.Placeholder || !strings.Contains(strings.ToLower(st.Issuer), "pebble") {
		t.Fatalf("status %+v %v", st, err)
	}
	certFile, _ := m.Paths("node.example.com")
	b, _ := os.ReadFile(certFile)
	if strings.Count(string(b), "BEGIN CERTIFICATE") < 2 {
		t.Fatal("expected the full chain")
	}
	if blk, _ := pem.Decode(b); blk == nil {
		t.Fatal("bad PEM")
	}
	// The account is reused.
	if done, err := m.Renew(context.Background(), []string{"node.example.com"}, "admin@example.com", "acme"); err != nil || len(done) != 0 {
		t.Fatalf("fresh certificate renewed again: %v %v", done, err)
	}
}
