// Package certs keeps the TLS certificates of the domains nginx serves: a self-signed
// placeholder first (nginx cannot start without a file), then a real one from Let's Encrypt
// over HTTP-01 (the token is served from a webroot by nginx on :80), renewed 30 days before
// it expires. Issuer "internal" signs with a local CA instead (tests, closed networks).
//
// Layout of Dir:
//
//	<domain>/fullchain.pem, <domain>/privkey.pem, <domain>/placeholder (marker)
//	_account/<directory host>.key   ACME account key
//	_ca/ca.pem, _ca/ca.key           internal CA
package certs

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/acme"
)

// LetsEncrypt is the production ACME directory.
const LetsEncrypt = "https://acme-v02.api.letsencrypt.org/directory"

// RenewBefore is how long before expiry a certificate is renewed.
const RenewBefore = 30 * 24 * time.Hour

var domainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// Manager issues and renews certificates. Safe for concurrent use.
type Manager struct {
	Dir          string // certificate store
	Webroot      string // HTTP-01 webroot served at /.well-known/acme-challenge/
	DirectoryURL string // ACME directory; "" = Let's Encrypt
	HTTPClient   *http.Client
	Log          *slog.Logger

	mu    sync.Mutex
	retry map[string]time.Time // domain -> earliest next attempt after a failure
	fails map[string]int
}

// Paths returns the certificate and key files of a domain.
func (m *Manager) Paths(domain string) (cert, key string) {
	d := filepath.Join(m.Dir, domain)
	return filepath.Join(d, "fullchain.pem"), filepath.Join(d, "privkey.pem")
}

// Ensure makes sure every domain has a certificate file, writing self-signed placeholders
// where none exists yet. It reports whether anything was written.
func (m *Manager) Ensure(domains []string) (bool, error) {
	changed := false
	for _, d := range domains {
		if !domainRe.MatchString(d) {
			return changed, fmt.Errorf("%q is not a domain", d)
		}
		cert, _ := m.Paths(d)
		if _, err := os.Stat(cert); err == nil {
			continue
		}
		key, err := newKey()
		if err != nil {
			return changed, err
		}
		der, err := selfSign(d, key, nil, nil, 90*24*time.Hour)
		if err != nil {
			return changed, err
		}
		if err := m.store(d, [][]byte{der}, key, true); err != nil {
			return changed, err
		}
		changed = true
	}
	return changed, nil
}

// Status is what is known about a domain's certificate.
type Status struct {
	Domain      string
	Placeholder bool
	Issuer      string
	NotAfter    time.Time
}

// Status reads the certificate of a domain.
func (m *Manager) Status(domain string) (Status, error) {
	cert, _ := m.Paths(domain)
	st := Status{Domain: domain}
	c, err := readCert(cert)
	if err != nil {
		return st, err
	}
	st.Issuer = c.Issuer.CommonName
	if st.Issuer == "" && len(c.Issuer.Organization) > 0 {
		st.Issuer = c.Issuer.Organization[0]
	}
	st.NotAfter = c.NotAfter
	_, perr := os.Stat(filepath.Join(m.Dir, domain, "placeholder"))
	st.Placeholder = perr == nil
	return st, nil
}

// Due reports whether a domain needs a (new) real certificate.
func (m *Manager) Due(domain string, now time.Time) bool {
	st, err := m.Status(domain)
	if err != nil {
		return true
	}
	return st.Placeholder || now.Add(RenewBefore).After(st.NotAfter)
}

// Renew issues certificates for the domains that are due and returns the renewed ones. A
// failure postpones that domain (1 min doubling up to 6 h) and is returned joined with others.
func (m *Manager) Renew(ctx context.Context, domains []string, email, issuer string) ([]string, error) {
	now := time.Now()
	var done []string
	var errs []error
	for _, d := range domains {
		if !m.Due(d, now) {
			continue
		}
		m.mu.Lock()
		next := m.retry[d]
		m.mu.Unlock()
		if now.Before(next) {
			continue
		}
		var err error
		if issuer == "internal" {
			err = m.issueInternal(d)
		} else {
			err = m.issueACME(ctx, d, email)
		}
		m.mu.Lock()
		if m.retry == nil {
			m.retry, m.fails = map[string]time.Time{}, map[string]int{}
		}
		if err != nil {
			m.fails[d]++
			wait := time.Minute << min(m.fails[d]-1, 9)
			if wait > 6*time.Hour {
				wait = 6 * time.Hour
			}
			m.retry[d] = now.Add(wait)
			errs = append(errs, fmt.Errorf("%s: %w", d, err))
		} else {
			delete(m.retry, d)
			delete(m.fails, d)
			done = append(done, d)
		}
		m.mu.Unlock()
	}
	return done, errors.Join(errs...)
}

