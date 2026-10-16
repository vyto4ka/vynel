package nginxconf

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// nginxBin returns the nginx binary for tests: NGINX_BIN or the system one; skips without.
func nginxBin(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("NGINX_BIN")
	if bin == "" {
		bin = "/usr/sbin/nginx"
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skip("nginx not found (set NGINX_BIN)")
	}
	return bin
}

// streamModules finds the dynamic stream module the way the agent does.
func streamModules(t *testing.T, bin string) []string {
	out, err := exec.Command(bin, "-V").CombinedOutput()
	if err != nil {
		t.Fatalf("nginx -V: %v", err)
	}
	if !strings.Contains(string(out), "--with-stream=dynamic") {
		return nil
	}
	dir := "/usr/lib/nginx/modules"
	if m := regexp.MustCompile(`--modules-path=(\S+)`).FindSubmatch(out); m != nil {
		dir = string(m[1])
	}
	return []string{filepath.Join(dir, "ngx_stream_module.so")}
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func selfSigned(t *testing.T, dir, domain string) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: domain}, DNSNames: []string{domain},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	d := filepath.Join(dir, domain)
	_ = os.MkdirAll(d, 0o755)
	_ = os.WriteFile(filepath.Join(d, "fullchain.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	_ = os.WriteFile(filepath.Join(d, "privkey.pem"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600)
}

func write(t *testing.T, path, body string) {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestNginxServes runs the generated config in a real nginx: SNI routing with PROXY headers,
// decoys with clean URLs and their 404 page, unknown and missing SNI, a CDN name without its
// own certificate, an XHTTP stream location that passes a GET body, ACME and the redirect on
// :80, and strict SNI on a listener without a default site — with ssl_reject_handshake and with
// the dummy certificate old nginx gets.
func TestNginxServes(t *testing.T) {
	for _, reject := range []bool{true, false} {
		t.Run(map[bool]string{true: "reject_handshake", false: "dummy_cert"}[reject], func(t *testing.T) { testNginxServes(t, reject) })
	}
}

func testNginxServes(t *testing.T, reject bool) {
	bin := nginxBin(t)
	root := t.TempDir()
	_ = os.Chmod(root, 0o755)
	streamPort, httpsPort, httpPort, strictPort := freePort(t), freePort(t), freePort(t), freePort(t)

	// Reality stand-in: reads the PROXY header the router sends, then answers.
	xray, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer xray.Close()
	proxyLine := make(chan string, 1)
	go func() {
		for {
			c, err := xray.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(c).ReadString('\n')
			proxyLine <- line
			c.Close()
		}
	}()
	// XHTTP backend: echoes method, body and the client address nginx forwards.
	backend := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "%s %s body=%s xff=%s host=%s", r.Method, r.URL.Path, body, r.Header.Get("X-Forwarded-For"), r.Host)
	})}
	bl, _ := net.Listen("tcp", "127.0.0.1:0")
	go func() { _ = backend.Serve(bl) }()
	defer func() { _ = backend.Close() }()

	conf, err := Build(Spec{
		HTTPPort: httpPort,
		Streams: []Stream{{Bind: "127.0.0.1", Port: streamPort, Default: "127.0.0.1:" + strconv.Itoa(httpsPort),
			Backends: []Backend{{Names: []string{"reality.test"}, Addr: xray.Addr().String()}}}},
		Sites: []Site{
			{Domain: "a.test", LocalPort: httpsPort, ProxyProtocol: true, Routes: []Route{{Kind: KindDecoy, Decoy: "one"}}},
			{Domain: "strict.test", LocalPort: strictPort, Routes: []Route{{Kind: KindDecoy, Decoy: "one"}}},
			{Domain: "origin.test", Names: []string{"cdn.test"}, LocalPort: httpsPort, ProxyProtocol: true, Default: true, Routes: []Route{
				{Kind: KindStream, PathPrefix: "/upload", Upstream: bl.Addr().String()},
				{Kind: KindDecoy, Decoy: "two"},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	certs, decoys, acme, run := filepath.Join(root, "certs"), filepath.Join(root, "decoy"), filepath.Join(root, "acme"), filepath.Join(root, "run")
	_ = os.MkdirAll(run, 0o755)
	for _, d := range CertDomains(conf) {
		selfSigned(t, certs, d)
	}
	write(t, filepath.Join(decoys, "one/index.html"), "one home")
	write(t, filepath.Join(decoys, "one/about.html"), "one about")
	write(t, filepath.Join(decoys, "one/docs/index.html"), "one docs")
	write(t, filepath.Join(decoys, "one/404.html"), "one not found")
	write(t, filepath.Join(decoys, "two/index.html"), "two home")
	write(t, filepath.Join(acme, ".well-known/acme-challenge/tok"), "tok.thumb")
	user := ""
	if os.Getuid() == 0 {
		user = "root"
	}
	selfSigned(t, certs, "dummy.invalid")
	dummy := filepath.Join(certs, "dummy.invalid")
	final := Finalize(conf, Local{Modules: streamModules(t, bin), User: user, Run: run, Certs: certs, Decoy: decoys, ACME: acme,
		RejectHandshake: reject, DummyCert: filepath.Join(dummy, "fullchain.pem"), DummyKey: filepath.Join(dummy, "privkey.pem")})
	cfgPath := filepath.Join(root, "nginx.conf")
	write(t, cfgPath, final)
	if out, err := exec.Command(bin, "-t", "-q", "-p", run, "-c", cfgPath).CombinedOutput(); err != nil {
		t.Fatalf("nginx -t: %v\n%s\n%s", err, out, final)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-p", run, "-c", cfgPath, "-g", "daemon off;")
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Signal(os.Interrupt); _ = cmd.Wait() }()
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(httpPort)); err == nil {
			c.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	get := func(sni, host, method, path, body string) (int, string, http.Header) {
		t.Helper()
		tr := &http.Transport{
			TLSClientConfig: &tls.Config{ServerName: sni, InsecureSkipVerify: true}, //nolint:gosec // self-signed test certs
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp", "127.0.0.1:"+strconv.Itoa(streamPort))
			},
		}
		if sni == "" {
			// No SNI at all: dial TLS ourselves with an IP as the name.
			tr.DialTLSContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
				c, err := (&net.Dialer{}).DialContext(ctx, "tcp", "127.0.0.1:"+strconv.Itoa(streamPort))
				if err != nil {
					return nil, err
				}
				tc := tls.Client(c, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // test
				return tc, tc.HandshakeContext(ctx)
			}
		}
		req, _ := http.NewRequest(method, "https://"+host+path, strings.NewReader(body))
		resp, err := (&http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
		if err != nil {
			t.Fatalf("%s %s%s (sni %q): %v", method, host, path, sni, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), resp.Header
	}
	check := func(name string, code int, body string, wantCode int, wantBody string) {
		t.Helper()
		if code != wantCode || !strings.Contains(body, wantBody) {
			t.Fatalf("%s: %d %q, want %d %q", name, code, body, wantCode, wantBody)
		}
	}

	c, b, h := get("a.test", "a.test", "GET", "/", "")
	check("decoy home", c, b, 200, "one home")
	if h.Get("Strict-Transport-Security") == "" || h.Get("Server") != "nginx" {
		t.Fatalf("headers %v", h)
	}
	c, b, _ = get("a.test", "a.test", "GET", "/about", "")
	check("clean URL", c, b, 200, "one about")
	c, b, _ = get("a.test", "a.test", "GET", "/docs", "")
	check("directory page", c, b, 200, "one docs")
	c, b, _ = get("a.test", "a.test", "GET", "/nope", "")
	check("decoy 404", c, b, 404, "one not found")
	// The CDN origin is this listener's default: it answers unknown names and no SNI.
	c, b, _ = get("unknown.test", "unknown.test", "GET", "/", "")
	check("unknown SNI", c, b, 200, "two home")
	c, b, _ = get("", "unknown.test", "GET", "/", "")
	check("no SNI", c, b, 200, "two home")
	c, b, _ = get("", "a.test", "GET", "/", "")
	check("no SNI, known Host", c, b, 200, "one home")
	// A listener without a default site: strict SNI, no certificate for strangers.
	strict := func(sni string) error {
		tc, err := tls.Dial("tcp", "127.0.0.1:"+strconv.Itoa(strictPort), &tls.Config{ServerName: sni, InsecureSkipVerify: true}) //nolint:gosec // test
		if err != nil {
			return err
		}
		defer func() { _ = tc.Close() }()
		if names := tc.ConnectionState().PeerCertificates[0].DNSNames; len(names) > 0 && names[0] == "strict.test" && sni != "strict.test" {
			return fmt.Errorf("a stranger got the site's certificate")
		}
		if sni == "strict.test" {
			return nil
		}
		// Old nginx: a dummy certificate, then the request is closed without an answer.
		_, _ = tc.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n"))
		buf := make([]byte, 64)
		if n, _ := tc.Read(buf); n > 0 {
			return fmt.Errorf("a stranger got an answer: %q", buf[:n])
		}
		return fmt.Errorf("closed")
	}
	if err := strict("strict.test"); err != nil {
		t.Fatalf("own name on the strict listener: %v", err)
	}
	for _, sni := range []string{"", "other.test"} {
		if err := strict(sni); err == nil || strings.Contains(err.Error(), "stranger") {
			t.Fatalf("strict SNI %q: %v", sni, err)
		}
	}
	// The CDN case: VK forwards the client's Host (and SNI) with the CDN domain.
	c, b, _ = get("cdn.test", "cdn.test", "GET", "/upload/abc?x=1", "payload")
	check("CDN name, GET with body", c, b, 200, "GET /upload/abc body=payload xff=127.0.0.1 host=cdn.test")
	c, b, _ = get("origin.test", "origin.test", "POST", "/upload/x", "up")
	check("origin POST", c, b, 200, "POST /upload/x body=up")

	// Reality SNI goes to Xray with a PROXY header.
	tc, err := tls.Dial("tcp", "127.0.0.1:"+strconv.Itoa(streamPort), &tls.Config{ServerName: "reality.test", InsecureSkipVerify: true}) //nolint:gosec // test
	if err == nil {
		_ = tc.Close()
	}
	select {
	case l := <-proxyLine:
		if !strings.HasPrefix(l, "PROXY TCP4 127.0.0.1 ") {
			t.Fatalf("PROXY header %q", l)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the router did not reach the Reality backend")
	}

	// :80
	resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(httpPort) + "/.well-known/acme-challenge/tok")
	if err != nil {
		t.Fatal(err)
	}
	b2, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b2) != "tok.thumb" {
		t.Fatalf("acme %d %q", resp.StatusCode, b2)
	}
	req, _ := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(httpPort)+"/x?y=1", nil)
	req.Host = "a.test"
	resp, err = http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 301 || resp.Header.Get("Location") != "https://a.test/x?y=1" {
		t.Fatalf("redirect %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}
