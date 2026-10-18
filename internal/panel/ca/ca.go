// Package ca is the panel's internal certificate authority for panel <-> node mTLS
// (docs/ARCHITECTURE.md §3, docs/STEALTH.md §2.6).
package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

const (
	caValidity     = 20 * 365 * 24 * time.Hour
	nodeValidity   = 10 * 365 * 24 * time.Hour
	serverValidity = 10 * 365 * 24 * time.Hour
)

// CA signs node client certificates and the gateway server certificate.
type CA struct {
	Cert *x509.Certificate
	Key  *ecdsa.PrivateKey
	dir  string
}

// LoadOrCreate loads ca.crt/ca.key from dir or creates a new CA there.
func LoadOrCreate(dir string) (*CA, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	certPath, keyPath := filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key")
	cert, key, err := loadPair(certPath, keyPath)
	if errors.Is(err, os.ErrNotExist) {
		key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		tpl := &x509.Certificate{
			SerialNumber:          randomSerial(),
			Subject:               pkix.Name{CommonName: "internal root"},
			NotBefore:             time.Now().Add(-time.Hour),
			NotAfter:              time.Now().Add(caValidity),
			IsCA:                  true,
			BasicConstraintsValid: true,
			MaxPathLenZero:        true,
			KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		}
		der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
		if err != nil {
			return nil, err
		}
		if err := savePair(certPath, keyPath, der, key); err != nil {
			return nil, err
		}
		cert, err = x509.ParseCertificate(der)
		if err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return &CA{Cert: cert, Key: key, dir: dir}, nil
}

// Fingerprint is the sha256 of the CA certificate (hex); nodes pin it before their first connection.
func (c *CA) Fingerprint() string { return Fingerprint(c.Cert.Raw) }

// Fingerprint returns the hex sha256 of a DER certificate.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// Pool returns a cert pool containing only this CA.
func (c *CA) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(c.Cert)
	return p
}

// SignNode signs a node CSR. The serial (hex) identifies the node on every connection.
func (c *CA) SignNode(csrDER []byte, nodeID int64) (certDER []byte, serial string, err error) {
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		return nil, "", fmt.Errorf("parse csr: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, "", fmt.Errorf("csr signature: %w", err)
	}
	sn := randomSerial()
	tpl := &x509.Certificate{
		SerialNumber: sn,
		Subject:      pkix.Name{CommonName: fmt.Sprintf("node-%d", nodeID)},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(nodeValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, c.Cert, csr.PublicKey, c.Key)
	if err != nil {
		return nil, "", err
	}
	return der, SerialHex(sn), nil
}

// SerialHex formats a certificate serial number.
func SerialHex(n *big.Int) string { return hex.EncodeToString(n.Bytes()) }

// ServerCert returns the gateway TLS certificate for sni, creating or re-issuing it when the
// name changed. The chain includes the CA so a node can pin it during Join.
func (c *CA) ServerCert(sni string) (tls.Certificate, error) {
	certPath, keyPath := filepath.Join(c.dir, "gateway.crt"), filepath.Join(c.dir, "gateway.key")
	cert, key, err := loadPair(certPath, keyPath)
	if err == nil && len(cert.DNSNames) == 1 && cert.DNSNames[0] == sni && time.Until(cert.NotAfter) > 30*24*time.Hour {
		return tls.Certificate{Certificate: [][]byte{cert.Raw, c.Cert.Raw}, PrivateKey: key, Leaf: cert}, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return tls.Certificate{}, err
	}
	key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tpl := &x509.Certificate{
		SerialNumber: randomSerial(),
		Subject:      pkix.Name{CommonName: sni},
		DNSNames:     []string{sni},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(serverValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, c.Cert, &key.PublicKey, c.Key)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := savePair(certPath, keyPath, der, key); err != nil {
		return tls.Certificate{}, err
	}
	leaf, _ := x509.ParseCertificate(der)
	return tls.Certificate{Certificate: [][]byte{der, c.Cert.Raw}, PrivateKey: key, Leaf: leaf}, nil
}

func randomSerial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		panic(err)
	}
	return n.Add(n, big.NewInt(1))
}

func loadPair(certPath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, err
	}
	cb, _ := pem.Decode(certPEM)
	kb, _ := pem.Decode(keyPEM)
	if cb == nil || kb == nil {
		return nil, nil, fmt.Errorf("bad PEM in %s or %s", certPath, keyPath)
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, nil, err
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

func savePair(certPath, keyPath string, der []byte, key *ecdsa.PrivateKey) error {
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	if err := WritePEM(keyPath, "EC PRIVATE KEY", kb, 0o600); err != nil {
		return err
	}
	return WritePEM(certPath, "CERTIFICATE", der, 0o644)
}

// WritePEM writes a PEM block atomically.
func WritePEM(path, typ string, der []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
