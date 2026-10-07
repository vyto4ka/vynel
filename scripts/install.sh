#!/usr/bin/env bash
# vynel installer: one script for every role (docs/INSTALL_GUIDE.md).
#
#   bash <(curl -fsSL https://raw.githubusercontent.com/vyto4ka/vynel/claude/magical-hamilton-9vnx7n/scripts/install.sh)
#
# Without arguments it shows a menu: everything on one server, panel only, node only, update,
# uninstall. Every choice can also run unattended:
#
#   install.sh --mode aio   --domain nl.example.com [--email you@example.com] [--admin-login boss] --yes
#   install.sh --mode panel --domain panel.example.com [--email ...] --yes
#   install.sh --mode node  --token vyn1.…            (the command is shown in the panel: Ноды → + Нода)
#   install.sh --mode update
#   install.sh --mode uninstall [--purge] [--yes]
#
# Binaries come from the branch's "edge" release built by CI; without one vynel is built from
# source (slow on small servers). Updates keep all data.
set -euo pipefail

REPO="${VYNEL_REPO:-https://github.com/vyto4ka/vynel.git}"
REF="${VYNEL_REF:-claude/magical-hamilton-9vnx7n}"
SRC=/opt/vynel-src
DATA=/var/lib/vynel
NODE_DATA=/var/lib/vynel-node
PANEL_UNIT=/etc/systemd/system/vynel.service
NODE_UNIT=/etc/systemd/system/vynel-node.service

MODE="" DOMAIN="" SUB_DOMAIN="" EMAIL="" NAME="" COUNTRY="" PUBLIC_IP="" GATEWAY_LISTEN=":9443"
ADMIN_LOGIN="admin" TOKEN="" BOT_TOKEN="" RESTORE="" ASSUME_YES=0 PURGE=0 WIZARD=0

red() { printf '\033[31m%s\033[0m\n' "$*"; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
pink() { printf '\033[35m%s\033[0m\n' "$*"; }
info() { printf '\033[36m==>\033[0m %s\n' "$*"; }
die() { red "error: $*" >&2; exit 1; }

usage() {
  cat <<EOF
Usage: install.sh                               (menu)
       install.sh --mode MODE [options]

Modes:
  aio        panel + VPN node on this server (one IP, one domain is enough)
  panel      panel only; VPN nodes are other servers
  node       VPN node for an existing panel (needs --token from the panel)
  update     update everything installed here, keep the data
  uninstall  remove the services (--purge also deletes all data and binaries)

Options:
  --domain DOMAIN        aio: node domain; panel: panel and subscription domain (A record -> this server)
  --sub-domain DOMAIN    aio: separate subscription domain (optional)
  --email EMAIL          email for Let's Encrypt (optional)
  --name NAME            aio: server name shown in VPN clients
  --country CC           aio: 2-letter country code for the flag (default: detected)
  --ip IP                public IP (default: detected)
  --gateway-listen ADDR  port for additional nodes (default :9443; 127.0.0.1:9443 = none)
  --admin-login LOGIN    web panel login (default admin; the password is generated)
  --token TOKEN          node: join token from the panel
  --bot-token TOKEN      aio/panel: Telegram bot token from @BotFather (optional)
  --restore FILE         aio/panel: restore users, nodes and keys from a backup (.tar.gz)
  --ref REF              git branch whose release to install (default $REF)
  --purge                uninstall: also delete data and binaries
  --yes                  do not ask questions
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --mode) MODE="${2:-}"; shift 2 ;;
    --domain) DOMAIN="${2:-}"; shift 2 ;;
    --sub-domain) SUB_DOMAIN="${2:-}"; shift 2 ;;
    --email) EMAIL="${2:-}"; shift 2 ;;
    --name) NAME="${2:-}"; shift 2 ;;
    --country) COUNTRY="${2:-}"; shift 2 ;;
    --ip) PUBLIC_IP="${2:-}"; shift 2 ;;
    --gateway-listen) GATEWAY_LISTEN="${2:-}"; shift 2 ;;
    --admin-login) ADMIN_LOGIN="${2:-}"; shift 2 ;;
    --bot-token) BOT_TOKEN="${2:-}"; shift 2 ;;
    --restore) RESTORE="${2:-}"; shift 2 ;;
    --token) TOKEN="${2:-}"; shift 2 ;;
    --ref) REF="${2:-}"; shift 2 ;;
    --yes|-y) ASSUME_YES=1; shift ;;
    --interactive|-i) WIZARD=1; shift ;;
    --uninstall) MODE=uninstall; shift ;;
    --update) MODE=update; shift ;;
    --purge) PURGE=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage; die "unknown option $1" ;;
  esac
done

[[ $EUID -eq 0 ]] || die "run as root"

# ---- terminal helpers ----

have_tty() { [[ -r /dev/tty ]] && { : </dev/tty; } 2>/dev/null; }

# ask VAR "question" "default"
ask() {
  local answer="" prompt="$2"
  [[ -n "$3" ]] && prompt+=" [$3]"
  read -r -p "  $prompt: " answer </dev/tty || true
  printf -v "$1" '%s' "${answer:-$3}"
}

# ask_yes "question" y|n
ask_yes() {
  local answer="" hint="y/N"
  [[ "$2" == y ]] && hint="Y/n"
  read -r -p "  $1 [$hint]: " answer </dev/tty || true
  answer="${answer:-$2}"
  [[ "$answer" =~ ^[YyДд] ]]
}

