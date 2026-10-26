#!/usr/bin/env bash
# Checks of the installer that need no root and no network (run by ci.yml):
#   - the scripts parse;
#   - which release the installer follows for every combination of --channel / --ref / a copy
#     published by CI (BUILT_RELEASE) / VYNEL_RELEASE;
#   - scripts/release-assets.sh bakes the release into install.sh and writes consistent checksums.
set -euo pipefail
cd "$(dirname "$0")/.."

fail=0
bad() { echo "FAIL: $*" >&2; fail=1; }

for f in scripts/install.sh scripts/install-aio.sh scripts/release-assets.sh scripts/code-version.sh scripts/release-key.sh; do
  bash -n "$f" || bad "$f does not parse"
done
if command -v shellcheck >/dev/null; then
  shellcheck -S error scripts/release-assets.sh scripts/test-install.sh || bad "shellcheck"
fi

# ---- which release is followed ----
# shellcheck disable=SC2016 # the sed pattern names $CHANNEL literally
block="$(sed -n '/^case "\$CHANNEL" in/,/^RELEASE_URL=/p' scripts/install.sh)"
[[ -n "$block" ]] || { echo "FAIL: the channel block is not in install.sh" >&2; exit 1; }

# The block runs through eval, so shellcheck sees neither its die nor its inputs used.
# shellcheck disable=SC2034,SC2329
resolve() { # CHANNEL REF_SET REF BUILT_RELEASE VYNEL_RELEASE VYNEL_RELEASE_URL -> "RELEASE URL"
  (
    die() { echo "die: $*"; exit 3; }
    CHANNEL="$1" REF_SET="$2" REF="$3" BUILT_RELEASE="$4"
    VYNEL_RELEASE="$5" VYNEL_RELEASE_URL="$6"
    eval "$block"
    echo "$RELEASE $RELEASE_URL"
  )
}
expect() { # description, expected, got
  [[ "$2" == "$3" ]] || bad "$1: got '$3', want '$2'"
}
base=https://github.com/vyto4ka/vynel/releases
#      channel  ref_set ref         built        env-release env-url
expect "plain tree copy installs the latest release" "latest $base/latest/download" "$(resolve "" 0 main "" "" "")"
expect "a stable release's installer" "latest $base/latest/download" "$(resolve "" 0 main latest "" "")"
expect "a dev build's installer follows its branch" "edge-claude-x $base/download/edge-claude-x" "$(resolve dev 1 claude/x edge-claude-x "" "")"
expect "dev build's installer without flags" "edge-claude-x $base/download/edge-claude-x" "$(resolve "" 0 claude/x edge-claude-x "" "")"
expect "--channel dev on the stable installer" "edge-main $base/download/edge-main" "$(resolve dev 0 main latest "" "")"
expect "--channel dev --ref" "edge-feat-y $base/download/edge-feat-y" "$(resolve dev 1 feat/y latest "" "")"
expect "--ref alone means dev (as before channels)" "edge-feat-y $base/download/edge-feat-y" "$(resolve "" 1 feat/y "" "" "")"
expect "--channel stable overrides a dev build" "latest $base/latest/download" "$(resolve stable 0 main edge-claude-x "" "")"
expect "update passes VYNEL_RELEASE" "edge-main $base/download/edge-main" "$(resolve "" 1 main latest edge-main "")"
expect "a pinned tag" "v1.2.3 $base/download/v1.2.3" "$(resolve "" 0 main "" v1.2.3 "")"
expect "a mirror URL" "latest https://mirror.example/r" "$(resolve "" 0 main "" "" https://mirror.example/r)"
expect "an unknown channel" "die: unknown channel 'nightly' (stable or dev)" "$(resolve nightly 0 main "" "" "" || true)"

# ---- release-assets.sh ----
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
printf amd >"$tmp/vynel-linux-amd64"; printf arm >"$tmp/vynel-linux-arm64"
scripts/release-assets.sh "$tmp" v9.9.9 latest main >/dev/null || bad "release-assets.sh failed"
grep -q '^BUILT_RELEASE="latest"$' "$tmp/install.sh" || bad "BUILT_RELEASE not baked"
[[ "$(cat "$tmp/VERSION")" == v9.9.9 ]] || bad "VERSION"
(cd "$tmp" && sha256sum -c --quiet SHA256SUMS) || bad "SHA256SUMS does not match the files"
grep -q ' install.sh$' "$tmp/SHA256SUMS" || bad "install.sh is not in SHA256SUMS"
# The baked copy differs from the tree only in the two lines.
n="$(diff scripts/install.sh "$tmp/install.sh" | grep -c '^[<>]' || true)"
[[ "$n" == 4 ]] || bad "baked installer differs in $n lines, want 4"
scripts/release-assets.sh "$tmp" 'x;y' latest >/dev/null 2>&1 && bad "a bad version was accepted"

# ---- the baked installer says where it follows ----
# shellcheck disable=SC2016 # the pattern names ${VYNEL_REF literally
sed -n '/^BUILT_RELEASE=/,/^\[\[ -n "\${VYNEL_REF/p' "$tmp/install.sh" | grep -q 'BUILT_REF' || bad "built values not read"

exit $fail
