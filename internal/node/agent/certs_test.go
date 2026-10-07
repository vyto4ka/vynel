package agent

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/apernet/quic-go"

	"github.com/vyto4ka/vynel/internal/node/state"
	"github.com/vyto4ka/vynel/internal/xray/xraytest"
	"github.com/vyto4ka/vynel/internal/xrayconf"
)

func TestCertBridge(t *testing.T) {
	dir := t.TempDir()
	b := newCertBridge(dir)
	cfg := []byte(`{"inbounds":[{"streamSettings":{"tlsSettings":{"certificates":[{"certificateFile":"vynel:cert:Node.Example.com","keyFile":"vynel:key:node.example.com"}]},
		"hysteriaSettings":{"masquerade":{"type":"file","dir":"vynel:decoy:cloud"}}}}]}`)
	out, doms, err := b.resolve(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(doms) != 1 || doms[0] != "node.example.com" {
		t.Fatalf("domains %v", doms)
	}
	crt := filepath.Join(dir, "certs", "node.example.com.crt")
	if !bytes.Contains(out, []byte(crt)) || bytes.Contains(out, []byte("vynel:")) {
		t.Fatalf("not resolved: %s", out)
	}
	if !b.pending("node.example.com") || !isSelfSigned(crt) {
		t.Fatal("expected a self-signed placeholder")
	}
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "web", "decoy", "cloud", "index.html")); err != nil {
		t.Fatalf("decoy not installed: %v", err)
	}

	// A restarted agent recognizes its own placeholder.
	b2 := newCertBridge(dir)
	if _, _, err := b2.resolve(cfg); err != nil || !b2.pending("node.example.com") {
		t.Fatalf("placeholder not recognized after restart: %v", err)
	}

	// Caddy gets the real certificate.
	real := filepath.Join(dir, "web", "caddy", "caddy", "certificates", "acme-v02.api.letsencrypt.org-directory", "node.example.com")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	realCrt, realKey, _ := selfSignedCert("node.example.com")
	realCrt = append([]byte("# real\n"), realCrt...)
	_ = os.WriteFile(filepath.Join(real, "node.example.com.crt"), realCrt, 0o600)
	_ = os.WriteFile(filepath.Join(real, "node.example.com.key"), realKey, 0o600)
	changed, err := b.sync("node.example.com")
	if err != nil || !changed || b.pending("node.example.com") {
		t.Fatalf("sync: changed=%v pending=%v err=%v", changed, b.pending("node.example.com"), err)
	}
	if got, _ := os.ReadFile(crt); !bytes.Equal(got, realCrt) {
		t.Fatal("certificate not copied")
	}
	if changed, _ := b.sync("node.example.com"); changed {
		t.Fatal("unchanged certificate reported as changed")
	}

	for _, bad := range []string{`"vynel:cert:../../etc/passwd"`, `"vynel:decoy:../x"`, `"vynel:nope:x"`} {
		if _, _, err := b.resolve([]byte(`{"a":` + bad + `}`)); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
}

// On a real Xray: a Hysteria2 inbound starts on the placeholder certificate, and when Caddy's
// certificate appears the agent restarts Xray, which then serves it.
func TestCertSwapRestartsXray(t *testing.T) {
	bin := xraytest.Binary(t)
	ctx := context.Background()
	dir := t.TempDir()
	port, apiPort := xraytest.FreePort(t), xraytest.FreePort(t)
	r, err := xrayconf.RenderInbound(xrayconf.InboundSpec{TemplateID: "hysteria2", PortOverride: port,
		Context: xrayconf.NodeContext{Code: "NL", Domain: "node.example.com", ListenIP: "127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	base, _ := xrayconf.ParseBase(`{"outbounds":[{"tag":"DIRECT","protocol":"freedom"}]}`)
	cfg, err := xrayconf.BuildConfig(xrayconf.NodeConfig{Base: base, Inbounds: []*xrayconf.RenderedInbound{r}, APIAddr: "127.0.0.1:" + strconv.Itoa(apiPort)})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(cfg)
	a, err := New(Config{DataDir: dir, Xray: bin, Dial: nil})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	st := &state.State{Config: raw, Inbounds: []state.Inbound{{Tag: r.Tag, Protocol: r.Protocol,
		Users: []state.User{{Email: "1", ID: "11111111-2222-4333-8444-555555555555"}}}}}
	a.mu.Lock()
	a.cur = st
	err = a.restartLocked(ctx, st)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	served := func() (*x509.Certificate, error) {
		qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		c, err := quic.DialAddr(qctx, "127.0.0.1:"+strconv.Itoa(port),
			&tls.Config{ServerName: "node.example.com", InsecureSkipVerify: true, NextProtos: []string{"h3"}}, nil)
		if err != nil {
			return nil, err
		}
		defer func() { _ = c.CloseWithError(0, "") }()
		return c.ConnectionState().TLS.PeerCertificates[0], nil
	}
	c, err := served()
	if err != nil || len(c.Subject.Organization) == 0 || c.Subject.Organization[0] != selfSignedOrg {
		t.Fatalf("placeholder not served: %v %v", c, err)
	}

	// Caddy issues the certificate.
	real := filepath.Join(dir, "web", "caddy", "caddy", "certificates", "local", "node.example.com")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	crt, key := issued(t, "node.example.com")
	_ = os.WriteFile(filepath.Join(real, "node.example.com.key"), key, 0o600)
	_ = os.WriteFile(filepath.Join(real, "node.example.com.crt"), crt, 0o600)
	a.mu.Lock()
	a.syncCertsLocked(ctx)
	a.mu.Unlock()
	xraytest.Eventually(t, 10*time.Second, "issued certificate served", func() error {
		c, err := served()
		if err != nil {
			return err
		}
		if c.Subject.CommonName != "issued" {
			return fmt.Errorf("serving %q", c.Subject.CommonName)
		}
		return nil
	})
}

func issued(t *testing.T, domain string) ([]byte, []byte) {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "issued"}, DNSNames: []string{domain},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	kd, _ := x509.MarshalECPrivateKey(k)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd})
}
