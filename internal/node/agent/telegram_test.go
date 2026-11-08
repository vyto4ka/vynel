package agent

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/vyto4ka/vynel/internal/jointoken"
	"github.com/vyto4ka/vynel/internal/panel/ca"
	"github.com/vyto4ka/vynel/internal/panel/gateway"
	"github.com/vyto4ka/vynel/internal/panel/reconciler"
	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
	"github.com/vyto4ka/vynel/internal/telegramrelay"
)

type observedTCP struct {
	net.Conn
	mu    *sync.Mutex
	bytes *bytes.Buffer
}

func (c observedTCP) Write(p []byte) (int, error) {
	c.mu.Lock()
	_, _ = c.bytes.Write(p)
	c.mu.Unlock()
	return c.Conn.Write(p)
}

// Real nested TLS and real gRPC flow control, without Xray/nginx binaries or Internet access.
func TestTelegramRelayEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := service.New(st)
	if err := svc.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	n, secret, err := svc.CreateNode(ctx, service.ActorCLI, service.NodeInput{Name: "relay", Country: "DE"})
	if err != nil {
		t.Fatal(err)
	}
	authority, err := ca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rec := reconciler.New(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rec.Debounce = time.Millisecond
	svc.OnChange = rec.Notify
	go func() { _ = rec.Run(ctx) }()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	gw := gateway.New(svc, authority, rec, "gw-relay.internal", nil)
	go func() { _ = gw.Serve(ctx, addr) }()
	for range 100 {
		c, err := net.DialTimeout("tcp", addr, 10*time.Millisecond)
		if err == nil {
			_ = c.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	dir := filepath.Join(t.TempDir(), "node")
	tok := jointoken.Token{Addr: addr, SNI: "gw-relay.internal", CAFingerprint: authority.Fingerprint(), Secret: secret}.Encode()
	if _, err := Join(ctx, dir, tok, "DE"); err != nil {
		t.Fatal(err)
	}
	dial, closeConn, err := GRPCDialer(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeConn()
	control, err := dial(ctx)
	if err != nil {
		t.Fatal(err)
	}
	remote := control.(*remoteConn)
	if err := control.Send(&nodev1.NodeMessage{Msg: &nodev1.NodeMessage_Hello{Hello: &nodev1.Hello{Capabilities: []string{telegramrelay.Capability}}}}); err != nil {
		t.Fatal(err)
	}
	const botToken = "123456789:never-visible-to-the-relay-node"
	uploadStarted, releaseUpload := make(chan struct{}, 1), make(chan struct{})
	uploadBytes := bytes.Repeat([]byte("backup data"), 500000)
	api := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/bot"+botToken+"/") {
			http.Error(w, "bad token", http.StatusUnauthorized)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/sendDocument") {
			uploadStarted <- struct{}{}
			select {
			case <-releaseUpload:
			case <-ctx.Done():
				return
			}
			n, err := io.Copy(io.Discard, r.Body)
			if err != nil || n != int64(len(uploadBytes)) {
				t.Errorf("upload truncated: %d/%d %v", n, len(uploadBytes), err)
				return
			}
		}
		_, _ = io.WriteString(w, `{"ok":true,"result":{"username":"relay_test_bot"}}`)
	}))
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"api.telegram.org"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	api.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	api.StartTLS()
	defer api.Close()
	root, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(root)
	var nodeBytes bytes.Buffer
	var nodeMu sync.Mutex
	a := &Agent{log: slog.New(slog.NewTextHandler(io.Discard, nil)), cfg: Config{TelegramDial: func(ctx context.Context) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, "tcp", api.Listener.Addr().String())
		if err != nil {
			return nil, err
		}
		return observedTCP{c, &nodeMu, &nodeBytes}, nil
	}}}
	var snapshots atomic.Int64
	slots := make(chan struct{}, telegramrelay.MaxTunnels)
	go func() {
		for {
			m, err := control.Recv()
			if err != nil {
				return
			}
			if c := m.GetTelegramConnect(); c != nil {
				go a.telegramConnect(ctx, remote, c.Ticket, slots)
			}
			if s := m.GetSnapshot(); s != nil {
				if err := control.Send(&nodev1.NodeMessage{Msg: &nodev1.NodeMessage_Ack{Ack: &nodev1.Ack{Revision: s.Revision, Hash: s.Hash}}}); err != nil {
					return
				}
				snapshots.Add(1)
			}
		}
	}()
	for range 200 {
		if node, err := svc.Node(ctx, n.ID); err == nil && node.LastSeenAt != nil && snapshots.Load() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
		if address != telegramrelay.Address {
			t.Errorf("unexpected address %q", address)
		}
		return rec.DialTelegram(ctx, n.ID)
	}}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr, Timeout: 10 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.telegram.org/bot"+botToken+"/getMe", nil)
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || !bytes.Contains(body, []byte("relay_test_bot")) {
		t.Fatalf("bad API response: %s %v", body, err)
	}
	before := snapshots.Load()
	finished := make(chan error, 1)
	go func() {
		req, err := http.NewRequestWithContext(ctx, "POST", "https://api.telegram.org/bot"+botToken+"/sendDocument", bytes.NewReader(uploadBytes))
		if err == nil {
			var resp *http.Response
			resp, err = hc.Do(req)
			if err == nil {
				_, err = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
		}
		finished <- err
	}()
	select {
	case <-uploadStarted:
	case <-ctx.Done():
		t.Fatal("upload did not start")
	}
	if err := rec.Resync(n.ID); err != nil {
		t.Fatal(err)
	}
	for range 200 {
		if snapshots.Load() > before {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if snapshots.Load() <= before {
		t.Fatal("bot upload blocked the control stream")
	}
	close(releaseUpload)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	nodeMu.Lock()
	leaked := bytes.Contains(nodeBytes.Bytes(), []byte(botToken))
	nodeMu.Unlock()
	if leaked {
		t.Fatal("node saw the bot token outside Telegram TLS")
	}
	// Untrusted Telegram certificates remain rejected even through a trusted node.
	bad := tr.Clone()
	bad.TLSClientConfig = &tls.Config{RootCAs: x509.NewCertPool(), MinVersion: tls.VersionTLS12}
	defer bad.CloseIdleConnections()
	if resp, err := (&http.Client{Transport: bad, Timeout: time.Second}).Do(req); err == nil {
		_ = resp.Body.Close()
		t.Fatal("accepted an untrusted Telegram certificate")
	}
	if _, err := svc.UpdateNode(ctx, service.ActorCLI, n.ID, service.NodeInput{}, false); err != nil {
		t.Fatal(err)
	}
	if c, err := rec.DialTelegram(ctx, n.ID); err == nil {
		_ = c.Close()
		t.Fatal("routed through a disabled node")
	}
	stream, err := remote.openTelegram(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Send(&nodev1.TelegramFrame{Ticket: strings.Repeat("a", 64)})
	if _, err := stream.Recv(); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("disabled node tunnel: %v", err)
	}
}
