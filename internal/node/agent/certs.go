package agent

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/vyto4ka/vynel/internal/decoy"
)

// Node-local paths in the Xray config. The panel does not know the node's directories, so
// templates write placeholders and the agent replaces them before starting Xray
// (docs/PROFILES.md §9.3):
//
//	vynel:cert:<domain>  the certificate Caddy got for <domain> (temporary self-signed until then)
//	vynel:key:<domain>   its private key
//	vynel:decoy:<name>   the decoy site directory Caddy serves
const placeholderPrefix = "vynel:"

var certDomainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$`)

// certBridge copies the certificates Caddy obtains into a stable place for Xray.
type certBridge struct {
	caddyStorage string // Caddy's data dir: <storage>/certificates/<issuer>/<domain>/<domain>.crt
	dir          string // where Xray reads them
	decoyDir     string
	selfSigned   map[string]bool // domains currently served with a placeholder certificate
}

func newCertBridge(dataDir string) *certBridge {
	web := filepath.Join(dataDir, "web")
	return &certBridge{
		caddyStorage: filepath.Join(web, "caddy", "caddy"),
		dir:          filepath.Join(dataDir, "certs"),
		decoyDir:     filepath.Join(web, "decoy"),
		selfSigned:   map[string]bool{},
	}
}

// resolve replaces the placeholders in an Xray config and returns the domains whose
// certificates it uses.
func (b *certBridge) resolve(config []byte) ([]byte, []string, error) {
	if !bytes.Contains(config, []byte(placeholderPrefix)) {
		return config, nil, nil
	}
	var cfg any
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, nil, err
	}
	seen := map[string]bool{}
	var domains []string
	var walkErr error
	var walk func(v any) any
	walk = func(v any) any {
		switch t := v.(type) {
		case map[string]any:
			for k, x := range t {
				t[k] = walk(x)
			}
		case []any:
			for i, x := range t {
				t[i] = walk(x)
			}
		case string:
			if !strings.HasPrefix(t, placeholderPrefix) {
				return t
			}
			out, domain, err := b.path(t)
			if err != nil {
				walkErr = errors.Join(walkErr, err)
				return t
			}
			if domain != "" && !seen[domain] {
				seen[domain] = true
				domains = append(domains, domain)
			}
			return out
		}
		return v
	}
	cfg = walk(cfg)
	if walkErr != nil {
		return nil, nil, walkErr
	}
	out, err := json.Marshal(cfg)
	return out, domains, err
}

func (b *certBridge) path(ph string) (string, string, error) {
	kind, arg, _ := strings.Cut(strings.TrimPrefix(ph, placeholderPrefix), ":")
	switch kind {
	case "cert", "key":
		domain := strings.ToLower(arg)
		if !certDomainRe.MatchString(domain) {
			return "", "", fmt.Errorf("%s: bad domain", ph)
		}
		if _, err := b.sync(domain); err != nil {
			return "", "", fmt.Errorf("certificate for %s: %w", domain, err)
		}
		ext := ".crt"
		if kind == "key" {
			ext = ".key"
		}
		return filepath.Join(b.dir, domain+ext), domain, nil
	case "decoy":
		dir, err := decoy.Install(b.decoyDir, arg)
		if err != nil {
			return "", "", fmt.Errorf("%s: %w", ph, err)
		}
		return dir, "", nil
	}
	return "", "", fmt.Errorf("unknown placeholder %s", ph)
}

// sync makes <dir>/<domain>.crt and .key exist: Caddy's certificate when it has one, else a
// self-signed placeholder so Xray can start. It reports whether the files changed.
// Writes are atomic (rename), so Xray's hourly certificate reload never reads half a file.
func (b *certBridge) sync(domain string) (bool, error) {
	if err := os.MkdirAll(b.dir, 0o700); err != nil {
		return false, err
	}
	crtPath, keyPath := filepath.Join(b.dir, domain+".crt"), filepath.Join(b.dir, domain+".key")
	if crt, key, ok := b.caddyCert(domain); ok {
		oldCrt, _ := os.ReadFile(crtPath)
		oldKey, _ := os.ReadFile(keyPath)
		b.selfSigned[domain] = false
		if bytes.Equal(oldCrt, crt) && bytes.Equal(oldKey, key) {
			return false, nil
		}
		if err := writeAtomic(keyPath, key); err != nil {
			return false, err
		}
		return true, writeAtomic(crtPath, crt)
	}
	if _, err := os.Stat(crtPath); err == nil {
		if _, seen := b.selfSigned[domain]; !seen {
			b.selfSigned[domain] = isSelfSigned(crtPath)
		}
		return false, nil
	}
	crt, key, err := selfSignedCert(domain)
	if err != nil {
		return false, err
	}
	if err := writeAtomic(keyPath, key); err != nil {
		return false, err
	}
	b.selfSigned[domain] = true
	return true, writeAtomic(crtPath, crt)
}

// caddyCert finds the newest certificate Caddy stored for the domain (any issuer).
func (b *certBridge) caddyCert(domain string) (crt, key []byte, ok bool) {
	matches, _ := filepath.Glob(filepath.Join(b.caddyStorage, "certificates", "*", domain, domain+".crt"))
	var best string
	var bestTime time.Time
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil && fi.ModTime().After(bestTime) {
			best, bestTime = m, fi.ModTime()
		}
	}
	if best == "" {
		return nil, nil, false
	}
	crt, err1 := os.ReadFile(best)
	key, err2 := os.ReadFile(strings.TrimSuffix(best, ".crt") + ".key")
	if err1 != nil || err2 != nil || len(crt) == 0 || len(key) == 0 {
		return nil, nil, false
	}
	return crt, key, true
}

// pending reports whether a domain still runs on the placeholder certificate.
func (b *certBridge) pending(domain string) bool { return b.selfSigned[domain] }

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// selfSignedOrg marks placeholder certificates, so a restarted agent recognizes them.
const selfSignedOrg = "vynel placeholder"

func selfSignedCert(domain string) ([]byte, []byte, error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	tpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: domain, Organization: []string{selfSignedOrg}},
		DNSNames: []string{domain}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(10, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		return nil, nil, err
	}
	kd, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd}), nil
}

func isSelfSigned(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	blk, _ := pem.Decode(raw)
	if blk == nil {
		return false
	}
	c, err := x509.ParseCertificate(blk.Bytes)
	return err == nil && len(c.Subject.Organization) > 0 && c.Subject.Organization[0] == selfSignedOrg
}
