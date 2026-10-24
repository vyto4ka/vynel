package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"testing"
	"time"
)

func csrFor(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "evil"}}, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

// A node certificate is a client certificate of this CA only, named by the panel and not by the
// node's CSR.
func TestSignNode(t *testing.T) {
	c, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	der, serial, err := c.SignNode(csrFor(t), 7)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	if cert.Subject.CommonName != "node-7" || SerialHex(cert.SerialNumber) != serial {
		t.Fatalf("subject %q, serial %s/%s", cert.Subject.CommonName, SerialHex(cert.SerialNumber), serial)
	}
	if cert.IsCA {
		t.Fatal("a node certificate must not be a CA")
	}
	opts := x509.VerifyOptions{Roots: c.Pool(), CurrentTime: time.Now(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	if _, err := cert.Verify(opts); err != nil {
		t.Fatalf("client verification: %v", err)
	}
	// It cannot pose as a server.
	opts.KeyUsages = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	if _, err := cert.Verify(opts); err == nil {
		t.Fatal("a node certificate was accepted as a server certificate")
	}
	// Another CA does not vouch for it.
	other, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	opts.Roots, opts.KeyUsages = other.Pool(), []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	if _, err := cert.Verify(opts); err == nil {
		t.Fatal("accepted by a foreign CA")
	}
	// Serials are unique.
	_, serial2, err := c.SignNode(csrFor(t), 7)
	if err != nil || serial2 == serial {
		t.Fatalf("serial reused: %s %s %v", serial, serial2, err)
	}
}

func TestSignNodeRejectsBadCSR(t *testing.T) {
	c, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.SignNode([]byte("not a csr"), 1); err == nil {
		t.Fatal("garbage accepted")
	}
	good := csrFor(t)
	bad := append([]byte(nil), good...)
	bad[len(bad)-1] ^= 0xff // the signature no longer matches
	if _, _, err := c.SignNode(bad, 1); err == nil {
		t.Fatal("a CSR with a broken signature was signed")
	}
}

func TestLoadOrCreateKeepsTheCA(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a.Fingerprint() != b.Fingerprint() {
		t.Fatal("the CA changed between loads: every node would lose the panel")
	}
	if !a.Cert.IsCA || a.Cert.MaxPathLen != 0 || !a.Cert.MaxPathLenZero {
		t.Fatalf("the CA must not sign sub-CAs: %+v", a.Cert)
	}
}

func TestServerCert(t *testing.T) {
	dir := t.TempDir()
	c, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	first, err := c.ServerCert("gw-one.internal")
	if err != nil {
		t.Fatal(err)
	}
	again, err := c.ServerCert("gw-one.internal")
	if err != nil || string(again.Certificate[0]) != string(first.Certificate[0]) {
		t.Fatalf("the certificate for the same name must be kept: %v", err)
	}
	other, err := c.ServerCert("gw-two.internal")
	if err != nil {
		t.Fatal(err)
	}
	if string(other.Certificate[0]) == string(first.Certificate[0]) {
		t.Fatal("a new name needs a new certificate")
	}
	leaf, _ := x509.ParseCertificate(other.Certificate[0])
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: c.Pool(), DNSName: "gw-two.internal"}); err != nil {
		t.Fatalf("server certificate: %v", err)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: c.Pool(), DNSName: "gw-one.internal"}); err == nil {
		t.Fatal("valid for another name")
	}
}
