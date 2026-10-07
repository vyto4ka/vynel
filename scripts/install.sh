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
GH_PROXY="${VYNEL_GH_PROXY:-}" FROM_SOURCE=0

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
  --gh-proxy URL         prefix for github.com downloads when GitHub is slow or blocked,
                         e.g. https://ghfast.top/ (also VYNEL_GH_PROXY)
  --build-from-source    allow compiling vynel when no prebuilt release can be downloaded
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
    --gh-proxy) GH_PROXY="${2:-}"; shift 2 ;;
    --build-from-source) FROM_SOURCE=1; shift ;;
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
      /usr/local/bin/vynel /usr/local/bin/vynel.prev /usr/local/bin/xray /usr/local/bin/caddy /usr/local/share/xray
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

export DEBIAN_FRONTEND=noninteractive
# apt waits for a lock held by unattended-upgrades instead of failing, and gives up on a mirror
# that stops answering instead of hanging.
APT_OPTS=(-o DPkg::Lock::Timeout=300 -o Acquire::Retries=3 -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30)
need_pkgs=0
for c in git curl unzip ip; do command -v "$c" >/dev/null || need_pkgs=1; done
if [[ $need_pkgs -eq 1 || "$MODE" != update ]]; then
  info "installing packages (apt)"
  if fuser /var/lib/dpkg/lock-frontend >/dev/null 2>&1; then
    echo "  apt is busy (automatic updates?) — waiting for it, up to 5 minutes"
  fi
  timeout 600 apt-get "${APT_OPTS[@]}" update -qq || red "  apt-get update failed or timed out — trying to install anyway"
  timeout 900 apt-get "${APT_OPTS[@]}" install -y -qq git curl unzip ca-certificates iproute2 psmisc >/dev/null || die "apt-get install failed"
fi

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
RELEASE_URL="${VYNEL_RELEASE_URL:-https://github.com/vyto4ka/vynel/releases/download/$RELEASE}"
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
  gov="$(curl -fsSL --connect-timeout 15 --max-time 30 'https://go.dev/VERSION?m=text' | head -1)"
  [[ -n "$gov" ]] || die "cannot reach go.dev to install Go"
  info "installing $gov"
  local tgz
  tgz="$(mktemp)"
  fetch "https://go.dev/dl/${gov}.linux-${GOARCH}.tar.gz" "$tgz" "Go" || die "cannot download Go"
  rm -rf /usr/local/go
  tar -C /usr/local -xzf "$tgz"
  rm -f "$tgz"
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

# ---- downloads ----

# gh URL: a github.com URL through the optional mirror (--gh-proxy).
gh() { printf '%s%s' "$GH_PROXY" "$1"; }

# fetch URL FILE [LABEL]: download with a visible progress bar. It never hangs: the connection
# must open within 20 s and a transfer slower than 2 KB/s for 30 s is aborted, with 2 retries.
# A slow but moving download is allowed to finish. Returns curl's code (22 = HTTP error).
STALL_SECS="${VYNEL_STALL_SECS:-30}"
fetch() {
  local url="$1" out="$2" label="${3:-}" progress=(-sS)
  [[ -t 1 && -n "$label" ]] && progress=(--progress-bar)
  [[ -n "$label" ]] && echo "  $label: $url"
  curl -fL "${progress[@]}" --connect-timeout 20 --speed-limit 2048 --speed-time "$STALL_SECS" \
    --retry 2 --retry-delay 3 --retry-connrefused -o "$out" "$url"
}

