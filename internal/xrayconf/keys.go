package xrayconf

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
)

// generators produce values for variables with source: generate.
var generators = map[string]func(Variable) (any, error){
	"x25519": func(Variable) (any, error) {
		priv, _, err := NewX25519()
		return priv, err
	},
	"shortid": func(Variable) (any, error) { return randomHex(8) },
	"hex16":   func(Variable) (any, error) { return randomHex(16) },
	"uuid":    func(Variable) (any, error) { return NewUUID() },
	"path": func(v Variable) (any, error) {
		if len(v.Options) == 0 {
			s, err := randomHex(6)
			return "/" + s, err
		}
		return pick(v.Options)
	},
}

// derivations compute derived variables.
var derivations = map[string]func(string) (any, error){
	"x25519_public": func(priv string) (any, error) { return X25519Public(priv) },
	// XHTTP obfuscation (template C, docs/PROFILES.md §8.3): "cookie" puts the session in a
	// cookie and the sequence and padding in the query (needs a 2026 client core); "compat" keeps
	// Xray's defaults, which every Xray-based client understands.
	"xhttp_session_placement": xhttpPick("cookie", "path"),
	"xhttp_seq_placement":     xhttpPick("query", "path"),
	"xhttp_padding_placement": xhttpPick("query", "queryInHeader"),
	"xhttp_padding_key":       xhttpPick("cb", "x_padding"),
	"xhttp_padding_obfs":      xhttpPick(true, false),
}

func xhttpPick(cookie, compat any) func(string) (any, error) {
	return func(mode string) (any, error) {
		switch mode {
		case "cookie":
			return cookie, nil
		case "compat", "":
			return compat, nil
		}
		return nil, fmt.Errorf("unknown XHTTP obfuscation %q (cookie, compat)", mode)
	}
}

// NewX25519 returns a Reality key pair in Xray's encoding (base64 raw URL).
func NewX25519() (priv, pub string, err error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString(k.Bytes()), enc.EncodeToString(k.PublicKey().Bytes()), nil
}

// X25519Public derives the public key from a private key in Xray's encoding.
func X25519Public(priv string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(priv)
	if err != nil {
		return "", fmt.Errorf("private key is not base64url: %w", err)
	}
	k, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()), nil
}

// NewUUID returns a random RFC 4122 v4 UUID.
func NewUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func pick(opts []string) (string, error) {
	i, err := rand.Int(rand.Reader, big.NewInt(int64(len(opts))))
	if err != nil {
		return "", err
	}
	return opts[i.Int64()], nil
}