# confirm "question": yes only on an explicit answer; without a terminal only with --yes.
confirm() {
  [[ $ASSUME_YES -eq 1 ]] && return 0
  local answer=""
  have_tty || { red "no terminal to ask: add --yes"; return 1; }
  printf '  %s [y/N] ' "$1"
  read -r answer </dev/tty || true
  [[ "$answer" =~ ^[YyДд] ]]
}

resolve() { getent ahostsv4 "$1" | awk 'NR==1 {print $1}' || true; }

# ask_domain VAR "question" "default" -> asks until the A record points here or the user accepts
ask_domain() {
  local d r
  while true; do
    ask d "$2" "$3"
    d="$(printf '%s' "$d" | tr '[:upper:]' '[:lower:]' | sed 's#^https\?://##; s#/.*##')"
    if [[ ! "$d" =~ ^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$ ]]; then
      red "  «$d» не похоже на домен"
      continue
    fi
    r="$(resolve "$d")"
    if [[ "$r" == "$PUBLIC_IP" ]]; then
      green "  ✓ $d → $PUBLIC_IP"
      break
    fi
    red "  ✗ $d → ${r:-ничего}, а нужно $PUBLIC_IP. Создайте A-запись $d → $PUBLIC_IP."
    ask_yes "Продолжить всё равно (сертификат не выпустится, пока DNS не поправлен)?" n && break
  done
  printf -v "$1" '%s' "$d"
}

# Pads by characters, not bytes (Cyrillic is two bytes per letter in UTF-8).
line() {
  local n pad
  n="$(printf '%s' "$1" | LC_ALL=C.UTF-8 wc -m 2>/dev/null || printf '%s' "$1" | wc -c)"
  pad=$(( 26 - n )); (( pad < 1 )) && pad=1
  printf '  %s%*s%s\n' "$1" "$pad" "" "$2"
}

country_name() {
  case "$1" in
    NL) echo "Нидерланды" ;; DE) echo "Германия" ;; FI) echo "Финляндия" ;; SE) echo "Швеция" ;;
    FR) echo "Франция" ;; GB) echo "Великобритания" ;; US) echo "США" ;; CA) echo "Канада" ;;
    PL) echo "Польша" ;; LV) echo "Латвия" ;; LT) echo "Литва" ;; EE) echo "Эстония" ;;
    AT) echo "Австрия" ;; CH) echo "Швейцария" ;; CZ) echo "Чехия" ;; ES) echo "Испания" ;;
    IT) echo "Италия" ;; RO) echo "Румыния" ;; BG) echo "Болгария" ;; HU) echo "Венгрия" ;;
    RS) echo "Сербия" ;; MD) echo "Молдова" ;; TR) echo "Турция" ;; KZ) echo "Казахстан" ;;
    AM) echo "Армения" ;; GE) echo "Грузия" ;; AE) echo "ОАЭ" ;; JP) echo "Япония" ;;
    SG) echo "Сингапур" ;; HK) echo "Гонконг" ;; RU) echo "Россия" ;; UA) echo "Украина" ;;
    *) echo "VPN" ;;
  esac
}

# ---- what is installed here ----

PANEL_INSTALLED=0 PANEL_WITH_NODE=0 NODE_INSTALLED=0
[[ -f "$PANEL_UNIT" ]] && PANEL_INSTALLED=1
[[ $PANEL_INSTALLED -eq 1 ]] && grep -q -- "--with-node" "$PANEL_UNIT" && PANEL_WITH_NODE=1
[[ -f "$NODE_UNIT" ]] && NODE_INSTALLED=1

installed_text() {
  if [[ $PANEL_WITH_NODE -eq 1 ]]; then echo "панель + нода (all-in-one)"
  elif [[ $PANEL_INSTALLED -eq 1 ]]; then echo "панель"
  elif [[ $NODE_INSTALLED -eq 1 ]]; then echo "нода"
  else echo "ничего"
  fi
}

# Back-compatible guesses for flag-only runs.
if [[ -z "$MODE" ]]; then
  if [[ -n "$TOKEN" ]]; then MODE=node
  elif [[ -n "$DOMAIN" ]]; then MODE=aio
  fi
fi

if [[ -z "$MODE" ]]; then
  have_tty || { usage; die "--mode is required when there is no terminal to ask questions"; }
  echo
  pink "  vynel — установка"
  echo "  На этом сервере сейчас: $(installed_text)"
  echo
  echo "  1) Всё на этом сервере: панель + VPN-нода   (проще всего начать с этого)"
  echo "  2) Только панель (VPN-ноды будут на других серверах)"
  echo "  3) Только VPN-нода для существующей панели  (нужен токен из панели)"
  echo "  4) Обновить установленное"
  echo "  5) Удалить"
  echo
  def=1 choice=""
  [[ $PANEL_INSTALLED -eq 1 || $NODE_INSTALLED -eq 1 ]] && def=4
  ask choice "Выберите" "$def"
  case "$choice" in
    1) MODE=aio ;; 2) MODE=panel ;; 3) MODE=node ;; 4) MODE=update ;; 5) MODE=uninstall ;;
    *) die "нет такого пункта: $choice" ;;
  esac
  WIZARD=1
fi
case "$MODE" in aio|panel|node|update|uninstall) ;; *) usage; die "unknown mode $MODE" ;; esac

# ---- uninstall ----

