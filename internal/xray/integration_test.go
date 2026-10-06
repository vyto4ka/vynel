package xray

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

	"github.com/vyto4ka/vpn/internal/xrayconf"
)

// testBinary returns the Xray binary from XRAY_BIN (see `make xray`) or skips the test.
func testBinary(t *testing.T) Binary {
	t.Helper()
	path := os.Getenv("XRAY_BIN")
	if path == "" {
		t.Skip("XRAY_BIN not set; run `make test-integration`")
	}
	assets := os.Getenv("XRAY_LOCATION_ASSET")
	if assets == "" {
		assets = filepath.Dir(path)
	}
	return Binary{Path: path, AssetDir: assets}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// startTLSTarget plays the role of the self-steal Caddy site behind Reality.
func startTLSTarget(t *testing.T, name string) int {
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
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "decoy") }),
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

// startOrigin is the "internet" the client reaches through the proxy.
func startOrigin(t *testing.T) string {
	t.Helper()
	body := strings.Repeat("x", 256*1024)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) })}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return "http://" + l.Addr().String() + "/"
}

// testBase lets traffic reach 127.0.0.1 (the default base blocks geoip:private).
const testBase = `{"log":{"loglevel":"warning"},"outbounds":[{"tag":"DIRECT","protocol":"freedom"}]}`

func TestTemplateBConfigAcceptedByXray(t *testing.T) {
	bin := testBinary(t)
	r, err := xrayconf.RenderInbound(xrayconf.InboundSpec{
		TemplateID:    "vless-xhttp-vkcdn",
		ProfileValues: map[string]any{"UPLINK_PATH": "/upload"},
		NodeValues:    map[string]any{"CDN_DOMAIN": "cdn.example.com"},
		Context:       xrayconf.NodeContext{Code: "NL", Domain: "origin.example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	base, _ := xrayconf.ParseBase(xrayconf.DefaultBaseJSON)
	cfg, err := xrayconf.BuildConfig(xrayconf.NodeConfig{
		Base: base, Inbounds: []*xrayconf.RenderedInbound{r}, APIAddr: "127.0.0.1:10085",
		Clients: map[string][]xrayconf.Client{r.Tag: {{Email: "1", ID: "11111111-1111-4111-8111-111111111111"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(cfg)
	if err := bin.Test(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
}

// End to end on a real Xray: template A with Reality, hot add user, traffic, stats, hot remove.
func TestRealityHotUsersAndStats(t *testing.T) {
	bin := testBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dir := t.TempDir()

	const domain = "example.test"
	targetPort := startTLSTarget(t, domain)
	origin := startOrigin(t)
	serverPort, apiPort, socksPort := freePort(t), freePort(t), freePort(t)

	tpl, _ := xrayconf.GetTemplate("vless-reality-selfsteal")
	nodeVals, err := tpl.GenerateValues(xrayconf.ScopeNode, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := xrayconf.RenderInbound(xrayconf.InboundSpec{
		TemplateID:    "vless-reality-selfsteal",
		ProfileValues: map[string]any{"SELFSTEAL_PORT": targetPort},
		NodeValues:    nodeVals,
		PortOverride:  serverPort,
		Context:       xrayconf.NodeContext{Code: "NL", Domain: domain, ListenIP: "127.0.0.1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	base, _ := xrayconf.ParseBase(testBase)
	apiAddr := "127.0.0.1:" + strconv.Itoa(apiPort)
	cfg, err := xrayconf.BuildConfig(xrayconf.NodeConfig{Base: base, Inbounds: []*xrayconf.RenderedInbound{r}, APIAddr: apiAddr})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(cfg)

	server := NewProcess(bin, filepath.Join(dir, "server.json"), nil)
	defer server.Close()
	if err := server.Apply(ctx, raw); err != nil {
		t.Fatal(err)
	}
	api, err := DialAPI(apiAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	if err := api.WaitReady(ctx); err != nil {
		t.Fatal(err, server.Tail())
	}

	uuid, _ := xrayconf.NewUUID()
	const email = "42"
	if err := api.AddVLESSUser(ctx, r.Tag, email, uuid, r.Flow); err != nil {
		t.Fatal(err)
	}
	if err := api.AddVLESSUser(ctx, r.Tag, email, uuid, r.Flow); err != nil {
		t.Fatalf("adding twice must be idempotent: %v", err)
	}
	users, err := api.InboundUsers(ctx, r.Tag)
	if err != nil || len(users) != 1 || users[0] != email {
		t.Fatalf("inbound users %v %v", users, err)
	}

	client := startClient(t, bin, dir, socksPort, serverPort, uuid, domain, r)
	defer client.Close()

	body, err := fetchVia(ctx, socksPort, origin)
	if err != nil {
		t.Fatalf("request through proxy failed: %v\nserver: %v\nclient: %v", err, server.Tail(), client.Tail())
	}
	if len(body) != 256*1024 {
		t.Fatalf("short body %d", len(body))
	}

	deltas, err := api.UserTrafficDeltas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(deltas) != 1 || deltas[0].Email != email || deltas[0].Downlink < 256*1024 {
		t.Fatalf("stats: %+v", deltas)
	}
	again, _ := api.UserTrafficDeltas(ctx)
	if len(again) != 0 {
		t.Fatalf("stats must be reset after reading: %+v", again)
	}

	if err := api.RemoveUser(ctx, r.Tag, email); err != nil {
		t.Fatal(err)
	}
	if err := api.RemoveUser(ctx, r.Tag, email); err != nil {
		t.Fatalf("removing twice must be idempotent: %v", err)
	}
	if _, err := fetchVia(ctx, socksPort, origin); err == nil {
		t.Fatal("removed user can still connect")
	}
}

func startClient(t *testing.T, bin Binary, dir string, socksPort, serverPort int, uuid, domain string, r *xrayconf.RenderedInbound) *Process {
	t.Helper()
	pub := r.Values["REALITY_PUBLIC_KEY"].(string)
	sid := r.Values["REALITY_SHORT_ID"].(string)
	cfg := map[string]any{
		"log":      map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": socksPort, "protocol": "socks", "settings": map[string]any{"udp": false}}},
		"outbounds": []any{map[string]any{
			"protocol": "vless",
			"settings": map[string]any{"vnext": []any{map[string]any{
				"address": "127.0.0.1", "port": serverPort,
				"users": []any{map[string]any{"id": uuid, "encryption": "none", "flow": r.Flow}},
			}}},
			"streamSettings": map[string]any{
				"network": "raw", "security": "reality",
				"realitySettings": map[string]any{"serverName": domain, "fingerprint": "firefox", "publicKey": pub, "shortId": sid},
			},
		}},
	}
	raw, _ := json.Marshal(cfg)
	p := NewProcess(bin, filepath.Join(dir, "client.json"), nil)
	if err := p.Apply(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(socksPort)); err == nil {
			c.Close()
			return p
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("client socks not listening: %v", p.Tail())
	return nil
}

func fetchVia(ctx context.Context, socksPort int, url string) ([]byte, error) {
	d, err := proxy.SOCKS5("tcp", "127.0.0.1:"+strconv.Itoa(socksPort), nil, proxy.Direct)
	if err != nil {
		return nil, err
	}
	hc := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext:       d.(proxy.ContextDialer).DialContext,
			DisableKeepAlives: true,
		},
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