// Retry clears the failure back-off, e.g. after the config changed.
func (m *Manager) Retry() {
	m.mu.Lock()
	m.retry, m.fails = nil, nil
	m.mu.Unlock()
}

func (m *Manager) log() *slog.Logger {
	if m.Log == nil {
		return slog.Default()
	}
	return m.Log
}

func (m *Manager) issueACME(ctx context.Context, domain, email string) error {
	dir := m.DirectoryURL
	if dir == "" {
		dir = LetsEncrypt
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	client, err := m.account(ctx, dir, email)
	if err != nil {
		return fmt.Errorf("account: %w", err)
	}
	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(domain))
	if err != nil {
		return fmt.Errorf("order: %w", err)
	}
	for _, u := range order.AuthzURLs {
		z, err := client.GetAuthorization(ctx, u)
		if err != nil {
			return err
		}
		if z.Status == acme.StatusValid {
			continue
		}
		var chal *acme.Challenge
		for _, c := range z.Challenges {
			if c.Type == "http-01" {
				chal = c
			}
		}
		if chal == nil {
			return fmt.Errorf("no http-01 challenge offered")
		}
		body, err := client.HTTP01ChallengeResponse(chal.Token)
		if err != nil {
			return err
		}
		file := filepath.Join(m.Webroot, ".well-known", "acme-challenge", chal.Token)
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil { //nolint:gosec // nginx workers must read it
			return err
		}
		_, err = client.Accept(ctx, chal)
		if err == nil {
			_, err = client.WaitAuthorization(ctx, z.URI)
		}
		_ = os.Remove(file)
		if err != nil {
			return fmt.Errorf("http-01 for %s (is port 80 reachable and the A record right?): %w", domain, err)
		}
	}
	if order, err = client.WaitOrder(ctx, order.URI); err != nil {
		return fmt.Errorf("order: %w", err)
	}
	key, err := newKey()
	if err != nil {
		return err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: domain}, DNSNames: []string{domain}}, key)
	if err != nil {
		return err
	}
	chain, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return fmt.Errorf("finalize: %w", err)
	}
	if err := m.store(domain, chain, key, false); err != nil {
		return err
	}
	m.log().Info("certificate issued", "domain", domain)
	return nil
}

// account loads or creates the ACME account key for a directory and registers it.
func (m *Manager) account(ctx context.Context, dir, email string) (*acme.Client, error) {
	u, err := url.Parse(dir)
	if err != nil {
		return nil, err
	}
	keyFile := filepath.Join(m.Dir, "_account", u.Host+".key")
	key, err := readKey(keyFile)
	if err != nil {
		k, err := newKey()
		if err != nil {
			return nil, err
		}
		if err := writeKey(keyFile, k); err != nil {
			return nil, err
		}
		key = k
	}
	client := &acme.Client{Key: key, DirectoryURL: dir, HTTPClient: m.HTTPClient, UserAgent: "vynel"}
	acct := &acme.Account{}
	if email != "" {
		acct.Contact = []string{"mailto:" + email}
	}
	if _, err := client.Register(ctx, acct, acme.AcceptTOS); err != nil && !errors.Is(err, acme.ErrAccountAlreadyExists) {
		return nil, err
	}
	return client, nil
}

// issueInternal signs a certificate with the local CA.
func (m *Manager) issueInternal(domain string) error {
	caCert, caKey, err := m.internalCA()
	if err != nil {
		return err
	}
	key, err := newKey()
	if err != nil {
		return err
	}
	der, err := selfSign(domain, key, caCert, caKey, 180*24*time.Hour)
	if err != nil {
		return err
	}
	return m.store(domain, [][]byte{der, caCert.Raw}, key, false)
}

// InternalCA returns the PEM of the local CA (tests trust it).
func (m *Manager) InternalCA() ([]byte, error) {
	c, _, err := m.internalCA()
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw}), nil
}

