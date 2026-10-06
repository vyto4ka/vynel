#!/usr/bin/env bash
# All-in-one install: panel + node + Caddy on ONE server and ONE IP (docs/ALL_IN_ONE.md).
#
#   bash <(curl -fsSL https://raw.githubusercontent.com/vyto4ka/vpn/claude/magical-hamilton-9vnx7n/scripts/install-aio.sh) \
#        --domain nl.example.com [--sub-domain sub.example.com] [--email you@example.com]
#
# Re-running it updates the code and keeps the data. Until release binaries exist (roadmap
# stage 9) it builds from source, so the first run takes a few minutes.
set -euo pipefail

REPO="${VPN_REPO:-https://github.com/vyto4ka/vpn.git}"
REF="${VPN_REF:-claude/magical-hamilton-9vnx7n}"
SRC=/opt/vpn-src
DATA=/var/lib/vpn
UNIT=/etc/systemd/system/vpn-panel.service

DOMAIN="" SUB_DOMAIN="" EMAIL="" NAME="" COUNTRY="" PUBLIC_IP="" GATEWAY_LISTEN=":9443"
ASSUME_YES=0 UNINSTALL=0 PURGE=0

red() { printf '\033[31m%s\033[0m\n' "$*"; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
info() { printf '\033[36m==>\033[0m %s\n' "$*"; }
die() { red "error: $*" >&2; exit 1; }

usage() {
  cat <<EOF
Usage: install-aio.sh --domain DOMAIN [options]

  --domain DOMAIN        node domain, A record -> this server (required)
  --sub-domain DOMAIN    subscription domain (default: the node domain; one domain is enough)
  --email EMAIL          email for Let's Encrypt (optional)
  --name NAME            server name shown in VPN clients (default: country name or "VPN")
  --country CC           2-letter country code for the flag (default: detected)
  --ip IP                public IP (default: detected)
  --gateway-listen ADDR  port for additional nodes (default :9443; 127.0.0.1:9443 = none)
  --yes                  do not ask questions
  --uninstall [--purge]  remove the service (--purge also deletes all data)
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --domain) DOMAIN="${2:-}"; shift 2 ;;
    --sub-domain) SUB_DOMAIN="${2:-}"; shift 2 ;;
    --email) EMAIL="${2:-}"; shift 2 ;;
    --name) NAME="${2:-}"; shift 2 ;;
    --country) COUNTRY="${2:-}"; shift 2 ;;
    --ip) PUBLIC_IP="${2:-}"; shift 2 ;;
    --gateway-listen) GATEWAY_LISTEN="${2:-}"; shift 2 ;;
    --yes|-y) ASSUME_YES=1; shift ;;
    --uninstall) UNINSTALL=1; shift ;;
    --purge) PURGE=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage; die "unknown option $1" ;;
  esac
done

[[ $EUID -eq 0 ]] || die "run as root"

confirm() {
  [[ $ASSUME_YES -eq 1 ]] && return 0
  [[ -t 0 || -e /dev/tty ]] || return 0
  local answer
  read -r -p "$1 [y/N] " answer </dev/tty || true
  [[ "$answer" =~ ^[YyДд] ]]
}

if [[ $UNINSTALL -eq 1 ]]; then
  info "removing vpn-panel service"
  systemctl disable --now vpn-panel 2>/dev/null || true
  rm -f "$UNIT"
  systemctl daemon-reload
  if [[ $PURGE -eq 1 ]]; then
    confirm "delete $DATA (users, keys, certificates)?" || die "aborted"
    rm -rf "$DATA" /etc/sysctl.d/90-vpn.conf
  fi
  green "removed"
  exit 0
fi

[[ -n "$DOMAIN" ]] || { usage; die "--domain is required"; }
SUB_DOMAIN="${SUB_DOMAIN:-$DOMAIN}"

# ---- system checks ----
command -v apt-get >/dev/null || die "only Debian/Ubuntu are supported"
case "$(uname -m)" in
  x86_64) GOARCH=amd64 ;;
  aarch64|arm64) GOARCH=arm64 ;;
  *) die "unsupported architecture $(uname -m)" ;;
esac

info "installing packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq git curl unzip ca-certificates iproute2 >/dev/null

if [[ -z "$PUBLIC_IP" ]]; then
  PUBLIC_IP="$(curl -4 -fsS --max-time 10 https://api.ipify.org || true)"
  [[ -n "$PUBLIC_IP" ]] || PUBLIC_IP="$(ip -4 route get 1.1.1.1 | awk '{for (i=1;i<NF;i++) if ($i=="src") print $(i+1)}')"
fi
[[ -n "$PUBLIC_IP" ]] || die "cannot detect the public IP, pass --ip"
if [[ -z "$COUNTRY" ]]; then
  COUNTRY="$(curl -fsS --max-time 5 "https://ipinfo.io/${PUBLIC_IP}/country" 2>/dev/null | tr -d '[:space:]' || true)"
  [[ "$COUNTRY" =~ ^[A-Za-z]{2}$ ]] || COUNTRY=""
fi
NAME="${NAME:-VPN}"
info "server $PUBLIC_IP, country ${COUNTRY:-?}, domain $DOMAIN, subscriptions on $SUB_DOMAIN"

domains=("$DOMAIN")
[[ "$SUB_DOMAIN" != "$DOMAIN" ]] && domains+=("$SUB_DOMAIN")
for d in "${domains[@]}"; do
  resolved="$(getent ahostsv4 "$d" | awk 'NR==1 {print $1}' || true)"
  if [[ "$resolved" != "$PUBLIC_IP" ]]; then
    red "DNS: $d -> ${resolved:-nothing}, expected $PUBLIC_IP"
    red "     create an A record $d -> $PUBLIC_IP, otherwise the certificate cannot be issued"
    confirm "continue anyway?" || die "aborted"
  fi
