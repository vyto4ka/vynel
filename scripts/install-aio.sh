#!/usr/bin/env bash
# Kept for old links: the installer is scripts/install.sh now (menu: all-in-one, panel, node,
# update, uninstall). Flags are passed through; --domain alone still means all-in-one.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" 2>/dev/null && pwd || true)"
if [[ -n "$here" && -f "$here/install.sh" ]]; then
  exec bash "$here/install.sh" "$@"
fi
REF="${VYNEL_REF:-claude/magical-hamilton-9vnx7n}"
exec bash <(curl -fsSL "https://raw.githubusercontent.com/vyto4ka/vynel/$REF/scripts/install.sh") "$@"
