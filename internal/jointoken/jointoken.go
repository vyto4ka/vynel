// Package jointoken encodes everything a node needs for its first connection in one string,
// like Remnawave's SECRET_KEY but without manual copying of certificates.
package jointoken

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
)

const prefix = "vpn1."

// Token is the decoded join token.
type Token struct {
	Addr          string `json:"a"` // panel gateway host:port as nodes reach it
	SNI           string `json:"s"` // gateway TLS server name (required, docs/STEALTH.md §2.6)
	CAFingerprint string `json:"f"` // sha256 of the CA certificate, pinned before trust exists
	Secret        string `json:"t"` // one-time install token
}

// Encode returns the string form.
func (t Token) Encode() string {
	b, _ := json.Marshal(t)
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

// Parse decodes and validates a token string.
func Parse(s string) (Token, error) {
	var t Token
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, prefix) {
		return t, errors.New("not a node join token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(s, prefix))
	if err != nil {
		return t, fmt.Errorf("join token: %w", err)
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return t, fmt.Errorf("join token: %w", err)
	}
	if _, _, err := net.SplitHostPort(t.Addr); err != nil {
		return t, fmt.Errorf("join token: bad panel address %q", t.Addr)
	}
	if t.SNI == "" || len(t.CAFingerprint) != 64 || t.Secret == "" {
		return t, errors.New("join token is incomplete")
	}
	return t, nil
}
