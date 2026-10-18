#!/usr/bin/env bash
# Downloads the Xray release used by integration tests and the dev environment.
set -euo pipefail
dir="${1:-.cache/xray}"
version="${XRAY_VERSION:-latest}"
case "$(uname -m)" in
  x86_64) asset=Xray-linux-64.zip ;;
  aarch64|arm64) asset=Xray-linux-arm64-v8a.zip ;;
  *) echo "unsupported arch $(uname -m)" >&2; exit 1 ;;
esac
if [[ -x "$dir/xray" ]]; then exit 0; fi
if [[ "$version" == latest ]]; then
  url="https://github.com/XTLS/Xray-core/releases/latest/download/$asset"
else
  url="https://github.com/XTLS/Xray-core/releases/download/$version/$asset"
fi
mkdir -p "$dir"
tmp="$(mktemp)"
curl -fsSL -o "$tmp" "$url"
unzip -o -q "$tmp" -d "$dir"
rm -f "$tmp"
chmod +x "$dir/xray"
"$dir/xray" version | head -1