# gh_latest OWNER/REPO [ASSET]: the latest release tag, or nothing. Tried in order: the
# redirect of releases/latest/download/ASSET, the redirect of /releases/latest (neither has the
# API's 60 requests/hour limit, both work through --gh-proxy), then the API.
gh_latest() {
  local loc tag=""
  if [[ -n "${2:-}" ]]; then
    loc="$(curl -sS --connect-timeout 15 --max-time 30 -o /dev/null -w '%{redirect_url}' "$(gh "https://github.com/$1/releases/latest/download/$2")" 2>/dev/null || true)"
    [[ "$loc" == */releases/download/*/* ]] && tag="${loc#*/releases/download/}" && tag="${tag%%/*}"
  fi
  if [[ -z "$tag" ]]; then
    loc="$(curl -sS --connect-timeout 15 --max-time 30 -o /dev/null -w '%{redirect_url}' "$(gh "https://github.com/$1/releases/latest")" 2>/dev/null || true)"
    [[ "$loc" == */tag/* ]] && tag="${loc##*/tag/}"
  fi
  if [[ -z "$tag" ]]; then
    tag="$(curl -fsS --connect-timeout 15 --max-time 30 "https://api.github.com/repos/$1/releases/latest" 2>/dev/null | grep -o '"tag_name": *"[^"]*"' | cut -d'"' -f4 || true)"
  fi
  printf '%s' "$tag"
}

# install_release_binary: 0 = installed, 2 = already this version, 1 = not available (yet),
# 3 = network trouble (timeout, reset: GitHub is slow or blocked from here).
install_release_binary() {
  local tmp want have rc=0
  tmp="$(mktemp -d)"
  fetch "$(gh "$RELEASE_URL/SHA256SUMS")" "$tmp/SHA256SUMS" || rc=$?
  if [[ $rc -ne 0 ]]; then
    rm -rf "$tmp"
    [[ $rc -eq 22 ]] && return 1
    return 3
  fi
  want="$(awk -v f="vynel-linux-$GOARCH" '$2 == f {print $1}' "$tmp/SHA256SUMS")"
  [[ -n "$want" ]] || { rm -rf "$tmp"; return 1; }
  have="$(sha256sum /usr/local/bin/vynel 2>/dev/null | awk '{print $1}' || true)"
  if [[ "$have" == "$want" ]]; then
    rm -rf "$tmp"
    return 2
  fi
  fetch "$(gh "$RELEASE_URL/vynel-linux-$GOARCH")" "$tmp/vynel" "vynel" || rc=$?
  if [[ $rc -ne 0 ]]; then
    rm -rf "$tmp"
    [[ $rc -eq 22 ]] && return 1
    return 3
  fi
  if [[ "$(sha256sum "$tmp/vynel" | awk '{print $1}')" != "$want" ]]; then
    # CI replaces the release file by file: a binary and checksums from different builds.
    echo "  checksum does not match yet (the release is being updated)"
    rm -rf "$tmp"
    return 1
  fi
  [[ -x /usr/local/bin/vynel ]] && cp -f /usr/local/bin/vynel /usr/local/bin/vynel.prev
  install -m 755 "$tmp/vynel" /usr/local/bin/vynel.new
  mv -f /usr/local/bin/vynel.new /usr/local/bin/vynel
  rm -rf "$tmp"
  return 0
}

build_from_source() {
  ensure_go
  ensure_memory
  info "fetching source ($REF)"
  if [[ -d "$SRC/.git" ]]; then
    timeout 600 git -C "$SRC" fetch -q --depth 1 origin "$REF"
    git -C "$SRC" reset -q --hard FETCH_HEAD
  else
    rm -rf "$SRC"
    timeout 600 git clone -q --depth 1 -b "$REF" "$REPO" "$SRC"
  fi
  local commit
  commit="$(git -C "$SRC" rev-parse --short HEAD)"
  with_progress "building vynel $commit from source (10–20 minutes on a small VPS)" \
    bash -c "cd '$SRC' && CGO_ENABLED=0 go build -trimpath -ldflags '-s -w -X github.com/vyto4ka/vynel/internal/buildinfo.Version=$commit -X github.com/vyto4ka/vynel/internal/buildinfo.Commit=$commit' -o /usr/local/bin/vynel.new ./cmd/vynel"
  [[ -x /usr/local/bin/vynel ]] && cp -f /usr/local/bin/vynel /usr/local/bin/vynel.prev
  mv /usr/local/bin/vynel.new /usr/local/bin/vynel
  red "  note: a source build has no web UI inside (it is built in CI); the panel will say so. Use the release when it is available."
}

# install_vynel: VYNEL_CHANGED=1 when a new binary was put in place.
VYNEL_CHANGED=0
install_vynel() {
  info "vynel: checking the release $RELEASE"
  # Right after a push CI rebuilds the release (2–3 minutes): wait for it instead of compiling.
  local attempt rc tries="${VYNEL_RELEASE_TRIES:-10}" netfail=0
  for ((attempt = 1; attempt <= tries; attempt++)); do
    rc=0
    install_release_binary || rc=$?
    case $rc in
      0) VYNEL_CHANGED=1; green "  installed $(/usr/local/bin/vynel version)"; return 0 ;;
      2) green "  already the latest: $(/usr/local/bin/vynel version)"; return 0 ;;
      3) netfail=$((netfail + 1))
         if [[ $netfail -ge 2 ]]; then
           red "  GitHub does not answer from this server (timeouts)."
           break
         fi ;;
    esac
    if [[ $attempt -lt $tries ]]; then
      if [[ $rc -eq 3 ]]; then echo "  GitHub is slow or unreachable, retrying in 20 s"
      else echo "  release not available yet (CI rebuilds it after a push), retrying in 20 s ($attempt/$((tries - 1)))"; fi
      sleep 20
    fi
  done
  if [[ -x /usr/local/bin/vynel && $FROM_SOURCE -eq 0 ]]; then
    red "  cannot download the release: nothing changed, vynel keeps running."
    red "  Check https://github.com/vyto4ka/vynel/releases/tag/$RELEASE and run the update again;"
    red "  if GitHub is slow from this server, add --gh-proxy https://ghfast.top/"
    return 1
  fi
  if [[ $FROM_SOURCE -eq 0 && $ASSUME_YES -eq 0 ]] && have_tty; then
    ask_yes "Собрать vynel из исходников? Это 10–20 минут на маленьком сервере" n || die "отменено: запустите позже или с --gh-proxy"
  fi
  build_from_source
  VYNEL_CHANGED=1
}