done

# Ports 80/443 must be free (except for our own service when updating).
systemctl stop vpn-panel 2>/dev/null || true
busy="$(ss -Hltnp '( sport = :443 or sport = :80 )' 2>/dev/null || true)"
if [[ -n "$busy" ]]; then
  red "ports 80/443 are in use:"
  echo "$busy"
  die "stop the web server / old VPN using them (e.g. systemctl disable --now nginx) and run again"
fi

# ---- Go toolchain (until release binaries exist) ----
need_go=1
if command -v go >/dev/null; then
  v="$(go env GOVERSION 2>/dev/null | sed 's/go//')"
  [[ "$(printf '%s\n1.21\n' "$v" | sort -V | head -1)" == "1.21" ]] && need_go=0
fi
if [[ $need_go -eq 1 && ! -x /usr/local/go/bin/go ]]; then
  GOV="$(curl -fsSL 'https://go.dev/VERSION?m=text' | head -1)"
  info "installing $GOV"
  curl -fsSL "https://go.dev/dl/${GOV}.linux-${GOARCH}.tar.gz" | tar -C /usr/local -xz
fi
export PATH="/usr/local/go/bin:$PATH"
export GOTOOLCHAIN=auto GOFLAGS=-mod=mod

# ---- source and binaries ----
info "fetching source ($REF)"
if [[ -d "$SRC/.git" ]]; then
  git -C "$SRC" fetch -q --depth 1 origin "$REF"
  git -C "$SRC" reset -q --hard FETCH_HEAD
else
  rm -rf "$SRC"
  git clone -q --depth 1 -b "$REF" "$REPO" "$SRC"
fi
COMMIT="$(git -C "$SRC" rev-parse --short HEAD)"

info "building vpn ($COMMIT)"
(cd "$SRC" && CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X github.com/vyto4ka/vpn/internal/buildinfo.Version=$COMMIT -X github.com/vyto4ka/vpn/internal/buildinfo.Commit=$COMMIT" \
  -o /usr/local/bin/vpn.new ./cmd/vpn)
mv /usr/local/bin/vpn.new /usr/local/bin/vpn

if [[ ! -x /usr/local/share/xray/xray ]]; then
  info "installing Xray"
  "$SRC/scripts/fetch-xray.sh" /usr/local/share/xray
fi
ln -sf /usr/local/share/xray/xray /usr/local/bin/xray

if [[ ! -x /usr/local/bin/caddy ]]; then
  info "installing Caddy"
  CV="$(curl -fsSL https://api.github.com/repos/caddyserver/caddy/releases/latest 2>/dev/null | grep -o '"tag_name": *"v[^"]*"' | grep -o 'v[0-9.]*' || true)"
  if [[ -n "$CV" ]] && curl -fsSL "https://github.com/caddyserver/caddy/releases/download/${CV}/caddy_${CV#v}_linux_${GOARCH}.tar.gz" | tar -xz -C /usr/local/bin caddy; then
    :
  else
    info "GitHub download failed, building Caddy from source"
    GOBIN=/usr/local/bin go install github.com/caddyserver/caddy/v2/cmd/caddy@latest
  fi
fi
chmod +x /usr/local/bin/caddy

# ---- firewall ----
if command -v ufw >/dev/null && ufw status | grep -q "Status: active"; then
  info "opening ports in ufw"
  ufw allow 80/tcp >/dev/null; ufw allow 443/tcp >/dev/null
  [[ "$GATEWAY_LISTEN" == 127.0.0.1:* ]] || ufw allow "${GATEWAY_LISTEN##*:}/tcp" >/dev/null
fi

# ---- configuration ----
info "configuring the panel"
setup_args=(--domain "$DOMAIN" --sub-domain "$SUB_DOMAIN" --name "$NAME")
[[ -n "$COUNTRY" ]] && setup_args+=(--country "$COUNTRY")
[[ -n "$EMAIL" ]] && setup_args+=(--email "$EMAIL")
vpn admin --data-dir "$DATA" setup "${setup_args[@]}"

cat >"$UNIT" <<EOF
[Unit]
Description=VPN panel with a local node (Xray + Caddy)
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/vpn panel --data-dir $DATA --gateway-listen $GATEWAY_LISTEN --public-addr $PUBLIC_IP:${GATEWAY_LISTEN##*:} --with-node
Restart=always
RestartSec=3
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable --now vpn-panel >/dev/null 2>&1
systemctl restart vpn-panel

info "waiting for the node to start"
ok=0
for _ in $(seq 1 60); do
  if vpn admin --data-dir "$DATA" node list 2>/dev/null | grep -q "in sync"; then ok=1; break; fi
  sleep 2
done
vpn admin --data-dir "$DATA" node list || true
[[ $ok -eq 1 ]] || red "the node is not in sync yet: journalctl -u vpn-panel -e"

info "waiting for the certificate of $SUB_DOMAIN"
cert=0
for _ in $(seq 1 45); do
  code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 --resolve "$SUB_DOMAIN:443:$PUBLIC_IP" "https://$SUB_DOMAIN/" || true)"
  if [[ "$code" == "200" ]]; then cert=1; break; fi
  sleep 2
done
[[ $cert -eq 1 ]] || red "https://$SUB_DOMAIN/ does not answer yet (DNS or port 80 not reachable?). Caddy keeps retrying; see journalctl -u vpn-panel"

green "done"
cat <<EOF

  Create a user:        vpn admin user add vasya
  Subscription link:    vpn admin user show vasya      (open it on the phone or import into Happ / v2RayTun)
  Users and traffic:    vpn admin user list ; vpn admin stats
  Logs:                 journalctl -u vpn-panel -f
  Update:               run this script again
EOF
