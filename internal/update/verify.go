package update

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/vyto4ka/vynel/scripts"
)

// Releases are signed when scripts/release-key.sh put a public key into the installer
// (RELEASE_PUBKEY); CI then publishes SHA256SUMS.signed. The key is read from the installer
// built into this binary, so the update trusts what the running version trusts, not what a
// download says.

var pubKeyRe = regexp.MustCompile(`(?m)^RELEASE_PUBKEY="\$\{VYNEL_RELEASE_PUBKEY:-([^}]*)\}"`)

// installerSource is the installer built into this binary (a variable for tests).
var installerSource = func() []byte { return scripts.Install }

// releaseKey is the release signing key, nil when releases are not signed.
func releaseKey() (ed25519.PublicKey, error) {
	m := pubKeyRe.FindSubmatch(installerSource())
	if m == nil || len(m[1]) == 0 {
		return nil, nil
	}
	der, err := base64.StdEncoding.DecodeString(string(m[1]))
	if err != nil {
		return nil, fmt.Errorf("release key: %w", err)
	}
	k, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("release key: %w", err)
	}
	pk, ok := k.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("release key is not Ed25519")
	}
	return pk, nil
}

// verifySums checks SHA256SUMS.signed (the checksums, then "# ed25519 <base64>" over them as
// the last line) and returns the checksums by file name.
func verifySums(signed []byte, key ed25519.PublicKey) (map[string]string, error) {
	body := bytes.TrimRight(signed, "\n")
	i := bytes.LastIndexByte(body, '\n')
	if i < 0 {
		return nil, errors.New("SHA256SUMS.signed has no signature line")
	}
	msg, last := body[:i+1], string(body[i+1:])
	if !strings.HasPrefix(last, "# ed25519 ") {
		return nil, errors.New("SHA256SUMS.signed has no signature line")
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(last, "# ed25519 "))
	if err != nil || !ed25519.Verify(key, msg, sig) {
		return nil, errors.New("the release signature does not match: the download was changed on the way (mirror? proxy?)")
	}
	sums := map[string]string{}
	for _, line := range strings.Split(string(msg), "\n") {
		if f := strings.Fields(line); len(f) == 2 {
			sums[strings.TrimPrefix(f[1], "*")] = f[0]
		}
	}
	return sums, nil
}

// signedInstaller downloads the release's installer and returns it only if the release
// signature and its checksum match. A release from before installers were published has no
// install.sh in its checksums: the installer built into this binary is used then.
func signedInstaller(ctx context.Context, s Source, key ed25519.PublicKey) (script []byte, note string, err error) {
	signed, err := get(ctx, s.releaseURL()+"/SHA256SUMS.signed", 1<<20)
	if err != nil {
		return nil, "", fmt.Errorf("cannot download the release signature (SHA256SUMS.signed): %w", err)
	}
	sums, err := verifySums(signed, key)
	if err != nil {
		return nil, "", err
	}
	want, ok := sums["install.sh"]
	if !ok {
		return installerSource(), "the release has no signed installer: using the one built into this version", nil
	}
	script, err = get(ctx, s.releaseURL()+"/install.sh", 4<<20)
	if err != nil {
		return nil, "", fmt.Errorf("cannot download the installer: %w", err)
	}
	if got := sha256.Sum256(script); hex.EncodeToString(got[:]) != want {
		return nil, "", errors.New("the installer does not match the signed checksums: the download was changed on the way")
	}
	return script, "installer verified (release signature and checksum)", nil
}
