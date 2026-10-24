package gateway_test

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/vyto4ka/vynel/internal/jointoken"
	"github.com/vyto4ka/vynel/internal/node/agent"
	"github.com/vyto4ka/vynel/internal/panel/ca"
	"github.com/vyto4ka/vynel/internal/panel/gateway"
	"github.com/vyto4ka/vynel/internal/panel/reconciler"
	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
)

const sni = "gw-test.internal"

type env struct {
	ctx  context.Context
	svc  *service.Service
	ca   *ca.CA
	addr string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := service.New(st)
	if err := svc.EnsureDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	authority, err := ca.LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := lis.Addr().String()
	lis.Close()
	g := gateway.New(svc, authority, reconciler.New(svc, nil), sni, nil)
	go func() { _ = g.Serve(ctx, addr) }()
	for i := 0; i < 100; i++ { // wait for the listener
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return &env{ctx: ctx, svc: svc, ca: authority, addr: addr}
}

// token creates a node and returns its join token string, as the panel shows it.
func (e *env) token(t *testing.T, fingerprint string) (string, int64) {
	t.Helper()
	n, secret, err := e.svc.CreateNode(e.ctx, service.ActorCLI, service.NodeInput{Name: "Нода", Country: "nl", Domain: "nl.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	return jointoken.Token{Addr: e.addr, SNI: sni, CAFingerprint: fingerprint, Secret: secret}.Encode(), n.ID
}

func code(err error) codes.Code { return status.Code(err) }

func TestJoin(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.token(t, e.ca.Fingerprint())

	dir := filepath.Join(t.TempDir(), "node")
	if _, err := agent.Join(e.ctx, dir, tok, "NL"); err != nil {
		t.Fatalf("join: %v", err)
	}
	if !agent.Joined(dir) {
		t.Fatal("no credentials stored")
	}
	// A token joins once.
	if _, err := agent.Join(e.ctx, filepath.Join(t.TempDir(), "again"), tok, "NL"); err == nil {
		t.Fatal("a used token joined a second node")
	}
	// A wrong secret.
	bad := jointoken.Token{Addr: e.addr, SNI: sni, CAFingerprint: e.ca.Fingerprint(), Secret: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}.Encode()
	if _, err := agent.Join(e.ctx, filepath.Join(t.TempDir(), "bad"), bad, ""); err == nil || code(unwrap(err)) != codes.PermissionDenied {
		t.Fatalf("a made-up token: %v", err)
	}
}

func unwrap(err error) error {
	for err != nil {
		if _, ok := status.FromError(err); ok && status.Code(err) != codes.Unknown {
			return err
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return err
		}
		err = u.Unwrap()
	}
	return err
}

// A node that is told another CA's fingerprint does not trust the panel it reached.
func TestJoinPinsTheCA(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.token(t, "00"+e.ca.Fingerprint()[2:])
	if _, err := agent.Join(e.ctx, filepath.Join(t.TempDir(), "node"), tok, ""); err == nil {
		t.Fatal("joined a panel whose CA is not the pinned one")
	}
}

func TestConnectAuthentication(t *testing.T) {
	e := newEnv(t)
	tok, nodeID := e.token(t, e.ca.Fingerprint())
	dir := filepath.Join(t.TempDir(), "node")
	if _, err := agent.Join(e.ctx, dir, tok, ""); err != nil {
		t.Fatal(err)
	}
	dial, closeConn, err := agent.GRPCDialer(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeConn()

	// With a valid certificate the stream is accepted: it then waits for the reconciler (not
	// started here), so the call ends by the deadline and not by an authentication error.
	ctx, cancel := context.WithTimeout(e.ctx, 1500*time.Millisecond)
	defer cancel()
	conn, err := dial(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Recv()
	if c := code(err); c == codes.Unauthenticated || c == codes.PermissionDenied {
		t.Fatalf("a valid node was refused: %v", err)
	}

	// Reissuing the token revokes the certificate: the same credentials stop working.
	if _, err := e.svc.ReissueInstallToken(e.ctx, service.ActorCLI, nodeID); err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithTimeout(e.ctx, 3*time.Second)
	defer cancel2()
	conn, err = dial(ctx2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Recv(); code(err) != codes.PermissionDenied {
		t.Fatalf("a revoked certificate: %v, want PermissionDenied", err)
	}
}
