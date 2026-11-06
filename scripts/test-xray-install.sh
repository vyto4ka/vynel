#!/usr/bin/env bash
# Exercise the real Xray installer with fake release archives, no root or network.
set -euo pipefail
cd "$(dirname "$0")/.."
test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT
DATA="$test_root/panel" NODE_DATA="$test_root/node" XRAY_DIR="$test_root/xray" XRAY_BIN="$test_root/bin/xray"
mkdir -p "$DATA/node" "$NODE_DATA" "$XRAY_DIR" "$test_root/bin" "$test_root/archive"
printf '{}\n' >"$DATA/node/xray.json"
printf '{}\n' >"$NODE_DATA/xray.json"
cat >"$test_root/archive/xray" <<'EOF'
#!/usr/bin/env bash
if [[ "$1" == version ]]; then echo 'Xray 26.9.30'; exit 0; fi
[[ "$*" == 'run -test -config '* ]] || exit 2
! grep -q incompatible "$4"
EOF
chmod 755 "$test_root/archive/xray"
printf 'new assets' >"$test_root/archive/geoip.dat"
(cd "$test_root/archive" && zip -q "$test_root/release.zip" xray geoip.dat)
printf '#!/bin/sh\necho "Xray 26.10.31"\n' >"$XRAY_DIR/xray"
chmod 755 "$XRAY_DIR/xray"
printf 'old assets' >"$XRAY_DIR/geoip.dat"

# Only function definitions; never execute the installer's top level.
eval "$(sed -n '/^rollback_xray() {/,/^# install_nginx:/p' scripts/install.sh)"
die() { echo "error: $*" >&2; exit 1; }
red() { echo "$*"; }
green() { echo "$*"; }
info() { echo "$*"; }
gh() { echo "$1"; }
xray_digest_ok() { return 0; }
gh_latest() { echo unexpected-latest-lookup >>"$test_root/latest"; echo v26.9.30; }
fetch() { echo "$1" >"$test_root/url"; cp "$test_root/release.zip" "$2"; }

GOARCH=amd64 XRAY_VERSION=v26.9.30 XRAY_CHANGED=0
install_xray force >"$test_root/log" 2>&1 || { cat "$test_root/log"; exit 1; }
[[ "$("$XRAY_BIN" version)" == 'Xray 26.9.30' ]] || die 'pin was not installed'
[[ ! -f "$test_root/latest" ]] || die 'a pinned install asked for latest'
[[ "$(cat "$test_root/url")" == https://github.com/XTLS/Xray-core/releases/download/v26.9.30/Xray-linux-64.zip ]] || die 'wrong pinned URL'
[[ "$(cat "$XRAY_DIR/geoip.dat")" == 'new assets' ]] || die 'new assets not installed'
rollback_xray >"$test_root/log" 2>&1 || die 'rollback failed'
[[ "$("$XRAY_BIN" version)" == 'Xray 26.10.31' && "$(cat "$XRAY_DIR/geoip.dat")" == 'old assets' ]] || die 'rollback mixed releases'

expect_failure() {
  if (install_xray force) >"$test_root/log" 2>&1; then cat "$test_root/log"; die "$1 succeeded"; fi
  [[ "$("$XRAY_BIN" version)" == 'Xray 26.10.31' ]] || die "$1 replaced the installed binary"
  [[ "$(cat "$XRAY_DIR/geoip.dat")" == 'old assets' ]] || die "$1 replaced the assets"
}
printf 'incompatible\n' >"$NODE_DATA/xray.json"
expect_failure 'incompatible config'
printf '{}\n' >"$NODE_DATA/xray.json"
XRAY_VERSION=v1.8.24
expect_failure 'wrong release binary'
fetch() { return 1; }
expect_failure 'failed pinned download'

# Following latest retains the existing version when GitHub is temporarily unreachable.
XRAY_VERSION=""
install_xray force >"$test_root/log" 2>&1 || { cat "$test_root/log"; exit 1; }
[[ -f "$test_root/latest" && "$("$XRAY_BIN" version)" == 'Xray 26.10.31' ]] || die 'latest failure did not keep current Xray'
echo 'Xray installer checks passed'
