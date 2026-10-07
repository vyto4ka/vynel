// Package xraytest has helpers for tests that run a real Xray (set XRAY_BIN, see `make xray`).
package xraytest

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/proxy"

	"github.com/vyto4ka/vynel/internal/xray"
)

// Binary returns the Xray binary from XRAY_BIN or skips the test.
func Binary(t testing.TB) xray.Binary {
	t.Helper()
	path := os.Getenv("XRAY_BIN")
	if path == "" {
		t.Skip("XRAY_BIN not set; run `make test-integration`")
	}
	assets := os.Getenv("XRAY_LOCATION_ASSET")
	if assets == "" {
		assets = filepath.Dir(path)
	}
	return xray.Binary{Path: path, AssetDir: assets}
}

// FreePort returns a free TCP port on 127.0.0.1.
func FreePort(t testing.TB) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// TLSTarget starts a TLS 1.3 site for name: the self-steal Caddy stand-in behind Reality.
func TLSTarget(t testing.TB, name string) int {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{
		ErrorLog: log.New(io.Discard, "", 0), // Reality probes the target; handshake noise is expected
		Handler:  http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "decoy") }),
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
			NextProtos:   []string{"h2", "http/1.1"},
		},
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.ServeTLS(l, "", "") }()
	t.Cleanup(func() { _ = srv.Close() })
	return l.Addr().(*net.TCPAddr).Port
}

// OriginSize is the body size served by Origin.
const OriginSize = 256 * 1024

// Origin starts the "internet" a client reaches through the proxy and returns its URL.
func Origin(t testing.TB) string {
	t.Helper()
	body := strings.Repeat("x", OriginSize)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) })}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return "http://" + l.Addr().String() + "/"
}

// RealityClient describes a VLESS Reality client.
type RealityClient struct {
	ServerPort int
	UUID       string
	Flow       string
	ServerName string
	PublicKey  string
	ShortID    string
}

// StartRealityClient runs an Xray client exposing SOCKS on the returned port.
func StartRealityClient(t testing.TB, bin xray.Binary, c RealityClient) int {
	t.Helper()
	socks := FreePort(t)
	cfg := map[string]any{
		"log":      map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": socks, "protocol": "socks", "settings": map[string]any{"udp": false}}},
		"outbounds": []any{map[string]any{
			"protocol": "vless",
			"settings": map[string]any{"vnext": []any{map[string]any{
				"address": "127.0.0.1", "port": c.ServerPort,
				"users": []any{map[string]any{"id": c.UUID, "encryption": "none", "flow": c.Flow}},
			}}},
			"streamSettings": map[string]any{
				"network": "raw", "security": "reality",
				"realitySettings": map[string]any{"serverName": c.ServerName, "fingerprint": "firefox", "publicKey": c.PublicKey, "shortId": c.ShortID},
			},
		}},
	}
	raw, _ := json.Marshal(cfg)
	return StartClient(t, bin, raw, socks)
}

// StartClient runs an Xray client config and waits until its SOCKS inbound on socksPort listens.
func StartClient(t testing.TB, bin xray.Binary, config []byte, socksPort int) int {
	t.Helper()
	p := xray.NewProcess(bin, filepath.Join(t.TempDir(), "client.json"), nil)
	if err := p.Apply(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(socksPort)); err == nil {
			conn.Close()
			return socksPort
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("client socks not listening: %v", p.Tail())
	return 0
}

// Fetch GETs url through a SOCKS5 proxy on 127.0.0.1:socksPort with a fresh connection.
func Fetch(ctx context.Context, socksPort int, url string) ([]byte, error) {
	d, err := proxy.SOCKS5("tcp", "127.0.0.1:"+strconv.Itoa(socksPort), nil, proxy.Direct)
	if err != nil {
		return nil, err
	}
	hc := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{DialContext: d.(proxy.ContextDialer).DialContext, DisableKeepAlives: true},
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// Eventually polls cond until it returns nil or the timeout passes.
func Eventually(t testing.TB, timeout time.Duration, what string, cond func() error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var err error
	for time.Now().Before(deadline) {
		if err = cond(); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s: %v", what, err)
}
