#!/usr/bin/env bash
# Prepares what a release publishes next to the binaries: the installer, the checksums, the
# version and, when a signing key is given, the signature. Both release workflows use it
# (.github/workflows/release.yml for stable, edge.yml for dev builds).
#
#   scripts/release-assets.sh DIR VERSION BUILT_RELEASE [BUILT_REF]
#
#   DIR            holds vynel-linux-amd64 and vynel-linux-arm64 (make build-all); the files below
#                  are written next to them, and the binaries xz-compressed (vynel-linux-*.xz,
#                  a quarter of the size: what install.sh downloads when it can)
#   VERSION        what `vynel version` prints for these binaries; goes to DIR/VERSION
#   BUILT_RELEASE  the release this copy of install.sh follows: "latest" for the stable channel,
#                  "edge-<branch>" for a dev build, a tag for a pinned release
#   BUILT_REF      the branch (dev builds); default main
#
# DIR/install.sh is scripts/install.sh with BUILT_RELEASE and BUILT_REF filled in, so an installer
# downloaded from a release installs and later updates from that same release. The checksums
# cover it. With RELEASE_SIGNING_KEY (PEM, Ed25519) SHA256SUMS.signed is written too
# (docs/DEVELOPMENT.md, "Подписанные релизы").
set -euo pipefail

[[ $# -ge 3 ]] || { sed -n '2,/^set -/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//' >&2; exit 2; }
dir="$(cd "$1" && pwd)"
version="$2" built_release="$3" built_ref="${4:-main}"
root="$(cd "$(dirname "$0")/.." && pwd)"

# The values go into a sed replacement and into a shell assignment: nothing but plain names.
for v in "$version" "$built_release" "$built_ref"; do
  [[ "$v" =~ ^[A-Za-z0-9._/+-]{1,100}$ ]] || { echo "release-assets: bad value '$v'" >&2; exit 1; }
done
for f in vynel-linux-amd64 vynel-linux-arm64; do
  [[ -s "$dir/$f" ]] || { echo "release-assets: $dir/$f is missing (make build-all)" >&2; exit 1; }
done

sed -e "s|^BUILT_RELEASE=\"\"\$|BUILT_RELEASE=\"$built_release\"|" \
    -e "s|^BUILT_REF=\"\"\$|BUILT_REF=\"$built_ref\"|" "$root/scripts/install.sh" >"$dir/install.sh"
grep -q "^BUILT_RELEASE=\"$built_release\"\$" "$dir/install.sh" || { echo "release-assets: BUILT_RELEASE not found in install.sh" >&2; exit 1; }
grep -q "^BUILT_REF=\"$built_ref\"\$" "$dir/install.sh" || { echo "release-assets: BUILT_REF not found in install.sh" >&2; exit 1; }
bash -n "$dir/install.sh"
chmod 755 "$dir/install.sh"

printf '%s\n' "$version" >"$dir/VERSION"
for f in vynel-linux-amd64 vynel-linux-arm64; do
  xz -9 -T1 -c "$dir/$f" >"$dir/$f.xz"
done
# install.sh is in the checksums: `vynel update` runs it only when it matches them (and, with a
# key, the signature). The plain binaries stay for older installers and `install.sh --binary`.
(cd "$dir" && sha256sum vynel-linux-amd64 vynel-linux-arm64 vynel-linux-amd64.xz vynel-linux-arm64.xz install.sh >SHA256SUMS)
cat "$dir/SHA256SUMS"

key="${RELEASE_SIGNING_KEY:-}"
if [[ -z "$key" ]]; then
  echo "no RELEASE_SIGNING_KEY: the release is not signed (scripts/release-key.sh)"
  exit 0
fi
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
umask 077
printf '%s\n' "$key" >"$tmp/release.pem"
openssl pkeyutl -sign -rawin -inkey "$tmp/release.pem" -in "$dir/SHA256SUMS" -out "$tmp/sums.sig"
# SHA256SUMS.signed = the checksums plus "# ed25519 <signature>" in one file, uploaded in one go,
# so a downloader never sees checksums and a signature from different builds.
{ cat "$dir/SHA256SUMS"; printf '# ed25519 %s\n' "$(base64 -w0 "$tmp/sums.sig")"; } >"$dir/SHA256SUMS.signed"
# The key in scripts/install.sh must match, or every install would fail.
pub="$(grep -o 'VYNEL_RELEASE_PUBKEY:-[^}]*' "$root/scripts/install.sh" | cut -d- -f2- || true)"
if [[ -n "$pub" ]]; then
  printf -- '-----BEGIN PUBLIC KEY-----\n%s\n-----END PUBLIC KEY-----\n' "$pub" >"$tmp/release.pub"
  openssl pkeyutl -verify -pubin -inkey "$tmp/release.pub" -rawin -in "$dir/SHA256SUMS" -sigfile "$tmp/sums.sig"
fi
echo "signed: SHA256SUMS.signed"