# install_xray [force]: latest Xray release; skipped when it is already installed. An update keeps
# the old one if the download fails.
install_xray() {
  if [[ -x /usr/local/share/xray/xray && "${1:-}" != force ]]; then
    ln -sf /usr/local/share/xray/xray /usr/local/bin/xray
    return 0
  fi
  info "Xray: checking the latest release"
  local asset tmp latest have
  case "$GOARCH" in amd64) asset=Xray-linux-64.zip ;; arm64) asset=Xray-linux-arm64-v8a.zip ;; esac
  latest="$(gh_latest XTLS/Xray-core "$asset")"
  have="$(/usr/local/share/xray/xray version 2>/dev/null | awk 'NR==1 {print "v"$2}' || true)"
  if [[ -n "$latest" && "$latest" == "$have" ]]; then
    ln -sf /usr/local/share/xray/xray /usr/local/bin/xray
    green "  already the latest: Xray ${have#v}"
    return 0
  fi
  tmp="$(mktemp)"
  local url="https://github.com/XTLS/Xray-core/releases/latest/download/$asset"
  [[ -n "$latest" ]] && url="https://github.com/XTLS/Xray-core/releases/download/$latest/$asset"
  if fetch "$(gh "$url")" "$tmp" "Xray ${latest:-latest}" && unzip -tq "$tmp" >/dev/null 2>&1; then
    # Unpack aside and swap files: writing over a running xray fails with "Text file busy".
    local dir
    dir="$(mktemp -d)"
    unzip -o -q "$tmp" -d "$dir"
    mkdir -p /usr/local/share/xray
    local f
    for f in "$dir"/*; do install -m "$([[ "$(basename "$f")" == xray ]] && echo 755 || echo 644)" "$f" /usr/local/share/xray/; done
    rm -rf "$dir"
  elif [[ -x /usr/local/share/xray/xray ]]; then
    red "  Xray download failed, keeping the installed one"
  else
    rm -f "$tmp"
    die "cannot download Xray (slow GitHub? try --gh-proxy https://ghfast.top/)"
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
  info "Caddy: checking the latest release"
  local cv tmp have
  tmp="$(mktemp -d)"
  cv="$(gh_latest caddyserver/caddy)"
  have="$(/usr/local/bin/caddy version 2>/dev/null | awk '{print $1}' || true)"
  if [[ -n "$cv" && "$cv" == "$have" ]]; then
    rm -rf "$tmp"
    green "  already the latest: Caddy $have"
    return 0
  fi
  [[ -n "$cv" ]] || echo "  cannot find out the latest Caddy version (GitHub unreachable?)"
  if [[ -n "$cv" ]] && fetch "$(gh "https://github.com/caddyserver/caddy/releases/download/${cv}/caddy_${cv#v}_linux_${GOARCH}.tar.gz")" "$tmp/caddy.tgz" "Caddy $cv" \
    && tar -xzf "$tmp/caddy.tgz" -C "$tmp" caddy; then
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

# open_ports PORT[/PROTO]...: only when ufw is active; a bare port means TCP.
open_ports() {
  if command -v ufw >/dev/null && [[ "$(ufw status 2>/dev/null || true)" == *"Status: active"* ]]; then
    info "opening ports in ufw: $*"
    local p
    for p in "$@"; do
      if [[ "$p" == */* ]]; then ufw allow "$p" >/dev/null; else ufw allow "$p/tcp" >/dev/null; fi
    done
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
  local out
  out="$(vynel restore --data-dir "$DATA" --yes "$RESTORE")" # whole output: `| head` would SIGPIPE under pipefail
  RESTORED="${out%%$'\n'*}"
  green "  $RESTORED"
}

