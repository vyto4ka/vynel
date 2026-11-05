package gateway_test

import (
	"context"
	"crypto/tls"
	"testing"
	"time"

	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

func TestTelegramTunnelRequiresNodeCertificate(t *testing.T) {
	e := newEnv(t)
	cc, err := grpc.NewClient(e.addr, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, RootCAs: e.ca.Pool(), ServerName: sni})))
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	ctx, cancel := context.WithTimeout(e.ctx, time.Second)
	defer cancel()
	stream, err := nodev1.NewNodeGatewayClient(cc).TelegramTunnel(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Send(&nodev1.TelegramFrame{Ticket: "fake"})
	if _, err := stream.Recv(); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unauthenticated tunnel: %v", err)
	}
}
