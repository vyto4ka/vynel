// Package gateway is the panel's gRPC endpoint for nodes: Join (install token -> certificate)
// and Connect (mTLS session handed to the reconciler).
package gateway

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/vyto4ka/vynel/internal/panel/ca"
	"github.com/vyto4ka/vynel/internal/panel/reconciler"
	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
)

// Server implements NodeGateway.
type Server struct {
	nodev1.UnimplementedNodeGatewayServer
	svc *service.Service
	ca  *ca.CA
	rec *reconciler.Reconciler
	sni string
	log *slog.Logger
}

// New creates the gateway.
func New(svc *service.Service, authority *ca.CA, rec *reconciler.Reconciler, sni string, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{svc: svc, ca: authority, rec: rec, sni: sni, log: log}
}

// TLSConfig requires the exact SNI (anything else aborts the handshake before a certificate is
// sent) and verifies client certificates when present (Join has none, Connect requires one).
func (g *Server) TLSConfig() (*tls.Config, error) {
	cert, err := g.ca.ServerCert(g.sni)
	if err != nil {
		return nil, err
	}
	base := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.VerifyClientCertIfGiven,
		ClientCAs:    g.ca.Pool(),
		NextProtos:   []string{"h2"},
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			if hello.ServerName != g.sni {
				return nil, errors.New("unknown server name")
			}
			return base, nil
		},
	}, nil
}

// Serve listens on addr until ctx ends.
func (g *Server) Serve(ctx context.Context, addr string) error {
	tlsCfg, err := g.TLSConfig()
	if err != nil {
		return err
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(tlsCfg)),
		grpc.KeepaliveParams(keepalive.ServerParameters{Time: 30 * time.Second, Timeout: 10 * time.Second}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 10 * time.Second, PermitWithoutStream: true}),
	)
	nodev1.RegisterNodeGatewayServer(srv, g)
	go func() {
		<-ctx.Done()
		srv.Stop()
	}()
	g.log.Info("node gateway listening", "addr", lis.Addr().String(), "sni", g.sni)
	if err := srv.Serve(lis); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

// Join registers a node with a one-time install token.
func (g *Server) Join(ctx context.Context, req *nodev1.JoinRequest) (*nodev1.JoinResponse, error) {
	var certDER []byte
	node, err := g.svc.ConsumeInstallToken(ctx, req.Token, func(n *store.Node) (string, error) {
		der, serial, err := g.ca.SignNode(req.CsrDer, n.ID)
		certDER = der
		return serial, err
	})
	if err != nil {
		if errors.Is(err, service.ErrInvalid) {
			g.log.Warn("node join rejected", "peer", peerAddr(ctx), "err", err)
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	g.log.Info("node joined", "node", node.Code, "peer", peerAddr(ctx))
	return &nodev1.JoinResponse{CertDer: certDER, CaDer: g.ca.Cert.Raw, NodeId: node.ID, NodeCode: node.Code}, nil
}

// Connect authenticates the node by its client certificate and hands the stream to the reconciler.
func (g *Server) Connect(stream grpc.BidiStreamingServer[nodev1.NodeMessage, nodev1.PanelMessage]) error {
	node, err := g.authenticate(stream.Context())
	if err != nil {
		return err
	}
	if !node.Enabled {
		g.log.Info("disabled node connected; it will receive an empty config", "node", node.Code)
	}
	return g.rec.Serve(node.ID, stream)
}

func (g *Server) authenticate(ctx context.Context) (*store.Node, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no peer")
	}
	info, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(info.State.VerifiedChains) == 0 {
		return nil, status.Error(codes.Unauthenticated, "client certificate required")
	}
	serial := ca.SerialHex(info.State.VerifiedChains[0][0].SerialNumber)
	node, err := store.GetNodeByCertSerial(ctx, g.svc.Store().DB, serial)
	if errors.Is(err, store.ErrNotFound) {
		g.log.Warn("connection with a revoked or unknown certificate", "peer", p.Addr.String(), "serial", serial)
		return nil, status.Error(codes.PermissionDenied, "certificate revoked")
	} else if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return node, nil
}

func peerAddr(ctx context.Context) string {
	if p, ok := peer.FromContext(ctx); ok {
		return p.Addr.String()
	}
	return "?"
}

// String helps debugging.
func (g *Server) String() string { return fmt.Sprintf("gateway(%s)", g.sni) }