func (m *Manager) internalCA() (*x509.Certificate, crypto.Signer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	certFile, keyFile := filepath.Join(m.Dir, "_ca", "ca.pem"), filepath.Join(m.Dir, "_ca", "ca.key")
	if c, err := readCert(certFile); err == nil {
		if k, err := readKey(keyFile); err == nil {
			return c, k, nil
		}
	}
	key, err := newKey()
	if err != nil {
		return nil, nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber: serial(), Subject: pkix.Name{CommonName: "vynel local CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(10, 0, 0),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, key.Public(), key)
	if err != nil {
		return nil, nil, err
	}
	if err := writeKey(keyFile, key); err != nil {
		return nil, nil, err
	}
	if err := atomicWrite(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return nil, nil, err
	}
	c, err := x509.ParseCertificate(der)
	return c, key, err
}

// store writes a chain and key atomically (key first, so a reader never pairs a new
// certificate with the old key for long; nginx reloads only after both are in place).
func (m *Manager) store(domain string, chain [][]byte, key crypto.Signer, placeholder bool) error {
	certFile, keyFile := m.Paths(domain)
	if err := writeKey(keyFile, key); err != nil {
		return err
	}
	var pemChain []byte
	for _, der := range chain {
		pemChain = append(pemChain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	if err := atomicWrite(certFile, pemChain, 0o644); err != nil {
		return err
	}
	marker := filepath.Join(m.Dir, domain, "placeholder")
	if placeholder {
		return os.WriteFile(marker, nil, 0o600)
	}
	if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Import copies a certificate obtained elsewhere (Caddy's storage) when the store has none or
// only a placeholder and the imported one is valid for longer than RenewBefore.
func (m *Manager) Import(domain string, certPEM, keyPEM []byte) (bool, error) {
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return false, err
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return false, err
	}
	if leaf.VerifyHostname(domain) != nil || time.Now().Add(RenewBefore).After(leaf.NotAfter) {
		return false, nil
	}
	if st, err := m.Status(domain); err == nil && !st.Placeholder {
		return false, nil
	}
	certFile, keyFile := m.Paths(domain)
	if err := atomicWrite(keyFile, keyPEM, 0o600); err != nil {
		return false, err
	}
	if err := atomicWrite(certFile, certPEM, 0o644); err != nil {
		return false, err
	}
	_ = os.Remove(filepath.Join(m.Dir, domain, "placeholder"))
	return true, nil
}

// Dummy returns a self-signed certificate for no real name (made once): what nginx without
// ssl_reject_handshake shows to connections it then closes, so no domain leaks.
func (m *Manager) Dummy() (cert, key string, err error) {
	dir := filepath.Join(m.Dir, "_default")
	cert, key = filepath.Join(dir, "fullchain.pem"), filepath.Join(dir, "privkey.pem")
	if _, err := os.Stat(cert); err == nil {
		return cert, key, nil
	}
	k, err := newKey()
	if err != nil {
		return "", "", err
	}
	tpl := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(10, 0, 0)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, k.Public(), k)
	if err != nil {
		return "", "", err
	}
	if err := writeKey(key, k); err != nil {
		return "", "", err
	}
	return cert, key, atomicWrite(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
}

// Domains lists the domains in the store.
func (m *Manager) Domains() []string {
	ents, _ := os.ReadDir(m.Dir)
	var out []string
	for _, e := range ents {
		if e.IsDir() && !strings.HasPrefix(e.Name(), "_") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

func newKey() (*ecdsa.PrivateKey, error) { return ecdsa.GenerateKey(elliptic.P256(), rand.Reader) }

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	return n
}

// selfSign makes a leaf for domain, signed by ca (or by itself when ca is nil).
func selfSign(domain string, key crypto.Signer, ca *x509.Certificate, caKey crypto.Signer, valid time.Duration) ([]byte, error) {
	tpl := &x509.Certificate{
		SerialNumber: serial(), Subject: pkix.Name{CommonName: domain, Organization: []string{"vynel placeholder"}},
		DNSNames: []string{domain}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(valid),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	parent, signer := tpl, key
	if ca != nil {
		tpl.Subject.Organization = nil
		parent, signer = ca, caKey
	}
	return x509.CreateCertificate(rand.Reader, tpl, parent, key.Public(), signer)
}

func readCert(path string) (*x509.Certificate, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	b, _ := pem.Decode(raw)
	if b == nil {
		return nil, fmt.Errorf("%s: no PEM", path)
	}
	return x509.ParseCertificate(b.Bytes)
}

func readKey(path string) (crypto.Signer, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	b, _ := pem.Decode(raw)
	if b == nil {
		return nil, fmt.Errorf("%s: no PEM", path)
	}
	if k, err := x509.ParseECPrivateKey(b.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(b.Bytes)
	if err != nil {
		return nil, err
	}
	s, ok := k.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("%s: unsupported key", path)
	}
	return s, nil
}

func writeKey(path string, key crypto.Signer) error {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	return atomicWrite(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600)
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