if [[ "$MODE" == uninstall ]]; then
  [[ $PANEL_INSTALLED -eq 1 || $NODE_INSTALLED -eq 1 ]] || { green "nothing is installed"; exit 0; }
  echo "  installed: $(installed_text)"
  if [[ $WIZARD -eq 1 && $PURGE -eq 0 ]] && ask_yes "Удалить и все данные (пользователи, ключи, сертификаты)?" n; then
    PURGE=1
  fi
  if [[ $PURGE -eq 1 ]]; then
    confirm "delete $DATA and $NODE_DATA (users, keys, certificates) and the binaries?" || die "aborted"
  elif [[ $WIZARD -eq 1 ]]; then
    confirm "remove the services (data stays in $DATA)?" || die "aborted"
  fi
  for unit in vynel vynel-node; do
    systemctl disable --now "$unit" 2>/dev/null || true
  done
  rm -f "$PANEL_UNIT" "$NODE_UNIT"
  systemctl daemon-reload
  if [[ $PURGE -eq 1 ]]; then
    rm -rf "$DATA" "$NODE_DATA" /etc/sysctl.d/90-vynel.conf "$SRC" \
      /usr/local/bin/vynel /usr/local/bin/xray /usr/local/bin/caddy /usr/local/share/xray
  fi
  green "removed$([[ $PURGE -eq 1 ]] && echo " with all data")"
  exit 0
fi

# ---- system checks ----

command -v apt-get >/dev/null || die "only Debian/Ubuntu are supported"
case "$(uname -m)" in
  x86_64) GOARCH=amd64 ;;
  aarch64|arm64) GOARCH=arm64 ;;
  *) die "unsupported architecture $(uname -m)" ;;
esac

if [[ "$MODE" == update ]]; then
  [[ $PANEL_INSTALLED -eq 1 || $NODE_INSTALLED -eq 1 ]] || die "nothing to update: vynel is not installed here (run without --mode for the menu)"
fi
if [[ "$MODE" == node && $PANEL_INSTALLED -eq 1 ]]; then
  die "this server runs the panel (ports 80/443 are taken); a node needs another server — or choose all-in-one to run a node here"
fi
if [[ ( "$MODE" == aio || "$MODE" == panel ) && $NODE_INSTALLED -eq 1 ]]; then
  die "this server runs a node of another panel; remove it first (install.sh --mode uninstall)"
fi

info "installing packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq git curl unzip ca-certificates iproute2 >/dev/null

detect_ip() {
  [[ -n "$PUBLIC_IP" ]] && return 0
  PUBLIC_IP="$(curl -4 -fsS --max-time 10 https://api.ipify.org 2>/dev/null || true)"
  [[ -n "$PUBLIC_IP" ]] || PUBLIC_IP="$(ip -4 route get 1.1.1.1 | awk '{for (i=1;i<NF;i++) if ($i=="src") print $(i+1)}')"
  [[ -n "$PUBLIC_IP" ]] || die "cannot detect the public IP, pass --ip"
}

detect_country() {
  if [[ -z "$COUNTRY" ]]; then
    COUNTRY="$(curl -fsS --max-time 5 "https://ipinfo.io/${PUBLIC_IP}/country" 2>/dev/null | tr -d '[:space:]' || true)"
    [[ "$COUNTRY" =~ ^[A-Za-z]{2}$ ]] || COUNTRY=""
  fi
  COUNTRY="$(printf '%s' "$COUNTRY" | tr '[:lower:]' '[:upper:]')"
}

check_dns() {
  local d resolved
  for d in "$@"; do
    resolved="$(resolve "$d")"
    if [[ "$resolved" != "$PUBLIC_IP" && $WIZARD -eq 0 ]]; then
      red "DNS: $d -> ${resolved:-nothing}, expected $PUBLIC_IP"
      red "     create an A record $d -> $PUBLIC_IP, otherwise the certificate cannot be issued"
      confirm "continue anyway?" || die "aborted"
    fi
  done
}

# Ports 80/443 must be free (our own services are stopped first when updating).
check_ports() {
  if [[ "$MODE" == node ]]; then
    systemctl stop vynel-node 2>/dev/null || true
  else
    systemctl stop vynel 2>/dev/null || true
  fi
  local busy
  busy="$(ss -Hltnp '( sport = :443 or sport = :80 )' 2>/dev/null || true)"
  if [[ -n "$busy" ]]; then
    red "ports 80/443 are in use:"
    echo "$busy"
    die "stop the web server / old VPN using them (e.g. systemctl disable --now nginx) and run again"
  fi
}

# ---- binaries ----
# vynel: a prebuilt binary from the branch's "edge" release (built by .github/workflows/edge.yml).
# Building from source is the fallback only: on a 1 vCPU / 1 GB VPS it takes 10–20 minutes.
RELEASE="${VYNEL_RELEASE:-edge-${REF//\//-}}"
RELEASE_URL="https://github.com/vyto4ka/vynel/releases/download/$RELEASE"
TEMP_SWAP=0

ensure_go() {
  export PATH="/usr/local/go/bin:$PATH"
  export GOTOOLCHAIN=auto GOFLAGS=-mod=mod
  if command -v go >/dev/null; then
    local v
    v="$(go env GOVERSION 2>/dev/null | sed 's/go//')"
    [[ "$(printf '%s\n1.21\n' "$v" | sort -V | head -1)" == "1.21" ]] && return 0
  fi
  local gov
  gov="$(curl -fsSL 'https://go.dev/VERSION?m=text' | head -1)"
  info "installing $gov"
  rm -rf /usr/local/go
  curl -fsSL "https://go.dev/dl/${gov}.linux-${GOARCH}.tar.gz" | tar -C /usr/local -xz
}

