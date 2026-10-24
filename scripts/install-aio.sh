#!/usr/bin/env bash
# Kept for old links: the installer is scripts/install.sh now (menu: all-in-one, panel, node,
# update, uninstall). Flags are passed through; --domain alone still means all-in-one.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" 2>/dev/null && pwd || true)"
if [[ -n "$here" && -f "$here/install.sh" ]]; then
  exec bash "$here/install.sh" "$@"
fi
# The installer comes from a release (the latest one; VYNEL_RELEASE=edge-BRANCH for a dev build).
RELEASE="${VYNEL_RELEASE:-latest}"
if [[ "$RELEASE" == latest ]]; then
  url="https://github.com/vyto4ka/vynel/releases/latest/download/install.sh"
else
  url="https://github.com/vyto4ka/vynel/releases/download/$RELEASE/install.sh"
fi
exec bash <(curl -fsSL "$url") "$@"