# setup_bot: stores the token and makes a bind code -> BOT_CODE BOT_LINK
BOT_CODE="" BOT_LINK=""
setup_bot() {
  if [[ -n "$BOT_TOKEN" ]]; then
    admin bot token "$BOT_TOKEN" >/dev/null || { red "  the bot token was not accepted; set it later in the panel (Telegram)"; return 0; }
  fi
  local st
  st="$(admin bot 2>/dev/null || true)" # captured: `| grep -q` can SIGPIPE the writer under pipefail
  [[ "$st" == *"token    set"* ]] || return 0
  if [[ "$st" == *$'\nadmin '* ]]; then return 0; fi # already bound (update or restore)
  BOT_CODE="$(admin bot code | awk '$1=="code" {print $2}')"
  local tok user
  tok="${BOT_TOKEN}"
  if [[ -n "$tok" ]]; then
    user="$(curl -fsS --max-time 10 "https://api.telegram.org/bot${tok}/getMe" 2>/dev/null | grep -o '"username":"[^"]*"' | cut -d'"' -f4 || true)"
    if [[ -n "$user" ]]; then BOT_LINK="https://t.me/${user}?start=${BOT_CODE}"; fi
  fi
  return 0
}

print_bot_block() {
  if [[ -n "$BOT_CODE" ]]; then
    pink "  Telegram-бот"
    if [[ -n "$BOT_LINK" ]]; then
      line "  привязать себя" "$BOT_LINK"
      line "  или отправьте боту" "/start $BOT_CODE"
    else
      line "  отправьте боту" "/start $BOT_CODE"
    fi
    echo "  Код действует 15 минут; новый: vynel admin bot code (или в панели: Telegram)."
    echo
  elif [[ "$(admin bot 2>/dev/null || true)" != *"token    set"* ]]; then
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
  # 443/udp: Hysteria2 inbounds (docs/PROFILES.md §9).
  if [[ "$GATEWAY_LISTEN" == 127.0.0.1:* ]]; then open_ports 80 443 443/udp; else open_ports 80 443 443/udp "${GATEWAY_LISTEN##*:}"; fi

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
    if [[ "$(admin node list 2>/dev/null || true)" == *"in sync"* ]]; then ok=1; break; fi
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
  if ! awk 'NR>1 {print $2}' <<<"$(admin profile list 2>/dev/null || true)" | grep -qx Reality; then
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
  open_ports 80 443 443/udp

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
# wait_active UNIT SECONDS: the unit is active and stays up (a crash loop would flip it back).
wait_active() {
  local i
  for ((i = 0; i < $2; i++)); do
    sleep 1
    if [[ "$(systemctl is-active "$1" 2>/dev/null)" == active ]] && (( i >= 4 )); then
      sleep 2
      [[ "$(systemctl is-active "$1" 2>/dev/null)" == active ]] && return 0
    fi
  done
  return 1
}

do_update() {
  echo "  installed: $(installed_text) · $(vynel version 2>/dev/null || echo '?')"
  if [[ $WIZARD -eq 1 ]]; then
    ask_yes "Обновить vynel, Xray и Caddy до свежих версий? Данные сохранятся" y || die "отменено"
  fi
  install_vynel || exit 1
  if [[ $PANEL_WITH_NODE -eq 1 || $NODE_INSTALLED -eq 1 ]]; then install_xray force; fi
  install_caddy force
  drop_temp_swap

  local units=() u failed=()
  [[ $PANEL_INSTALLED -eq 1 ]] && units+=(vynel)
  [[ $NODE_INSTALLED -eq 1 ]] && units+=(vynel-node)
  if [[ $PANEL_INSTALLED -eq 1 ]]; then
    admin setting install.command "$(script_command)" >/dev/null || true
  fi
  for u in "${units[@]}"; do
    info "restarting $u"
    systemctl restart "$u" || true
    wait_active "$u" 30 || failed+=("$u")
  done
  if [[ ${#failed[@]} -gt 0 ]]; then
    red "  did not start: ${failed[*]}"
    journalctl -u "${failed[0]}" -n 15 --no-pager 2>/dev/null | sed 's/^/    /' || true
    if [[ $VYNEL_CHANGED -eq 1 && -x /usr/local/bin/vynel.prev ]]; then
      red "  rolling back to the previous vynel"
      cp -f /usr/local/bin/vynel.prev /usr/local/bin/vynel.new && mv -f /usr/local/bin/vynel.new /usr/local/bin/vynel
      for u in "${units[@]}"; do systemctl restart "$u" || true; done
      for u in "${units[@]}"; do wait_active "$u" 30 || die "$u does not start even with the previous version: journalctl -u $u -n 50"; done
      die "the new version did not start and was rolled back ($(vynel version 2>/dev/null)); send the log above"
    fi
    die "${failed[*]} not running: journalctl -u ${failed[0]} -n 50"
  fi
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
