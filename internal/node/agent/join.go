package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"

	"github.com/vyto4ka/vynnel/internal/jointoken"
	"github.com/vyto4ka/vynnel/internal/panel/ca"
	nodev1 "github.com/vyto4ka/vynnel/internal/proto/vynnel/node/v1"
)

// Credentials are stored in the node data dir after Join.
const (
	fileKey   = "node.key"
	fileCert  = "node.crt"
	fileCA    = "ca.crt"
	filePanel = "panel.json"
)

type panelInfo struct {
	Addr     string `json:"addr"`
	SNI      string `json:"sni"`
	NodeID   int64  `json:"node_id"`
	NodeCode string `json:"node_code"`
}

// Joined reports whether the data dir holds node credentials.
func Joined(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, fileCert))
	return err == nil
}

// Join registers the node with a join token and stores the credentials in dir.
// The panel's certificate is trusted only if its CA matches the fingerprint in the token.
func Join(ctx context.Context, dir, tokenStr string) (code string, err error) {
	tok, err := jointoken.Parse(tokenStr)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "node"}}, key)
	if err != nil {
		return "", err
	}
	tlsCfg := &tls.Config{
		MinVersion:         tls.VersionTLS13,
		ServerName:         tok.SNI,
		InsecureSkipVerify: true, // replaced by the pinned-CA check below
		VerifyConnection:   pinnedVerifier(tok.SNI, tok.CAFingerprint),
	}
	conn, err := grpc.NewClient(tok.Addr, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
	if err != nil {
		return "", err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := nodev1.NewNodeGatewayClient(conn).Join(ctx, &nodev1.JoinRequest{Token: tok.Secret, CsrDer: csr})
	if err != nil {
		return "", fmt.Errorf("join: %w", err)
	}
	if ca.Fingerprint(resp.CaDer) != tok.CAFingerprint {
		return "", errors.New("join: panel returned a different CA than the token pins")
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", err
	}
	if err := ca.WritePEM(filepath.Join(dir, fileKey), "EC PRIVATE KEY", kb, 0o600); err != nil {
		return "", err
	}
	if err := ca.WritePEM(filepath.Join(dir, fileCA), "CERTIFICATE", resp.CaDer, 0o644); err != nil {
		return "", err
	}
	info, _ := json.MarshalIndent(panelInfo{Addr: tok.Addr, SNI: tok.SNI, NodeID: resp.NodeId, NodeCode: resp.NodeCode}, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, filePanel), info, 0o600); err != nil {
		return "", err
	}
	// The certificate goes last: its presence marks a completed join.
	if err := ca.WritePEM(filepath.Join(dir, fileCert), "CERTIFICATE", resp.CertDer, 0o644); err != nil {
		return "", err
	}
	return resp.NodeCode, nil
}

// pinnedVerifier accepts the server only if one of the presented certificates is the pinned CA
// and the leaf is valid for sni under that CA.
func pinnedVerifier(sni, fingerprint string) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		var root *x509.Certificate
		for _, c := range cs.PeerCertificates {
			if ca.Fingerprint(c.Raw) == fingerprint {
				root = c
			}
		}
		if root == nil || len(cs.PeerCertificates) == 0 {
			return errors.New("panel certificate is not signed by the pinned CA")
		}
		pool := x509.NewCertPool()
		pool.AddCert(root)
		_, err := cs.PeerCertificates[0].Verify(x509.VerifyOptions{DNSName: sni, Roots: pool})
		return err
	}
}

// SetPanelAddr changes the panel address the node dials (`vynnel node set-panel`, docs/ARCHITECTURE.md §11.3).
func SetPanelAddr(dir, addr string) error {
	info, err := loadPanelInfo(dir)
	if err != nil {
		return err
	}
	info.Addr = addr
	b, _ := json.MarshalIndent(info, "", "  ")
	return os.WriteFile(filepath.Join(dir, filePanel), b, 0o600)
}

func loadPanelInfo(dir string) (*panelInfo, error) {
	raw, err := os.ReadFile(filepath.Join(dir, filePanel))
	if err != nil {
		return nil, err
	}
	var info panelInfo
	return &info, json.Unmarshal(raw, &info)
}

// GRPCDialer returns a Dialer using the stored credentials.
func GRPCDialer(dir string) (Dialer, func(), error) {
	info, err := loadPanelInfo(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("node is not joined (%w); run `vynnel node join --token ...`", err)
	}
	cert, err := tls.LoadX509KeyPair(filepath.Join(dir, fileCert), filepath.Join(dir, fileKey))
	if err != nil {
		return nil, nil, err
	}
	caPEM, err := os.ReadFile(filepath.Join(dir, fileCA))
	if err != nil {
		return nil, nil, err
	}
	pool := x509.NewCertPool()
	if b, _ := pem.Decode(caPEM); b == nil || !pool.AppendCertsFromPEM(caPEM) {
		return nil, nil, errors.New("bad ca.crt")
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS13, ServerName: info.SNI, RootCAs: pool, Certificates: []tls.Certificate{cert}}
	conn, err := grpc.NewClient(info.Addr,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 30 * time.Second, Timeout: 10 * time.Second, PermitWithoutStream: true}),
	)
	if err != nil {
		return nil, nil, err
	}
	client := nodev1.NewNodeGatewayClient(conn)
	dial := func(ctx context.Context) (Conn, error) {
		return client.Connect(ctx)
	}
	return dial, func() { conn.Close() }, nil
}
