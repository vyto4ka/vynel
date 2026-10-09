#!/usr/bin/env bash
# Creates the release signing key (docs/DEVELOPMENT.md, "Signed releases").
#
#   scripts/release-key.sh
#
# It prints the private key for the GitHub secret RELEASE_SIGNING_KEY and writes the public key
# into scripts/install.sh. Commit install.sh after adding the secret: from then on CI signs
# every release and installers refuse binaries that are not signed with this key.
set -euo pipefail
cd "$(dirname "$0")/.."
key="$(mktemp)"
trap 'rm -f "$key"' EXIT
openssl genpkey -algorithm ed25519 -out "$key"
pub="$(openssl pkey -in "$key" -pubout | grep -v -- '-----')"
sed -i "s|^RELEASE_PUBKEY=\"\${VYNEL_RELEASE_PUBKEY:-[^}]*}\"|RELEASE_PUBKEY=\"\${VYNEL_RELEASE_PUBKEY:-$pub}\"|" scripts/install.sh
grep -q "$pub" scripts/install.sh || { echo "could not write the public key into scripts/install.sh" >&2; exit 1; }
echo "1. GitHub → repository Settings → Secrets and variables → Actions → New repository secret"
echo "   name: RELEASE_SIGNING_KEY, value (all lines):"
echo
cat "$key"
echo
echo "2. Commit scripts/install.sh (the public key is in it now) and push."
echo "3. Keep the private key above somewhere safe offline, or just in the secret: a lost key"
echo "   means running this script again."