# The Go compiler needs ~1.5 GB for this project; small VPSes get a temporary swap file.
ensure_memory() {
  local mem swap
  mem="$(awk '/MemTotal/ {print int($2/1024)}' /proc/meminfo)"
  swap="$(awk '/SwapTotal/ {print int($2/1024)}' /proc/meminfo)"
  if (( mem + swap < 2000 )) && [[ ! -e /swapfile-vynel ]]; then
    info "only ${mem} MB RAM: adding a 2 GB swap file for the build"
    fallocate -l 2G /swapfile-vynel 2>/dev/null || dd if=/dev/zero of=/swapfile-vynel bs=1M count=2048 status=none
    chmod 600 /swapfile-vynel
    mkswap /swapfile-vynel >/dev/null
    swapon /swapfile-vynel
    TEMP_SWAP=1
  fi
}

# with_progress "message" command...: prints a dot every 10 seconds so a long step does not look hung.
with_progress() {
  local msg="$1"; shift
  printf '\033[36m==>\033[0m %s ' "$msg"
  ( while true; do sleep 10; printf '.'; done ) &
  local dots=$! rc=0
  "$@" >/tmp/vynel-build.log 2>&1 || rc=$?
  kill "$dots" 2>/dev/null; wait "$dots" 2>/dev/null || true
  if [[ $rc -eq 0 ]]; then echo " ok"; else echo " failed"; tail -20 /tmp/vynel-build.log; fi
  return $rc
}

install_release_binary() {
  local tmp
  tmp="$(mktemp -d)"
  curl -fsSL --max-time 120 -o "$tmp/vynel-linux-$GOARCH" "$RELEASE_URL/vynel-linux-$GOARCH" || { rm -rf "$tmp"; return 1; }
  curl -fsSL --max-time 30 -o "$tmp/SHA256SUMS" "$RELEASE_URL/SHA256SUMS" || { rm -rf "$tmp"; return 1; }
  (cd "$tmp" && grep " vynel-linux-$GOARCH\$" SHA256SUMS | sha256sum -c --quiet -) || { red "checksum mismatch"; rm -rf "$tmp"; return 1; }
  install -m 755 "$tmp/vynel-linux-$GOARCH" /usr/local/bin/vynel.new
  mv /usr/local/bin/vynel.new /usr/local/bin/vynel
  rm -rf "$tmp"
}

build_from_source() {
  ensure_go
  ensure_memory
  info "fetching source ($REF)"
  if [[ -d "$SRC/.git" ]]; then
    git -C "$SRC" fetch -q --depth 1 origin "$REF"
    git -C "$SRC" reset -q --hard FETCH_HEAD
  else
    rm -rf "$SRC"
    git clone -q --depth 1 -b "$REF" "$REPO" "$SRC"
  fi
  local commit
  commit="$(git -C "$SRC" rev-parse --short HEAD)"
  with_progress "building vynel $commit from source (10–20 minutes on a small VPS)" \
    bash -c "cd '$SRC' && CGO_ENABLED=0 go build -trimpath -ldflags '-s -w -X github.com/vyto4ka/vynel/internal/buildinfo.Version=$commit -X github.com/vyto4ka/vynel/internal/buildinfo.Commit=$commit' -o /usr/local/bin/vynel.new ./cmd/vynel"
  mv /usr/local/bin/vynel.new /usr/local/bin/vynel
  red "  note: a source build has no web UI inside (it is built in CI); the panel will say so. Use the release when it is available."
}

install_vynel() {
  info "downloading vynel ($RELEASE)"
  # Right after a push the release is being re-created for about a minute: retry before building.
  local got=0 attempt
  for attempt in 1 2 3 4; do
    if install_release_binary 2>/dev/null; then got=1; break; fi
    [[ $attempt -lt 4 ]] && { echo "  release not available yet, retrying in 20s ($attempt/3)"; sleep 20; }
  done
  if [[ $got -eq 1 ]]; then
    green "  $(/usr/local/bin/vynel version)"
  else
    red "  no prebuilt binary for $RELEASE/$GOARCH, building from source"
    build_from_source
  fi
}

# install_xray [force]: latest Xray release; an update keeps the old one if the download fails.
install_xray() {
  if [[ -x /usr/local/share/xray/xray && "${1:-}" != force ]]; then
    ln -sf /usr/local/share/xray/xray /usr/local/bin/xray
    return 0
  fi
  info "installing Xray"
  local asset tmp
  case "$GOARCH" in amd64) asset=Xray-linux-64.zip ;; arm64) asset=Xray-linux-arm64-v8a.zip ;; esac
  tmp="$(mktemp)"
  if curl -fsSL -o "$tmp" "https://github.com/XTLS/Xray-core/releases/latest/download/$asset"; then
    mkdir -p /usr/local/share/xray
    unzip -o -q "$tmp" -d /usr/local/share/xray
    chmod +x /usr/local/share/xray/xray
  elif [[ -x /usr/local/share/xray/xray ]]; then
    red "  Xray download failed, keeping the installed one"
  else
    rm -f "$tmp"
    die "cannot download Xray"
  fi
  rm -f "$tmp"
  ln -sf /usr/local/share/xray/xray /usr/local/bin/xray
  green "  $(/usr/local/bin/xray version 2>/dev/null | head -1)"
}

