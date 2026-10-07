package e2e_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/apernet/quic-go"
	"github.com/apernet/quic-go/http3"
	"gopkg.in/yaml.v3"

	"github.com/vyto4ka/vynel/internal/node/agent"
	"github.com/vyto4ka/vynel/internal/panel/app"
	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
	"github.com/vyto4ka/vynel/internal/xray/xraytest"
)

// TestHysteria2NextToReality: Reality on TCP and Hysteria2 on UDP share one IP and port number.
// Caddy (behind Reality) gets the certificate for the domain; the agent hands the same file to
// Hysteria2. Unauthenticated HTTP/3 visitors get the decoy site, clients from the subscription
// (Xray JSON, and Mihomo when MIHOMO_BIN is set) get through.
func TestHysteria2NextToReality(t *testing.T) {
	bin := xraytest.Binary(t)
	caddyBin := os.Getenv("CADDY_BIN")
	if caddyBin == "" {
		t.Skip("CADDY_BIN not set; run `make test-integration`")
	}
	ctx := context.Background()
	const wait = 30 * time.Second

	dir := t.TempDir()
	panel := startPanel(t, dir, "127.0.0.1:"+strconv.Itoa(xraytest.FreePort(t)))
	svc := panel.Service
	must(t, store.UpsertBaseConfig(ctx, svc.Store().DB, &store.BaseConfig{Name: "default", JSON: testBase, IsDefault: true}))

	port, selfsteal := xraytest.FreePort(t), xraytest.FreePort(t)
	for k, v := range map[string]string{
		service.SettingCaddyIssuer:    "internal",
		service.SettingCaddyHTTPPort:  strconv.Itoa(xraytest.FreePort(t)),
		service.SettingCaddyAdminPort: strconv.Itoa(xraytest.FreePort(t)),
		service.SettingXrayAPIPort:    strconv.Itoa(xraytest.FreePort(t)),
		service.SettingSubDomain:      domain,
		service.SettingSubPort:        strconv.Itoa(port),
	} {
		must(t, svc.SetSetting(ctx, actor, k, v))
	}
	node, err := svc.EnsureLocalNode(ctx, service.NodeInput{Name: "Нидерланды", Country: "nl", Domain: domain})
	if err != nil {
		t.Fatal(err)
	}
	lo, err := svc.AddAddress(ctx, actor, node.ID, "127.0.0.1", "")
	if err != nil {
		t.Fatal(err)
	}
	main, _ := svc.GroupByName(ctx, "Основная")
	for _, tpl := range []string{"vless-reality-selfsteal", "hysteria2"} {
		prof, err := svc.CreateProfile(ctx, actor, service.ProfileInput{Name: tpl, TemplateID: tpl, Values: map[string]any{"SELFSTEAL_PORT": selfsteal}})
		if err != nil {
			t.Fatal(err)
		}
		must(t, svc.GrantAccess(ctx, actor, main.ID, store.AccessProfile, prof.ID))
		if _, err := svc.AttachProfile(ctx, actor, service.AttachInput{NodeID: node.ID, ProfileID: prof.ID, ListenAddressID: &lo.ID,
			PortOverride: port, Values: map[string]any{"DECOY_SITE": "docs"}}); err != nil {
			t.Fatal(err)
		}
	}
	ds, err := svc.DesiredState(ctx, node.ID)
	if err != nil || len(ds.Problems) > 0 {
		t.Fatalf("desired state: %v %v", err, ds.Problems)
	}
	if !bytes.Contains(ds.Caddy, []byte(`"protocols":["h1","h2"]`)) {
		t.Fatalf("caddy keeps HTTP/3 on: %s", ds.Caddy)
	}

	nodeDir := filepath.Join(t.TempDir(), "node")
	a, err := agent.New(agent.Config{
		DataDir: nodeDir, Xray: bin, Version: "test", CaddyBin: caddyBin,
		Dial: app.LocalDialer(ctx, panel.Reconciler, node.ID), Log: logger("hy2-node"),
		Addresses: func() []*nodev1.Address { return nil }, MaxBackoff: 500 * time.Millisecond,
		StatsInterval: 200 * time.Millisecond, CertInterval: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	actx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { _ = a.Run(actx); close(done) }()
	t.Cleanup(func() { cancel(); <-done; a.Close() })
	alice, err := svc.CreateUser(ctx, actor, service.CreateUserInput{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	xraytest.Eventually(t, wait, "node converges", inSync(ctx, svc, node.Code))

	// The agent swaps its placeholder for the certificate Caddy issued.
	crtPath := filepath.Join(nodeDir, "certs", domain+".crt")
	var pin string
	xraytest.Eventually(t, wait, "Caddy's certificate reaches Xray", func() error {
		raw, err := os.ReadFile(crtPath)
		if err != nil {
			return err
		}
		blk, _ := pem.Decode(raw)
		if blk == nil {
			return fmt.Errorf("no PEM")
		}
		c, err := x509.ParseCertificate(blk.Bytes)
		if err != nil {
			return err
		}
		if !strings.Contains(c.Issuer.CommonName, "Caddy") {
			return fmt.Errorf("issuer %q", c.Issuer.CommonName)
		}
		sum := sha256.Sum256(blk.Bytes)
		pin = hex.EncodeToString(sum[:])
		return nil
	})

	// Without a password, HTTP/3 on the Hysteria port is the decoy site, with Caddy's certificate.
	h3 := &http.Client{Timeout: 5 * time.Second, Transport: &http3.Transport{
		TLSClientConfig: &tls.Config{ServerName: domain, InsecureSkipVerify: true, NextProtos: []string{"h3"}},
		Dial: func(ctx context.Context, _ string, tc *tls.Config, qc *quic.Config) (*quic.Conn, error) {
			return quic.DialAddrEarly(ctx, "127.0.0.1:"+strconv.Itoa(port), tc, qc)
		},
	}}
	xraytest.Eventually(t, wait, "decoy over HTTP/3", func() error {
		resp, err := h3.Get(fmt.Sprintf("https://%s:%d/", domain, port))
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 || !strings.Contains(string(b), "<header><b>") {
			return fmt.Errorf("status %d body %.80q", resp.StatusCode, b)
		}
		if sum := sha256.Sum256(resp.TLS.PeerCertificates[0].Raw); hex.EncodeToString(sum[:]) != pin {
			return fmt.Errorf("HTTP/3 serves another certificate")
		}
		return nil
	})

	// The subscription has both points; the Hysteria2 one carries the user's UUID as password.
	sub := func(ua, suffix string) []byte {
		req, _ := http.NewRequest(http.MethodGet, "http://"+panel.subAddr+service.DefaultSubPrefix+alice.SubToken+suffix, nil)
		req.Header.Set("User-Agent", ua)
		req.Header.Set("x-hwid", "phone-1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return b
	}
	links := decodeLinks(t, sub("Happ/3", ""))
	if len(links) != 2 || !strings.HasPrefix(links[1], "hysteria2://"+alice.UUID+"@"+domain+":"+strconv.Itoa(port)+"/?") {
		t.Fatalf("links %v", links)
	}

	var configs []map[string]any
	if err := json.Unmarshal(sub("x", "/json"), &configs); err != nil || len(configs) != 2 {
		t.Fatalf("json subscription: %v", err)
	}
	cfg := configs[1]
	out := cfg["outbounds"].([]any)[0].(map[string]any)
	out["settings"].(map[string]any)["address"] = "127.0.0.1"
	out["streamSettings"].(map[string]any)["tlsSettings"].(map[string]any)["pinnedPeerCertSha256"] = pin // internal CA in tests
	socks := xraytest.FreePort(t)
	in := cfg["inbounds"].([]any)[0].(map[string]any)
	in["port"] = socks
	cfg["inbounds"] = []any{in}
	delete(cfg, "routing")
	delete(cfg, "dns")
	raw, _ := json.Marshal(cfg)
	xraytest.StartClient(t, bin, raw, socks)
	origin := xraytest.Origin(t)
	fetch := func(socks int) func() error {
		return func() error {
			b, err := xraytest.Fetch(ctx, socks, origin)
			if err == nil && len(b) != xraytest.OriginSize {
				return fmt.Errorf("short body %d", len(b))
			}
			return err
		}
	}
	xraytest.Eventually(t, wait, "VPN through Hysteria2 (Xray client)", fetch(socks))

	if mh := os.Getenv("MIHOMO_BIN"); mh != "" {
		var doc map[string]any
		if err := yaml.Unmarshal(sub("clash-verge/v2.2.3", ""), &doc); err != nil {
			t.Fatal(err)
		}
		var hy map[string]any
		for _, p := range doc["proxies"].([]any) {
			if p.(map[string]any)["type"] == "hysteria2" {
				hy = p.(map[string]any)
			}
		}
		if hy == nil || hy["password"] != alice.UUID {
			t.Fatalf("mihomo proxies %v", doc["proxies"])
		}
		hy["server"] = "127.0.0.1"
		hy["fingerprint"] = pin
		mport := xraytest.FreePort(t)
		mdir := t.TempDir()
		y, _ := yaml.Marshal(map[string]any{"mixed-port": mport, "mode": "rule", "log-level": "warning",
			"proxies": []any{hy}, "rules": []string{"MATCH," + hy["name"].(string)}})
		must(t, os.WriteFile(filepath.Join(mdir, "config.yaml"), y, 0o600))
		log := &syncBuffer{}
		cmd := exec.Command(mh, "-d", mdir)
		cmd.Stdout, cmd.Stderr = log, log
		must(t, cmd.Start())
		t.Cleanup(func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			if t.Failed() {
				t.Logf("mihomo: %s", log.String())
			}
		})
		xraytest.Eventually(t, wait, "mihomo listening", func() error {
			c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(mport))
			if err == nil {
				c.Close()
			}
			return err
		})
		xraytest.Eventually(t, wait, "VPN through Hysteria2 (Mihomo)", fetch(mport))
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
