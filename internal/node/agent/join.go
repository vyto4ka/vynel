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
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"

	"github.com/vyto4ka/vynel/internal/jointoken"
	"github.com/vyto4ka/vynel/internal/panel/ca"
	nodev1 "github.com/vyto4ka/vynel/internal/proto/vynel/node/v1"
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

// Join registers the node with a join token and stores the credentials in dir. country is the
// node's country as the installer detected it ("" if unknown); the panel names the node by it.
// The panel's certificate is trusted only if its CA matches the fingerprint in the token.
func Join(ctx context.Context, dir, tokenStr, country string) (code string, err error) {
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
	resp, err := nodev1.NewNodeGatewayClient(conn).Join(ctx, &nodev1.JoinRequest{Token: tok.Secret, CsrDer: csr, Country: country})
	if err != nil {
		return "", formatJoinError(tok.Addr, err)
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

// formatJoinError turns raw gRPC/TLS errors into an actionable reason (clock skew, expired
// token, foreign server, or network reachability) while keeping the underlying error wrapped.
func formatJoinError(addr string, err error) error {
	msg := err.Error()
	var certErr x509.CertificateInvalidError
	switch {
	case (errors.As(err, &certErr) && certErr.Reason == x509.Expired) ||
		strings.Contains(msg, "certificate has expired or is not yet valid"):
		return fmt.Errorf("join: системные часы на ноде или панели не синхронизированы (сейчас на ноде %s UTC); включите NTP: timedatectl set-ntp true && timedatectl status (%w)",
			time.Now().UTC().Format(time.RFC3339), err)
	case status.Code(err) == codes.PermissionDenied:
		return fmt.Errorf("join: панель отклонила токен (одноразовый, действует 24 часа) — выпустите новый токен в панели (%w)", err)
	case strings.Contains(msg, "not signed by the pinned CA") || strings.Contains(msg, "unknown server name"):
		return fmt.Errorf("join: по адресу %s отвечает другой сервер или панель — проверьте, что токен взят с нужной панели (%w)", addr, err)
	case errors.Is(err, context.DeadlineExceeded) ||
		status.Code(err) == codes.DeadlineExceeded ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "no route to host") ||
		strings.Contains(msg, "network is unreachable"):
		return fmt.Errorf("join: не удалось подключиться к панели %s — проверьте, что служба vynel запущена и порт шлюза не закрыт у хостера (%w)", addr, err)
	}
	return fmt.Errorf("join: %w", err)
}

// SetPanelAddr changes the panel address the node dials (`vynel node set-panel`, docs/ARCHITECTURE.md §11.3).
func SetPanelAddr(dir, addr string) error {
	if host, port, err := net.SplitHostPort(addr); err != nil || host == "" || port == "" {
		return fmt.Errorf("panel address %q is not host:port", addr)
	}
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
	dial, _, closeFn, err := GRPCClient(dir)
	return dial, closeFn, err
}

// GRPCClient returns a Dialer and a GeoFetcher sharing one mTLS gRPC connection to the panel.
func GRPCClient(dir string) (Dialer, GeoFetcher, func(), error) {
	info, err := loadPanelInfo(dir)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("node is not joined (%w); run `vynel node join --token ...`", err)
	}
	cert, err := tls.LoadX509KeyPair(filepath.Join(dir, fileCert), filepath.Join(dir, fileKey))
	if err != nil {
		return nil, nil, nil, err
	}
	caPEM, err := os.ReadFile(filepath.Join(dir, fileCA))
	if err != nil {
		return nil, nil, nil, err
	}
	pool := x509.NewCertPool()
	if b, _ := pem.Decode(caPEM); b == nil || !pool.AppendCertsFromPEM(caPEM) {
		return nil, nil, nil, errors.New("bad ca.crt")
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS13, ServerName: info.SNI, RootCAs: pool, Certificates: []tls.Certificate{cert}}
	conn, err := grpc.NewClient(info.Addr,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 30 * time.Second, Timeout: 10 * time.Second, PermitWithoutStream: true}),
		// gRPC's default limit is 4 MB; a whole snapshot of a big node (an older panel does not
		// send parts) or a large stats batch may be more.
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(nodev1.MaxMessage), grpc.MaxCallSendMsgSize(nodev1.MaxMessage)),
	)
	if err != nil {
		return nil, nil, nil, err
	}
	client := nodev1.NewNodeGatewayClient(conn)
	dial := func(ctx context.Context) (Conn, error) {
		return client.Connect(ctx)
	}
	return dial, GRPCGeoFetcher(client), func() { conn.Close() }, nil
}