# install_caddy [force]
install_caddy() {
  if [[ -x /usr/local/bin/caddy && "${1:-}" != force ]]; then
    return 0
  fi
  info "installing Caddy"
  local cv tmp
  tmp="$(mktemp -d)"
  cv="$(curl -fsSL https://api.github.com/repos/caddyserver/caddy/releases/latest 2>/dev/null | grep -o '"tag_name": *"v[^"]*"' | grep -o 'v[0-9.]*' || true)"
  if [[ -n "$cv" ]] && curl -fsSL "https://github.com/caddyserver/caddy/releases/download/${cv}/caddy_${cv#v}_linux_${GOARCH}.tar.gz" | tar -xz -C "$tmp" caddy; then
    install -m 755 "$tmp/caddy" /usr/local/bin/caddy
  elif [[ -x /usr/local/bin/caddy ]]; then
    red "  Caddy download failed, keeping the installed one"
  else
    red "  Caddy download failed, building it from source"
    ensure_go
    ensure_memory
    with_progress "building Caddy (several minutes)" env GOBIN=/usr/local/bin go install github.com/caddyserver/caddy/v2/cmd/caddy@latest
  fi
  rm -rf "$tmp"
  chmod +x /usr/local/bin/caddy
}

drop_temp_swap() {
  if [[ $TEMP_SWAP -eq 1 ]]; then
    swapoff /swapfile-vynel && rm -f /swapfile-vynel
  fi
}

# open_ports PORT...: only when ufw is active.
open_ports() {
  if command -v ufw >/dev/null && ufw status | grep -q "Status: active"; then
    info "opening ports in ufw: $*"
    local p
    for p in "$@"; do ufw allow "$p/tcp" >/dev/null; done
  fi
}

# The command the panel shows for new nodes (Ноды → + Нода).
script_command() {
  local cmd="bash <(curl -fsSL https://raw.githubusercontent.com/vyto4ka/vynel/$REF/scripts/install.sh)"
  printf '%s' "$cmd"
}

admin() { vynel admin --data-dir "$DATA" "$@"; }

write_panel_unit() {
  local extra="$1"
  cat >"$PANEL_UNIT" <<EOF
[Unit]
Description=vynel panel${extra:+ with a local node (Xray + Caddy)}
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/vynel panel --data-dir $DATA --gateway-listen $GATEWAY_LISTEN --public-addr $PUBLIC_IP:${GATEWAY_LISTEN##*:}$extra
Restart=always
RestartSec=3
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF
}

write_node_unit() {
  cat >"$NODE_UNIT" <<EOF
[Unit]
Description=vynel VPN node (Xray + Caddy)
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/vynel node run --data-dir $NODE_DATA
Restart=always
RestartSec=3
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF
}

start_unit() {
  systemctl daemon-reload
  systemctl enable --now "$1" >/dev/null 2>&1
  systemctl restart "$1"
}

# wait_cert DOMAIN -> sets CERT_OK
CERT_OK=0
wait_cert() {
  info "waiting for the certificate of $1"
  local code
  for _ in $(seq 1 45); do
    code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 --resolve "$1:443:$PUBLIC_IP" "https://$1/" || true)"
    if [[ "$code" == "200" ]]; then CERT_OK=1; return 0; fi
    sleep 2
  done
  red "https://$1/ does not answer yet (DNS or port 80 not reachable?). Caddy keeps retrying; see journalctl -u vynel"
}

# web_admin: creates the admin once and reads the panel address -> WEB_URL WEB_LOGIN WEB_PASSWORD
web_admin() {
  local out
  out="$(admin web init --login "$ADMIN_LOGIN")"
  WEB_URL="$(awk '$1=="url" {print $2}' <<<"$out")"
  WEB_LOGIN="$(awk '$1=="login" {print $2}' <<<"$out")"
  WEB_PASSWORD="$(awk '$1=="password" && NF==2 {print $2}' <<<"$out")"
}

# ---- Telegram bot and restore (aio, panel) ----

ask_bot_and_restore() {
  echo "  Telegram-бот: пользователи из чата, вход в панель без пароля, ночные бэкапы, сообщения о нодах."
  ask BOT_TOKEN "Токен бота от @BotFather (Enter — настроить потом)" "$BOT_TOKEN"
  if [[ -z "$RESTORE" && ! -f "$DATA/panel.db" ]] && ask_yes "Восстановить пользователей и ноды из бэкапа?" n; then
    ask RESTORE "Путь к файлу бэкапа (.tar.gz)" ""
  fi
}

# restore_backup: before the panel is configured, so setup adapts the restored data to this server.
RESTORED=""
restore_backup() {
  [[ -n "$RESTORE" ]] || return 0
  [[ -f "$RESTORE" ]] || die "backup file $RESTORE not found"
  info "restoring from $RESTORE"
  systemctl stop vynel 2>/dev/null || true
  RESTORED="$(vynel restore --data-dir "$DATA" --yes "$RESTORE" | head -1)"
  green "  $RESTORED"
}

# setup_bot: stores the token and makes a bind code -> BOT_CODE BOT_LINK
BOT_CODE="" BOT_LINK=""
setup_bot() {
  if [[ -n "$BOT_TOKEN" ]]; then
    admin bot token "$BOT_TOKEN" >/dev/null || { red "  the bot token was not accepted; set it later in the panel (Telegram)"; return 0; }
  fi
  admin bot 2>/dev/null | grep -q "^token    set" || return 0
  admin bot 2>/dev/null | grep -q "^admin " && return 0 # already bound (update or restore)
  BOT_CODE="$(admin bot code | awk '$1=="code" {print $2}')"
  local tok user
  tok="${BOT_TOKEN}"
  if [[ -n "$tok" ]]; then
    user="$(curl -fsS --max-time 10 "https://api.telegram.org/bot${tok}/getMe" 2>/dev/null | grep -o '"username":"[^"]*"' | cut -d'"' -f4 || true)"
    [[ -n "$user" ]] && BOT_LINK="https://t.me/${user}?start=${BOT_CODE}"
  fi
}

print_bot_block() {
  if [[ -n "$BOT_CODE" ]]; then
    pink "  Telegram-бот"
    if [[ -n "$BOT_LINK" ]]; then
      line "  привязать себя" "$BOT_LINK"
    fi
    line "  или отправьте боту" "/start $BOT_CODE"
    echo "  Код действует 15 минут; новый: vynel admin bot code (или в панели: Telegram)."
    echo
  elif ! admin bot 2>/dev/null | grep -q "^token    set"; then
    echo "  Telegram-бот не настроен: панель → «Telegram» (токен от @BotFather)."
    echo
  fi
  if [[ -n "$RESTORED" ]]; then
    pink "  Восстановлено из бэкапа"
    echo "  $RESTORED"
    echo "  Если у панели новый IP, на каждой ноде: vynel node set-panel ${PUBLIC_IP}:${GATEWAY_LISTEN##*:}"
    echo
  fi
}

print_web_block() {
  pink "  Веб-панель"
  line "  адрес" "$WEB_URL"
  line "  логин" "$WEB_LOGIN"
  if [[ -n "$WEB_PASSWORD" ]]; then
    line "  пароль" "$WEB_PASSWORD"
    red "  Сохраните пароль: он показывается один раз. Адрес секретный — без него панель не найти."
  else
    line "  пароль" "прежний (новый: vynel admin web password)"
  fi
}

print_common_commands() {
  echo "  Сервер"
  line "  адрес и логин панели" "vynel admin web"
  line "  новый пароль панели" "vynel admin web password"
  line "  состояние нод" "vynel admin node list"
  line "  все команды" "vynel admin --help"
  line "  логи" "journalctl -u vynel -f"
  line "  перезапуск" "systemctl restart vynel"
  line "  обновить / удалить" "запустить установщик ещё раз и выбрать пункт"
}

# ================================================================== aio
do_aio() {
  detect_ip
  detect_country
  if [[ $WIZARD -eq 1 ]]; then
    echo
    green "Всё на одном сервере: панель + VPN-нода"
    echo "  Enter — оставить значение в скобках."
    echo
    ask PUBLIC_IP "Публичный IP сервера" "$PUBLIC_IP"
    ask_domain DOMAIN "Домен сервера (A-запись на этот IP; на нём VPN, сайт-заглушка, подписки и панель)" "$DOMAIN"
    if ask_yes "Отдельный домен для подписок? (не обязательно, одного домена достаточно)" n; then
      ask_domain SUB_DOMAIN "Домен подписок" "$SUB_DOMAIN"
    else
      SUB_DOMAIN="$DOMAIN"
    fi
    ask EMAIL "Email для Let's Encrypt (можно пусто)" "$EMAIL"
    ask COUNTRY "Код страны сервера, для флага в клиентах" "$COUNTRY"
    COUNTRY="$(printf '%s' "$COUNTRY" | tr '[:lower:]' '[:upper:]')"
    [[ -z "$COUNTRY" || "$COUNTRY" =~ ^[A-Z]{2}$ ]] || die "код страны — две латинские буквы, например NL"
    ask NAME "Название сервера в клиентах" "${NAME:-$(country_name "$COUNTRY")}"
    if ask_yes "Подключать к этой панели другие серверы (ноды) в будущем? Откроет порт 9443" y; then
      GATEWAY_LISTEN=":9443"
    else
      GATEWAY_LISTEN="127.0.0.1:9443"
    fi
    ask ADMIN_LOGIN "Логин для веб-панели (пароль сгенерируется сам)" "$ADMIN_LOGIN"
    ask_bot_and_restore
    SUB_DOMAIN="${SUB_DOMAIN:-$DOMAIN}"
    echo
    echo "  Сервер:    $PUBLIC_IP"
    echo "  Домен:     $DOMAIN"
    echo "  Подписки:  https://$SUB_DOMAIN/s/…"
    echo "  Клиенты:   ${NAME} (${COUNTRY:-без флага})"
    echo "  Email:     ${EMAIL:-—}"
    echo "  Ноды:      $([[ "$GATEWAY_LISTEN" == 127.0.0.1:* ]] && echo "только этот сервер" || echo "порт 9443 открыт")"
    echo "  Панель:    логин $ADMIN_LOGIN, пароль будет показан в конце"
    echo
    ask_yes "Устанавливаем?" y || die "отменено"
  fi
  [[ -n "$DOMAIN" ]] || { usage; die "--domain is required"; }
  [[ "$ADMIN_LOGIN" =~ ^[A-Za-z0-9_.@-]{1,64}$ ]] || die "the login may contain latin letters, digits and _ . - @"
  SUB_DOMAIN="${SUB_DOMAIN:-$DOMAIN}"
  NAME="${NAME:-$(country_name "$COUNTRY")}"
  info "server $PUBLIC_IP, country ${COUNTRY:-?}, domain $DOMAIN, subscriptions on $SUB_DOMAIN"
  if [[ "$SUB_DOMAIN" != "$DOMAIN" ]]; then check_dns "$DOMAIN" "$SUB_DOMAIN"; else check_dns "$DOMAIN"; fi
  check_ports

  install_vynel
  install_xray
  install_caddy
  drop_temp_swap
  if [[ "$GATEWAY_LISTEN" == 127.0.0.1:* ]]; then open_ports 80 443; else open_ports 80 443 "${GATEWAY_LISTEN##*:}"; fi

  restore_backup
  info "configuring the panel"
  local setup_args=(--domain "$DOMAIN" --sub-domain "$SUB_DOMAIN" --name "$NAME")
  [[ -n "$COUNTRY" ]] && setup_args+=(--country "$COUNTRY")
  [[ -n "$EMAIL" ]] && setup_args+=(--email "$EMAIL")
  admin setup "${setup_args[@]}"
  admin setting install.command "$(script_command)" >/dev/null
  web_admin
  setup_bot

  write_panel_unit " --with-node"
  start_unit vynel

  info "waiting for the node to start"
  local ok=0
  for _ in $(seq 1 60); do
    if admin node list 2>/dev/null | grep -q "in sync"; then ok=1; break; fi
    sleep 2
  done
  admin node list || true
  [[ $ok -eq 1 ]] || red "the node is not in sync yet: journalctl -u vynel -e"
  wait_cert "$SUB_DOMAIN"

  local sub_prefix
  sub_prefix="$(admin setting sub.prefix 2>/dev/null || true)"
  echo
  pink "══════════════════════  vynel установлен: панель + нода  ══════════════════════"
  echo
  line "Версия" "$(vynel version 2>/dev/null | awk '{print $2}')"
  line "Сервер" "$PUBLIC_IP ($NAME${COUNTRY:+, $COUNTRY})"
  line "Нода (VPN)" "$DOMAIN:443 — $([[ $ok -eq 1 ]] && echo "работает" || echo "запускается — см. логи")"
  line "Сертификат" "$([[ $CERT_OK -eq 1 ]] && echo "выпущен" || echo "ещё нет — проверьте DNS и порт 80")"
  line "Сайт-заглушка" "https://$DOMAIN/"
  line "Подписки" "https://$SUB_DOMAIN${sub_prefix:-/s/}<токен>"
  line "Подключение других нод" "$([[ "$GATEWAY_LISTEN" == 127.0.0.1:* ]] && echo "закрыто (только этот сервер)" || echo "порт ${GATEWAY_LISTEN##*:} открыт")"
  line "Данные" "$DATA  (бэкап: скопировать папку)"
  echo
  print_web_block
  echo
  print_bot_block
  echo "  Пользователи — в панели: «Пользователи» → «+ Пользователь», ввести имя, скопировать ссылку."
  echo "  Внешний вид подписки и заголовки для приложений — в панели: «Подписка»."
  echo "  Из терминала:"
  line "  добавить" "vynel admin user add vasya"
  line "  ссылка подписки" "vynel admin user show vasya"
  line "  продлить" "vynel admin user extend vasya --months 1"
  line "  устройства (HWID)" "vynel admin user devices vasya [--rm ID]"
  line "  статистика" "vynel admin stats"
  echo
  print_common_commands
  echo
  echo "  Ссылку подписки откройте на телефоне — там QR-код и кнопки для приложений."
  echo "  Подробно: https://github.com/vyto4ka/vynel/blob/$REF/docs/INSTALL_GUIDE.md"
  echo
}

# ================================================================== panel
do_panel() {
  detect_ip
  if [[ $WIZARD -eq 1 ]]; then
    echo
    green "Только панель: пользователи, подписки и веб-панель. VPN-ноды — на других серверах."
    echo "  Enter — оставить значение в скобках."
    echo
    ask PUBLIC_IP "Публичный IP сервера" "$PUBLIC_IP"
    ask_domain DOMAIN "Домен панели и подписок (A-запись на этот IP)" "$DOMAIN"
    ask EMAIL "Email для Let's Encrypt (можно пусто)" "$EMAIL"
    ask ADMIN_LOGIN "Логин для веб-панели (пароль сгенерируется сам)" "$ADMIN_LOGIN"
    ask_bot_and_restore
    echo
    echo "  Сервер:    $PUBLIC_IP, ноды подключаются на порт ${GATEWAY_LISTEN##*:}"
    echo "  Домен:     $DOMAIN (панель по секретному пути, подписки, сайт-заглушка)"
    echo "  Email:     ${EMAIL:-—}"
    echo "  Панель:    логин $ADMIN_LOGIN, пароль будет показан в конце"
    echo
    ask_yes "Устанавливаем?" y || die "отменено"
  fi
  [[ -n "$DOMAIN" ]] || { usage; die "--domain is required"; }
  [[ "$ADMIN_LOGIN" =~ ^[A-Za-z0-9_.@-]{1,64}$ ]] || die "the login may contain latin letters, digits and _ . - @"
  [[ "$GATEWAY_LISTEN" == 127.0.0.1:* ]] && die "a panel without its own node needs the node port open (--gateway-listen :9443)"
  check_dns "$DOMAIN"
  check_ports

  install_vynel
  install_caddy
  drop_temp_swap
  open_ports 80 443 "${GATEWAY_LISTEN##*:}"

  restore_backup
  info "configuring the panel"
  admin setting sub.domain "$DOMAIN" >/dev/null
  [[ -n "$EMAIL" ]] && admin setting caddy.email "$EMAIL" >/dev/null
  admin setting install.command "$(script_command)" >/dev/null
  # A profile for the nodes to come: Reality self-steal, available to the default group.
  if ! admin profile list 2>/dev/null | awk 'NR>1 {print $2}' | grep -qx Reality; then
    admin profile add --name Reality >/dev/null
  fi
  web_admin
  setup_bot

  write_panel_unit ""
  start_unit vynel
  sleep 2
  wait_cert "$DOMAIN"

  echo
  pink "══════════════════════════  vynel установлен: панель  ══════════════════════════"
  echo
  line "Версия" "$(vynel version 2>/dev/null | awk '{print $2}')"
  line "Сервер" "$PUBLIC_IP"
  line "Сертификат" "$([[ $CERT_OK -eq 1 ]] && echo "выпущен" || echo "ещё нет — проверьте DNS и порт 80")"
  line "Сайт-заглушка" "https://$DOMAIN/"
  line "Подписки" "https://$DOMAIN/s/<токен>"
  line "Ноды подключаются" "$PUBLIC_IP:${GATEWAY_LISTEN##*:}"
  line "Данные" "$DATA  (бэкап: скопировать папку)"
  echo
  print_web_block
  echo
  print_bot_block
  pink "  Дальше: добавьте VPN-ноду"
  echo "  1. В панели: «Ноды» → «+ Нода» → название, страна, домен ноды (A-запись на IP нового сервера)."
  echo "  2. Панель покажет команду вида:"
  echo "       $(script_command) --mode node --token vyn1.…"
  echo "  3. Выполните её на новом сервере под root. Через минуту нода станет «работает»."
  echo "  4. «Пользователи» → «+ Пользователь» → скопировать ссылку подписки."
  echo
  print_common_commands
  echo
}

# ================================================================== node
do_node() {
  detect_ip
  local joined=0
  [[ -f "$NODE_DATA/node.crt" ]] && joined=1
  if [[ $WIZARD -eq 1 && -z "$TOKEN" && $joined -eq 0 ]]; then
    echo
    green "VPN-нода для существующей панели"
    echo "  Токен выдаёт панель: «Ноды» → «+ Нода» (или «Новый токен» у ноды)."
    echo
    ask TOKEN "Токен (vyn1.…)" ""
  fi
  TOKEN="$(printf '%s' "$TOKEN" | tr -d '[:space:]')"
  if [[ $joined -eq 0 ]]; then
    [[ "$TOKEN" == vyn1.* ]] || die "a join token from the panel is required (--token vyn1.…)"
  fi
  check_ports

  install_vynel
  install_xray
  install_caddy
  drop_temp_swap
  open_ports 80 443

  if [[ -n "$TOKEN" ]]; then
    info "joining the panel"
    if [[ $joined -eq 1 ]]; then
      rm -rf "$NODE_DATA"
    fi
    vynel node join --data-dir "$NODE_DATA" "$TOKEN" || die "the panel did not accept the token: is it fresh (24 h, single use) and is port 9443 open on the panel?"
  fi
  write_node_unit
  start_unit vynel-node
  info "starting the node"
  local ok=0
  for _ in $(seq 1 15); do
    if systemctl is-active --quiet vynel-node; then ok=1; break; fi
    sleep 2
  done

  echo
  pink "══════════════════════════  vynel установлен: нода  ══════════════════════════"
  echo
  line "Версия" "$(vynel version 2>/dev/null | awk '{print $2}')"
  line "Сервер" "$PUBLIC_IP"
  line "Служба" "$([[ $ok -eq 1 ]] && echo "запущена" || echo "не запустилась — journalctl -u vynel-node -e")"
  line "Данные" "$NODE_DATA"
  echo
  echo "  Нода получает настройки от панели сама: домен, ключи Reality, пользователей."
  echo "  Проверьте в панели: «Ноды» — через минуту состояние «работает»."
  echo
  line "  логи" "journalctl -u vynel-node -f"
  line "  перезапуск" "systemctl restart vynel-node"
  line "  панель переехала" "vynel node set-panel НОВЫЙ_IP:9443"
  line "  обновить / удалить" "запустить установщик ещё раз и выбрать пункт"
  echo
}

# ================================================================== update
do_update() {
  echo "  installed: $(installed_text)"
  if [[ $WIZARD -eq 1 ]]; then
    ask_yes "Обновить vynel, Xray и Caddy до свежих версий? Данные сохранятся" y || die "отменено"
  fi
  install_vynel
  if [[ $PANEL_WITH_NODE -eq 1 || $NODE_INSTALLED -eq 1 ]]; then install_xray force; fi
  install_caddy force
  drop_temp_swap
  if [[ $PANEL_INSTALLED -eq 1 ]]; then
    admin setting install.command "$(script_command)" >/dev/null
    systemctl restart vynel
  fi
  [[ $NODE_INSTALLED -eq 1 ]] && systemctl restart vynel-node
  sleep 3
  echo
  pink "══════════════════════════  vynel обновлён  ══════════════════════════"
  echo
  line "Версия" "$(vynel version 2>/dev/null | awk '{print $2}')"
  line "Xray" "$(xray version 2>/dev/null | head -1 | awk '{print $2}' || echo —)"
  line "Caddy" "$(caddy version 2>/dev/null | awk '{print $1}' || echo —)"
  if [[ $PANEL_INSTALLED -eq 1 ]]; then
    line "Панель" "$(systemctl is-active vynel 2>/dev/null || true)"
    WEB_URL="$(admin web 2>/dev/null | awk '$1=="url" {print $2}')"
    line "Адрес панели" "$WEB_URL"
  fi
  [[ $NODE_INSTALLED -eq 1 ]] && line "Нода" "$(systemctl is-active vynel-node 2>/dev/null || true)"
  echo
}

case "$MODE" in
  aio) do_aio ;;
  panel) do_panel ;;
  node) do_node ;;
  update) do_update ;;
esac
