#!/usr/bin/env bash
# vynel installer: one script for every role (docs/INSTALL_GUIDE.md).
#
#   bash <(curl -fsSL https://raw.githubusercontent.com/vyto4ka/vynel/main/scripts/install.sh)
#
# Without arguments it shows a menu: everything on one server, panel only, node only, update,
# uninstall. Every choice can also run unattended:
#
#   install.sh --mode aio   --domain nl.example.com [--email you@example.com] [--admin-login boss] --yes
#   install.sh --mode panel --domain panel.example.com [--email ...] --yes
#   install.sh --mode node  --token vyn1.…            (the command is shown in the panel: Ноды → + Нода)
#   install.sh --mode update
#   install.sh --mode harden  [--ssh-keys-only] [--ssh-port random|N] [--firewall on|off]
#   install.sh --mode uninstall [--purge] [--yes]
#
# Binaries come from the branch's "edge" release built by CI; without one vynel is built from
# source (slow on small servers). Updates keep all data.
set -euo pipefail

REPO="${VYNEL_REPO:-https://github.com/vyto4ka/vynel.git}"
REF="${VYNEL_REF:-main}"
SRC=/opt/vynel-src
DATA=/var/lib/vynel
NODE_DATA=/var/lib/vynel-node
PANEL_UNIT=/etc/systemd/system/vynel.service
NODE_UNIT=/etc/systemd/system/vynel-node.service

MODE="" DOMAIN="" SUB_DOMAIN="" EMAIL="" NAME="" COUNTRY="" PUBLIC_IP="" GATEWAY_LISTEN=":9443"
ADMIN_LOGIN="admin" TOKEN="" BOT_TOKEN="" RESTORE="" ASSUME_YES=0 PURGE=0 WIZARD=0
GH_PROXY="${VYNEL_GH_PROXY:-}" FROM_SOURCE=0
VPN_IP="" SUB_IP="" FIREWALL="" SSH_PORT="" SSH_KEYS_ONLY="" SUB_PATH="" PANEL_PATH=""
NO_TUI="${VYNEL_NO_TUI:-0}" TUI=0 SETUP_BIN=""

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
  harden     protect this server: firewall, SSH by key only, another SSH port
  uninstall  remove the services (--purge also deletes all data and binaries)

Options:
  --domain DOMAIN        aio: node domain; panel: panel and subscription domain (A record -> this server)
  --sub-domain DOMAIN    aio: separate subscription domain (optional)
  --email EMAIL          email for Let's Encrypt (optional)
  --name NAME            aio: server name shown in VPN clients
  --country CC           aio: 2-letter country code for the flag (default: detected)
  --ip IP                public IP (default: detected)
  --vpn-ip IP            aio: address the VPN listens on (servers with several IPs)
  --sub-ip IP            aio: address of the subscriptions and the panel (default: the VPN one)
  --firewall on|off      firewall: only SSH and what vynel serves is open (default on for new installs)
  --ssh-keys-only        SSH: turn password login off (only when keys are installed)
  --ssh-port random|N    SSH: move to another port (the old one stays until you confirm)
  --gateway-listen ADDR  port for additional nodes (default :9443; 127.0.0.1:9443 = none)
  --admin-login LOGIN    web panel login (default admin; the password is generated)
  --sub-path PATH        aio/panel: path of subscription links (default /s/), e.g. sub or api/v1/client
  --panel-path PATH      aio/panel: secret path of the web panel (default: random, 12 characters)
  --token TOKEN          node: join token from the panel
  --bot-token TOKEN      aio/panel: Telegram bot token from @BotFather (optional)
  --restore FILE         aio/panel: restore users, nodes and keys from a backup (.tar.gz)
  --ref REF              git branch whose release to install (default $REF)
  --gh-proxy URL         prefix for github.com downloads when GitHub is slow or blocked,
                         e.g. https://ghfast.top/ (also VYNEL_GH_PROXY)
  --build-from-source    allow compiling vynel when no prebuilt release can be downloaded
  --no-tui               plain questions instead of the terminal form (also VYNEL_NO_TUI=1)
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
    --vpn-ip) VPN_IP="${2:-}"; shift 2 ;;
    --sub-ip) SUB_IP="${2:-}"; shift 2 ;;
    --firewall) FIREWALL="${2:-}"; shift 2 ;;
    --ssh-keys-only) SSH_KEYS_ONLY=yes; shift ;;
    --ssh-port) SSH_PORT="${2:-}"; shift 2 ;;
    --gateway-listen) GATEWAY_LISTEN="${2:-}"; shift 2 ;;
    --admin-login) ADMIN_LOGIN="${2:-}"; shift 2 ;;
    --sub-path) SUB_PATH="${2:-}"; shift 2 ;;
    --panel-path) PANEL_PATH="${2:-}"; shift 2 ;;
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
    --no-tui) NO_TUI=1; shift ;;
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

# resolve DOMAIN: its A record as public DNS sees it. Not /etc/hosts: Debian maps the server's own
# name to 127.0.1.1 there, and a domain named like the server would "resolve" to loopback.
resolve() {
  local bin ip=""
  for bin in "$SETUP_BIN" /usr/local/bin/vynel; do
    if [[ -n "$bin" && -x "$bin" ]] && ip="$("$bin" net resolve "$1" 2>/dev/null | head -1)" && [[ -n "$ip" ]]; then
      printf '%s' "$ip"
      return 0
    fi
  done
  ip="$(curl -fsS --max-time 5 -H 'accept: application/dns-json' "https://1.1.1.1/dns-query?name=$1&type=A" 2>/dev/null |
    grep -o '"data":"[0-9.]*"' | head -1 | cut -d'"' -f4 || true)"
  [[ -n "$ip" ]] || ip="$(getent ahostsv4 "$1" | awk '$1 !~ /^127\./ {print $1; exit}' || true)"
  printf '%s' "$ip"
}

# ask_domain VAR "question" "default" [IP] -> asks until the A record points at IP (default: the
# public IP) or the user accepts
ask_domain() {
  local d r want="${4:-$PUBLIC_IP}"
  while true; do
    ask d "$2" "$3"
    d="$(printf '%s' "$d" | tr '[:upper:]' '[:lower:]' | sed 's#^https\?://##; s#/.*##')"
    if [[ ! "$d" =~ ^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$ ]]; then
      red "  «$d» не похоже на домен"
      continue
    fi
    r="$(resolve "$d")"
    if [[ "$r" == "$want" ]]; then
      green "  ✓ $d → $want"
      break
    fi
    if [[ "$r" =~ ^(104\.(1[6-9]|2[0-7])|172\.(6[4-9]|7[01])|188\.114\.(9[6-9]|1[01][0-9])|162\.15[89]|141\.101|108\.162|190\.93|198\.41)\. ]]; then
      red "  ✗ $d за прокси Cloudflare (оранжевое облако). Включите «DNS only» (серое облако): A-запись $d → $want."
    else
      red "  ✗ $d → ${r:-ничего}, а нужно $want. Создайте A-запись $d → $want."
    fi
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

# bash_menu: the plain menu (no terminal form: --no-tui, an old release, a dumb terminal).
bash_menu() {
  echo
  pink "  vynel — установка"
  echo "  На этом сервере сейчас: $(installed_text)"
  echo
  echo "  1) Всё на этом сервере: панель + VPN-нода   (проще всего начать с этого)"
  echo "  2) Только панель (VPN-ноды будут на других серверах)"
  echo "  3) Только VPN-нода для существующей панели  (нужен токен из панели)"
  echo "  4) Обновить установленное"
  echo "  5) Защитить сервер: файрвол, SSH только по ключу, другой порт SSH"
  echo "  6) Удалить"
  echo
  local def=1 choice=""
  [[ $PANEL_INSTALLED -eq 1 || $NODE_INSTALLED -eq 1 ]] && def=4
  ask choice "Выберите" "$def"
  case "$choice" in
    1) MODE=aio ;; 2) MODE=panel ;; 3) MODE=node ;; 4) MODE=update ;; 5) MODE=harden ;; 6) MODE=uninstall ;;
    *) die "нет такого пункта: $choice" ;;
  esac
  WIZARD=1
}

if [[ -z "$MODE" ]]; then
  have_tty || { usage; die "--mode is required when there is no terminal to ask questions"; }
  if [[ "$NO_TUI" == 1 || "${TERM:-dumb}" == dumb ]]; then
    bash_menu
  else
    MODE=tui # the form opens once the vynel binary is here (below)
  fi
fi
case "$MODE" in aio|panel|node|update|harden|uninstall|tui) ;; *) usage; die "unknown mode $MODE" ;; esac
case "$FIREWALL" in ""|on|off) ;; *) die "--firewall: on or off" ;; esac
[[ -z "$SSH_PORT" || "$SSH_PORT" == random || ( "$SSH_PORT" =~ ^[0-9]+$ && "$SSH_PORT" -ge 1024 && "$SSH_PORT" -le 65535 ) ]] || die "--ssh-port: random or 1024–65535"

# ---- uninstall ----

do_uninstall() {
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
  vynel firewall off >/dev/null 2>&1 || true # the rules follow the services; do not leave a closed server
  for unit in vynel vynel-node; do
    systemctl disable --now "$unit" 2>/dev/null || true
  done
  rm -f "$PANEL_UNIT" "$NODE_UNIT"
  systemctl daemon-reload
  if [[ $PURGE -eq 1 ]]; then
    systemctl disable vynel-ips 2>/dev/null || true
    rm -f /etc/systemd/system/vynel-ips.service
    rm -rf "$DATA" "$NODE_DATA" /etc/sysctl.d/90-vynel.conf "$SRC" /etc/vynel \
      /usr/local/bin/vynel /usr/local/bin/vynel.prev /usr/local/bin/xray /usr/local/bin/caddy /usr/local/share/xray
  fi
  green "removed$([[ $PURGE -eq 1 ]] && echo " with all data")"
}

if [[ "$MODE" == uninstall ]]; then
  do_uninstall
  exit 0
fi

# ---- system checks ----

command -v apt-get >/dev/null || die "only Debian/Ubuntu are supported"
case "$(uname -m)" in
  x86_64) GOARCH=amd64 ;;
  aarch64|arm64) GOARCH=arm64 ;;
  *) die "unsupported architecture $(uname -m)" ;;
esac

check_mode() {
  if [[ "$MODE" == update || "$MODE" == harden ]]; then
    [[ $PANEL_INSTALLED -eq 1 || $NODE_INSTALLED -eq 1 ]] || die "nothing to $MODE: vynel is not installed here (run without --mode for the menu)"
  fi
  if [[ "$MODE" == node && $PANEL_INSTALLED -eq 1 ]]; then
    die "this server runs the panel (ports 80/443 are taken); a node needs another server — or choose all-in-one to run a node here"
  fi
  if [[ ( "$MODE" == aio || "$MODE" == panel ) && $NODE_INSTALLED -eq 1 ]]; then
    die "this server runs a node of another panel; remove it first (install.sh --mode uninstall)"
  fi
  return 0
}
check_mode

export DEBIAN_FRONTEND=noninteractive
# apt waits for a lock held by unattended-upgrades instead of failing, and gives up on a mirror
# that stops answering instead of hanging.
APT_OPTS=(-o DPkg::Lock::Timeout=300 -o Acquire::Retries=3 -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30)
export NEEDRESTART_MODE=a # Ubuntu's needrestart must not stop apt with a dialog

# ensure_packages PKG...: installs what is missing, after the questions. Nothing runs when all
# is there; the package lists are refreshed only when installing without it fails (fresh images
# usually have usable lists, and `apt-get update` is the slow part).
ensure_packages() {
  local p missing=()
  for p in "$@"; do
    dpkg-query -W -f='${Status}' "$p" 2>/dev/null | grep -q "install ok installed" || missing+=("$p")
  done
  [[ ${#missing[@]} -gt 0 ]] || return 0
  info "installing packages: ${missing[*]}"
  if command -v fuser >/dev/null && fuser /var/lib/dpkg/lock-frontend >/dev/null 2>&1; then
    echo "  apt is busy (automatic updates?) — waiting for it, up to 5 minutes"
  fi
  if timeout 900 apt-get "${APT_OPTS[@]}" install -y -qq --no-install-recommends "${missing[@]}" >/dev/null 2>&1; then
    return 0
  fi
  echo "  refreshing the package lists (apt-get update)"
  timeout 600 apt-get "${APT_OPTS[@]}" update -qq || red "  apt-get update failed or timed out — trying to install anyway"
  timeout 900 apt-get "${APT_OPTS[@]}" install -y -qq --no-install-recommends "${missing[@]}" >/dev/null || die "apt-get install ${missing[*]} failed"
}
# What the installation needs (the questions need nothing beyond curl, which got this script here).
BASE_PKGS=(curl unzip ca-certificates iproute2 psmisc nftables openssl)

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

# check_dns DOMAIN[@IP]...: every domain must point at its IP (default: the public IP).
check_dns() {
  local arg d want resolved
  for arg in "$@"; do
    d="${arg%@*}" want="$PUBLIC_IP"
    [[ "$arg" == *@* ]] && want="$(public_of "${arg#*@}")"
    resolved="$(resolve "$d")"
    if [[ "$resolved" != "$want" && $WIZARD -eq 0 ]]; then
      red "DNS: $d -> ${resolved:-nothing}, expected $want"
      red "     create an A record $d -> $want, otherwise the certificate cannot be issued"
      confirm "continue anyway?" || die "aborted"
    fi
  done
}

# ---- addresses (docs/INBOUNDS.md §2.7) ----

# list_ips: "IP INTERFACE" for global IPv4 addresses of real interfaces.
list_ips() {
  ip -o -4 addr show scope global 2>/dev/null | awk '{split($4, a, "/"); print a[1], $2}' |
    grep -Ev ' (docker|br-|veth|virbr|cni|flannel|cali|lxc|tun|tap|wg)' || true
}
primary_ip() { ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i=1;i<NF;i++) if ($i=="src") print $(i+1)}'; }
is_private() { [[ "$1" =~ ^(10\.|192\.168\.|172\.(1[6-9]|2[0-9]|3[01])\.|100\.(6[4-9]|[7-9][0-9]|1[01][0-9]|12[0-7])\.) ]]; }
# public_of IP: what the world sees for an interface address (a private one sits behind NAT).
public_of() { if is_private "$1"; then printf '%s' "$PUBLIC_IP"; else printf '%s' "$1"; fi; }

# choose_ip VAR "question" DEFAULT: a number from the list or an address.
choose_ip() {
  local answer i
  ask answer "$2 (номер или IP)" "$3"
  if [[ "$answer" =~ ^[0-9]+$ ]] && (( answer >= 1 && answer <= ${#IPS[@]} )); then
    answer="${IPS[$((answer - 1))]%% *}"
  fi
  for i in "${IPS[@]}"; do
    if [[ "${i%% *}" == "$answer" ]]; then printf -v "$1" '%s' "$answer"; return 0; fi
  done
  die "$answer — нет такого адреса на сервере (vynel net add-ip, если его нужно добавить)"
}

# ask_ips: on a server with several addresses, which one serves the VPN and which the panel.
IPS=()
ask_ips() {
  mapfile -t IPS < <(list_ips)
  (( ${#IPS[@]} > 1 )) || return 0
  local prim n=1 i ip note
  prim="$(primary_ip)"
  echo
  echo "  На сервере несколько IP:"
  for i in "${IPS[@]}"; do
    ip="${i%% *}" note=""
    [[ "$ip" == "$prim" ]] && note+=" · основной"
    is_private "$ip" && note+=" · за NAT, снаружи $PUBLIC_IP"
    printf '    %d) %-16s %s%s\n' "$n" "$ip" "${i#* }" "$note"
    n=$((n + 1))
  done
  echo "  VPN и панель можно развести по разным IP: заблокируют один — второй продолжит работать."
  choose_ip VPN_IP "IP для VPN" "${VPN_IP:-$prim}"
  choose_ip SUB_IP "IP для подписок и веб-панели" "${SUB_IP:-$VPN_IP}"
}

# ---- security (docs/INSTALL_GUIDE.md §7) ----

SSHD_DROPIN=/etc/ssh/sshd_config.d/90-vynel.conf
ssh_value() { sshd -T 2>/dev/null | awk -v k="$1" '$1==k {print $2}' | head -1 || true; }
ssh_ports() { sshd -T 2>/dev/null | awk '$1=="port" {print $2}' | sort -un | tr '\n' ' ' | sed 's/ $//' || true; }
has_ssh_keys() {
  local f
  for f in /root/.ssh/authorized_keys /home/*/.ssh/authorized_keys; do
    [[ -s "$f" ]] && grep -qE '^(ssh-|ecdsa-|sk-)' "$f" && return 0
  done
  return 1
}

# ask_security: the wizard's questions; flags answer them without a terminal.
ask_security() {
  echo
  pink "  Защита сервера"
  echo "  Файрвол: открыты только SSH и то, что обслуживает vynel (порты он знает сам)."
  echo "  Всё остальное молчит, адрес, который перебирает закрытые порты, блокируется на сутки."
  if ask_yes "Включить файрвол?" y; then FIREWALL=on; else FIREWALL=off; fi
  if [[ "$(ssh_value passwordauthentication)" == yes ]]; then
    if has_ssh_keys; then
      ask_yes "Вход по SSH только по ключу (пароль выключить)? Ключи на сервере найдены" y && SSH_KEYS_ONLY=yes
    else
      echo "  SSH принимает пароль, а ключей на сервере нет — оставляю как есть. Добавьте ключ (ssh-copy-id) и запустите «Защитить сервер»."
    fi
  fi
  local cur
  cur="$(ssh_ports)"
  if [[ "$cur" == 22 ]] && ask_yes "Перенести SSH с 22 на случайный порт? (боты перебирают пароли на 22; старый закроется после проверки)" n; then
    SSH_PORT=random
  fi
}

# apply_firewall: turns the managed firewall on or off and waits for the rules.
FW_OK=0
apply_firewall() {
  [[ -n "$FIREWALL" ]] || return 0
  if [[ "$FIREWALL" == off ]]; then
    vynel firewall off >/dev/null 2>&1 || true
    return 0
  fi
  command -v nft >/dev/null || ensure_packages nftables || true
  command -v nft >/dev/null || { red "  nftables is not installed: firewall skipped"; return 0; }
  info "firewall: only SSH and what vynel serves stays open"
  vynel firewall on >/dev/null
  local i
  for i in $(seq 1 30); do
    if [[ "$(vynel firewall status 2>/dev/null | head -1)" == "firewall   on" ]]; then FW_OK=1; break; fi
    sleep 1
  done
  [[ $FW_OK -eq 1 ]] || red "  the firewall rules did not load: journalctl -u vynel -u vynel-node | grep firewall"
}

reload_ssh() {
  if systemctl is-active --quiet ssh.socket 2>/dev/null; then
    systemctl daemon-reload && systemctl restart ssh.socket
  else
    systemctl reload ssh 2>/dev/null || systemctl reload sshd 2>/dev/null || systemctl restart ssh 2>/dev/null || systemctl restart sshd
  fi
}

# write_sshd PORTS...: the drop-in with our settings; an invalid config is never left behind.
write_sshd() {
  mkdir -p /etc/ssh/sshd_config.d
  if ! grep -qiE '^[[:space:]]*Include[[:space:]]+/etc/ssh/sshd_config\.d/\*\.conf' /etc/ssh/sshd_config; then
    sed -i '1i Include /etc/ssh/sshd_config.d/*.conf' /etc/ssh/sshd_config
  fi
  local keys_only=0
  if [[ "$SSH_KEYS_ONLY" == yes ]] || grep -qs '^PasswordAuthentication no' "$SSHD_DROPIN"; then keys_only=1; fi
  {
    echo "# managed by vynel install.sh --mode harden"
    if [[ $keys_only -eq 1 ]]; then
      echo "PasswordAuthentication no"
      echo "KbdInteractiveAuthentication no"
      echo "PermitRootLogin prohibit-password"
    fi
    echo "MaxAuthTries 3"
    echo "LoginGraceTime 30"
    local p
    for p in "$@"; do echo "Port $p"; done
  } >"$SSHD_DROPIN.new"
  local old=""
  [[ -f "$SSHD_DROPIN" ]] && old="$(cat "$SSHD_DROPIN")"
  mv -f "$SSHD_DROPIN.new" "$SSHD_DROPIN"
  if ! sshd -t 2>/tmp/vynel-sshd.err; then
    red "  sshd rejected the settings, nothing changed: $(cat /tmp/vynel-sshd.err)"
    if [[ -n "$old" ]]; then printf '%s\n' "$old" >"$SSHD_DROPIN"; else rm -f "$SSHD_DROPIN"; fi
    return 1
  fi
  reload_ssh
}

# apply_ssh: keys only and/or a new port. A new port is opened next to the old one; the old one
# closes only after you log in on the new one from another window.
SSH_NOTE=""
apply_ssh() {
  [[ "$SSH_KEYS_ONLY" == yes || -n "$SSH_PORT" ]] || return 0
  if [[ "$SSH_KEYS_ONLY" == yes ]] && ! has_ssh_keys; then
    red "  no SSH keys in authorized_keys: password login stays on (it would lock you out)"
    SSH_KEYS_ONLY=""
  fi
  local cur new keep=()
  cur="$(ssh_ports)"
  read -r -a keep <<<"$cur"
  (( ${#keep[@]} > 0 )) || keep=(22)
  if [[ -n "$SSH_PORT" ]]; then
    new="$SSH_PORT"
    [[ "$new" == random ]] && new=$(( 20000 + RANDOM % 40000 ))
    info "SSH: opening port $new next to ${cur:-22}"
    write_sshd "${keep[@]}" "$new" || return 0
    # The firewall follows sshd within 15 s.
    local i
    for i in $(seq 1 30); do
      [[ "$(vynel firewall status 2>/dev/null)" != *"firewall   on"* ]] && break
      [[ "$(vynel firewall status 2>/dev/null | awk '$1=="ssh"')" == *"$new"* ]] && break
      sleep 1
    done
    if [[ $ASSUME_YES -eq 0 || $TUI -eq 1 ]] && have_tty; then
      echo
      pink "  Проверьте вход по новому порту в ДРУГОМ окне, это окно не закрывайте:"
      echo "    ssh -p $new root@$PUBLIC_IP"
      if ask_yes "Вход по порту $new работает?" n; then
        write_sshd "$new" && SSH_NOTE="порт $new (22 закрыт)"
      else
        write_sshd "${keep[@]}" && SSH_NOTE="порт не менялся (${cur:-22}): новый не подтвердили"
        red "  Порт не сменён. Возможно, его закрывает файрвол хостера — откройте $new там и повторите."
      fi
    else
      SSH_NOTE="порты ${cur:-22} и $new; после проверки входа по $new закройте старый: install.sh --mode harden --ssh-port $new"
    fi
  else
    write_sshd "${keep[@]}" || return 0
  fi
  [[ "$SSH_KEYS_ONLY" == yes ]] && SSH_NOTE="только ключ${SSH_NOTE:+, $SSH_NOTE}"
  return 0
}

print_security_block() {
  pink "  Защита"
  local st
  st="$(vynel firewall status 2>/dev/null || true)"
  if [[ "$st" == "firewall   on"* ]]; then
    line "  файрвол" "включён"
    awk '$1=="ssh" || $1=="open" {sub(/^[ \t]*/, ""); print "    " $0}' <<<"$st"
    line "  сканеры портов" "блокируются на сутки (vynel firewall status)"
  else
    line "  файрвол" "выключен (включить: install.sh --mode harden)"
  fi
  local pw ports
  pw="$(ssh_value passwordauthentication)" ports="$(ssh_ports)"
  line "  SSH" "порт ${ports:-22}, $([[ "$pw" == no ]] && echo "только по ключу" || echo "пароль разрешён")"
  [[ -n "$SSH_NOTE" ]] && line "" "$SSH_NOTE"
  return 0
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
# Releases are signed (Ed25519, SHA256SUMS.signed): with a key here a binary whose checksums are
# not signed by it is never installed, even when it comes through --gh-proxy. Set up with
# scripts/release-key.sh (docs/DEVELOPMENT.md). Empty = checksums only.
RELEASE_PUBKEY="${VYNEL_RELEASE_PUBKEY:-}"

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
# release_sums DIR: the release's SHA256SUMS into DIR (signature checked when there is a key).
# 0 = ok, 1 = no release (yet), 3 = network trouble.
release_sums() {
  local rc=0
  if [[ -n "$RELEASE_PUBKEY" ]]; then
    fetch "$(gh "$RELEASE_URL/SHA256SUMS.signed")" "$1/SHA256SUMS.signed" || rc=$?
    if [[ $rc -eq 0 ]]; then
      verify_signed "$1/SHA256SUMS.signed" "$1/SHA256SUMS" || die "the release signature does not match: the download was changed on the way (proxy?). Nothing installed"
    fi
  else
    fetch "$(gh "$RELEASE_URL/SHA256SUMS")" "$1/SHA256SUMS" || rc=$?
  fi
  [[ $rc -eq 0 ]] && return 0
  [[ $rc -eq 22 ]] && return 1
  return 3
}

# release_binary DIR: downloads vynel for this machine into DIR/vynel and checks it -> 0/1/3.
release_binary() {
  local want rc=0
  want="$(awk -v f="vynel-linux-$GOARCH" '$2 == f {print $1}' "$1/SHA256SUMS")"
  [[ -n "$want" ]] || return 1
  if [[ -n "$SETUP_BIN" && "$(sha256sum "$SETUP_BIN" 2>/dev/null | awk '{print $1}')" == "$want" ]]; then
    cp -f "$SETUP_BIN" "$1/vynel" # already downloaded for the setup form
    return 0
  fi
  fetch "$(gh "$RELEASE_URL/vynel-linux-$GOARCH")" "$1/vynel" "vynel" || rc=$?
  if [[ $rc -ne 0 ]]; then
    [[ $rc -eq 22 ]] && return 1
    return 3
  fi
  if [[ "$(sha256sum "$1/vynel" | awk '{print $1}')" != "$want" ]]; then
    # CI replaces the release file by file: a binary and checksums from different builds.
    echo "  checksum does not match yet (the release is being updated)"
    return 1
  fi
  chmod 755 "$1/vynel"
}

install_release_binary() {
  local tmp want have rc=0
  tmp="$(mktemp -d)"
  release_sums "$tmp" || rc=$?
  if [[ $rc -ne 0 ]]; then
    rm -rf "$tmp"
    return $rc
  fi
  want="$(awk -v f="vynel-linux-$GOARCH" '$2 == f {print $1}' "$tmp/SHA256SUMS")"
  [[ -n "$want" ]] || { rm -rf "$tmp"; return 1; }
  have="$(sha256sum /usr/local/bin/vynel 2>/dev/null | awk '{print $1}' || true)"
  if [[ "$have" == "$want" ]]; then
    rm -rf "$tmp"
    return 2
  fi
  release_binary "$tmp" || rc=$?
  if [[ $rc -ne 0 ]]; then
    rm -rf "$tmp"
    return $rc
  fi
  [[ -x /usr/local/bin/vynel ]] && cp -f /usr/local/bin/vynel /usr/local/bin/vynel.prev
  install -m 755 "$tmp/vynel" /usr/local/bin/vynel.new
  mv -f /usr/local/bin/vynel.new /usr/local/bin/vynel
  rm -rf "$tmp"
  return 0
}

# verify_signed SIGNED OUT: checks "# ed25519 SIG" (the last line) over the lines above it and
# writes them to OUT.
verify_signed() {
  local dir sig
  command -v openssl >/dev/null || ensure_packages openssl
  dir="$(dirname "$2")"
  sig="$(tail -n 1 "$1")"
  [[ "$sig" == "# ed25519 "* ]] || return 1
  head -n -1 "$1" >"$2"
  printf '%s' "${sig#\# ed25519 }" | base64 -d >"$dir/sums.sig" 2>/dev/null || return 1
  printf -- '-----BEGIN PUBLIC KEY-----\n%s\n-----END PUBLIC KEY-----\n' "$RELEASE_PUBKEY" >"$dir/release.pub"
  openssl pkeyutl -verify -pubin -inkey "$dir/release.pub" -rawin -in "$2" -sigfile "$dir/sums.sig" >/dev/null 2>&1
}

build_from_source() {
  ensure_packages git
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
    code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 --resolve "$1:443:${2:-$PUBLIC_IP}" "https://$1/" || true)"
    if [[ "$code" == "200" ]]; then CERT_OK=1; return 0; fi
    sleep 2
  done
  red "https://$1/ does not answer yet (DNS or port 80 not reachable?). Caddy keeps retrying; see journalctl -u vynel"
}

# set_paths: the subscription link path and the panel's secret path, when given.
set_paths() {
  local p
  if [[ -n "$SUB_PATH" ]]; then
    p="${SUB_PATH#/}"; p="${p%/}"
    [[ "$p" =~ ^[A-Za-z0-9_-]+(/[A-Za-z0-9_-]+)*$ ]] || die "--sub-path: latin letters, digits, _ - and /"
    admin setting sub.prefix "/$p/" >/dev/null
  fi
  if [[ -n "$PANEL_PATH" ]]; then
    p="${PANEL_PATH#/}"; p="${p%/}"
    [[ "$p" =~ ^[A-Za-z0-9_-]{6,}$ ]] || die "--panel-path: at least 6 latin letters, digits, _ -"
    admin setting web.path "/$p/" >/dev/null
  fi
  return 0
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
    ask_ips
    if [[ -n "$VPN_IP" && "$VPN_IP" != "$SUB_IP" ]]; then
      echo "  VPN и панель на разных IP — нужны два домена."
      ask_domain DOMAIN "Домен VPN (A-запись на $(public_of "$VPN_IP"); там же сайт-заглушка)" "$DOMAIN" "$(public_of "$VPN_IP")"
      ask_domain SUB_DOMAIN "Домен подписок и панели (A-запись на $(public_of "$SUB_IP"))" "$SUB_DOMAIN" "$(public_of "$SUB_IP")"
    else
      ask_domain DOMAIN "Домен сервера (A-запись на этот IP; на нём VPN, сайт-заглушка, подписки и панель)" "$DOMAIN" "$(public_of "${VPN_IP:-$PUBLIC_IP}")"
      if ask_yes "Отдельный домен для подписок? (не обязательно, одного домена достаточно)" n; then
        ask_domain SUB_DOMAIN "Домен подписок" "$SUB_DOMAIN" "$(public_of "${SUB_IP:-$PUBLIC_IP}")"
      else
        SUB_DOMAIN="$DOMAIN"
      fi
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
    ask_security
    SUB_DOMAIN="${SUB_DOMAIN:-$DOMAIN}"
    echo
    echo "  Сервер:    $PUBLIC_IP"
    [[ -n "$VPN_IP" && "$VPN_IP" != "$SUB_IP" ]] && echo "  IP:        VPN $VPN_IP, панель и подписки $SUB_IP"
    echo "  Домен:     $DOMAIN"
    echo "  Подписки:  https://$SUB_DOMAIN/s/…"
    echo "  Клиенты:   ${NAME} (${COUNTRY:-без флага})"
    echo "  Email:     ${EMAIL:-—}"
    echo "  Ноды:      $([[ "$GATEWAY_LISTEN" == 127.0.0.1:* ]] && echo "только этот сервер" || echo "порт 9443 открыт")"
    echo "  Панель:    логин $ADMIN_LOGIN, пароль будет показан в конце"
    echo "  Защита:    файрвол $([[ "$FIREWALL" == on ]] && echo "включён" || echo "выключен")$([[ "$SSH_KEYS_ONLY" == yes ]] && echo ", SSH только по ключу")$([[ -n "$SSH_PORT" ]] && echo ", SSH на другой порт")"
    echo
    ask_yes "Устанавливаем?" y || die "отменено"
  fi
  [[ -n "$DOMAIN" ]] || { usage; die "--domain is required"; }
  [[ "$ADMIN_LOGIN" =~ ^[A-Za-z0-9_.@-]{1,64}$ ]] || die "the login may contain latin letters, digits and _ . - @"
  SUB_DOMAIN="${SUB_DOMAIN:-$DOMAIN}"
  FIREWALL="${FIREWALL:-on}"
  [[ -n "$SUB_IP" && -z "$VPN_IP" ]] && VPN_IP="$(primary_ip)"
  [[ -n "$VPN_IP" && -z "$SUB_IP" ]] && SUB_IP="$VPN_IP"
  if [[ "$VPN_IP" == "$SUB_IP" ]]; then VPN_IP="" SUB_IP=""; fi # one address: listen on all of them
  local ip
  for ip in $VPN_IP $SUB_IP; do
    list_ips | awk '{print $1}' | grep -qx "$ip" || die "$ip is not an address of this server (vynel net add-ip to add one)"
  done
  NAME="${NAME:-$(country_name "$COUNTRY")}"
  info "server $PUBLIC_IP, country ${COUNTRY:-?}, domain $DOMAIN, subscriptions on $SUB_DOMAIN"
  if [[ "$SUB_DOMAIN" != "$DOMAIN" ]]; then check_dns "$DOMAIN@${VPN_IP:-$PUBLIC_IP}" "$SUB_DOMAIN@${SUB_IP:-$PUBLIC_IP}"; else check_dns "$DOMAIN"; fi
  ensure_packages "${BASE_PKGS[@]}"
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
  setup_args+=(--vpn-ip "$VPN_IP" --sub-ip "$SUB_IP")
  admin setup "${setup_args[@]}"
  admin setting install.command "$(script_command)" >/dev/null
  set_paths
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
  wait_cert "$SUB_DOMAIN" "${SUB_IP:-$PUBLIC_IP}"
  apply_firewall
  apply_ssh

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
  print_security_block
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
    ask_security
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
  FIREWALL="${FIREWALL:-on}"
  check_dns "$DOMAIN"
  ensure_packages "${BASE_PKGS[@]}"
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
  set_paths
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
  apply_firewall
  apply_ssh

  echo
  pink "══════════════════════════  vynel установлен: панель  ══════════════════════════"
  echo
  line "Версия" "$(vynel version 2>/dev/null | awk '{print $2}')"
  line "Сервер" "$PUBLIC_IP"
  line "Сертификат" "$([[ $CERT_OK -eq 1 ]] && echo "выпущен" || echo "ещё нет — проверьте DNS и порт 80")"
  line "Сайт-заглушка" "https://$DOMAIN/"
  local sub_prefix
  sub_prefix="$(admin setting sub.prefix 2>/dev/null || true)"
  line "Подписки" "https://$DOMAIN${sub_prefix:-/s/}<токен>"
  line "Ноды подключаются" "$PUBLIC_IP:${GATEWAY_LISTEN##*:}"
  line "Данные" "$DATA  (бэкап: скопировать папку)"
  echo
  print_web_block
  echo
  print_security_block
  echo
  print_bot_block
  pink "  Дальше: добавьте VPN-ноду"
  echo "  1. В панели: «Ноды» → «Добавить сервер» → название, страна, домен ноды (A-запись на IP нового сервера)."
  echo "  2. Панель покажет команду вида:"
  echo "       $(script_command) --mode node --token vyn1.…"
  echo "  3. Выполните её на новом сервере под root. Чеклист в панели отметит каждый шаг сам."
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
    echo "  Токен выдаёт панель: «Ноды» → «Добавить сервер» (или «Новый токен» у ноды)."
    echo
    ask TOKEN "Токен (vyn1.…)" ""
    ask_security
  fi
  FIREWALL="${FIREWALL:-on}"
  TOKEN="$(printf '%s' "$TOKEN" | tr -d '[:space:]')"
  if [[ $joined -eq 0 ]]; then
    [[ "$TOKEN" == vyn1.* ]] || die "a join token from the panel is required (--token vyn1.…)"
  fi
  ensure_packages "${BASE_PKGS[@]}"
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
  apply_firewall
  apply_ssh

  echo
  pink "══════════════════════════  vynel установлен: нода  ══════════════════════════"
  echo
  line "Версия" "$(vynel version 2>/dev/null | awk '{print $2}')"
  line "Сервер" "$PUBLIC_IP"
  line "Служба" "$([[ $ok -eq 1 ]] && echo "запущена" || echo "не запустилась — journalctl -u vynel-node -e")"
  line "Данные" "$NODE_DATA"
  echo
  print_security_block
  echo
  echo "  Нода получает настройки от панели сама: домен, ключи Reality, пользователей."
  echo "  Проверьте в панели: «Ноды» — через минуту состояние «работает»."
  echo
  line "  логи" "journalctl -u vynel-node -f"
  line "  перезапуск" "systemctl restart vynel-node"
  line "  панель переехала" "vynel node set-panel НОВЫЙ_IP:9443"
  line "  ещё один IP" "vynel net add-ip 203.0.113.12"
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
  ensure_packages "${BASE_PKGS[@]}"
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
  apply_firewall
  apply_ssh
  echo
  print_security_block
  echo
}

# ================================================================== harden
do_harden() {
  detect_ip
  echo "  installed: $(installed_text) · $(vynel version 2>/dev/null || echo '?')"
  if [[ $WIZARD -eq 1 ]]; then
    ask_security
    if [[ "$(ssh_ports)" != 22 && -z "$SSH_PORT" ]]; then
      local cur
      cur="$(ssh_ports)"
      if [[ "$cur" == *" "* ]] && ask_yes "SSH слушает несколько портов ($cur). Оставить один?" y; then
        ask SSH_PORT "Какой оставить" "${cur##* }"
      fi
    fi
  fi
  [[ -n "$FIREWALL$SSH_KEYS_ONLY$SSH_PORT" ]] || FIREWALL=on
  # --ssh-port with the current second port: just close the others.
  if [[ -n "$SSH_PORT" && "$SSH_PORT" != random && " $(ssh_ports) " == *" $SSH_PORT "* ]]; then
    info "SSH: keeping only port $SSH_PORT"
    write_sshd "$SSH_PORT" && SSH_NOTE="порт $SSH_PORT"
    SSH_PORT=""
  fi
  apply_firewall
  apply_ssh
  echo
  pink "══════════════════════════  сервер защищён  ══════════════════════════"
  echo
  print_security_block
  echo
  line "  что открыто" "vynel firewall status"
  line "  открыть порт" "vynel firewall allow 8080"
  line "  снять блокировку" "vynel firewall unblock IP"
  line "  выключить" "vynel firewall off"
  echo
}

# ================================================================== the form
# setup_binary: a vynel that has the form — the installed one if it is new enough, else this
# release's, downloaded and checked like an install (and reused by it).
setup_binary() {
  if [[ -x /usr/local/bin/vynel ]] && /usr/local/bin/vynel setup-tui --help >/dev/null 2>&1; then
    SETUP_BIN=/usr/local/bin/vynel
    return 0
  fi
  local dir
  dir="$(mktemp -d /tmp/vynel-setup.XXXXXX)"
  info "downloading vynel for the setup form"
  release_sums "$dir" || return 1
  release_binary "$dir" || return 1
  "$dir/vynel" setup-tui --help >/dev/null 2>&1 || return 1
  SETUP_BIN="$dir/vynel"
}

# run_tui: the form; its answers become the flags of an unattended run.
run_tui() {
  setup_binary || return 1
  detect_ip
  detect_country
  local installed="" version="" out rc=0
  [[ $PANEL_WITH_NODE -eq 1 ]] && installed=aio
  [[ $PANEL_WITH_NODE -eq 0 && $PANEL_INSTALLED -eq 1 ]] && installed=panel
  [[ $NODE_INSTALLED -eq 1 ]] && installed=node
  [[ -n "$installed" ]] && version="$(vynel version 2>/dev/null || true)"
  out="$(mktemp)"
  "$SETUP_BIN" setup-tui --out "$out" --installed "$installed" --version "$version" \
    --public-ip "$PUBLIC_IP" --country "$COUNTRY" --domain "$DOMAIN" --email "$EMAIL" --token "$TOKEN" </dev/tty >/dev/tty || rc=$?
  if [[ $rc -eq 2 ]]; then rm -f "$out"; die "отменено"; fi
  if [[ $rc -ne 0 ]]; then rm -f "$out"; return 1; fi
  # Only known names, single-quoted values (internal/setup Answers.Shell).
  if grep -qvE "^(MODE|DOMAIN|SUB_DOMAIN|EMAIL|NAME|COUNTRY|PUBLIC_IP|VPN_IP|SUB_IP|GATEWAY_LISTEN|ADMIN_LOGIN|BOT_TOKEN|RESTORE|TOKEN|FIREWALL|SSH_KEYS_ONLY|SSH_PORT|PURGE|SUB_PATH|PANEL_PATH)='" "$out"; then
    rm -f "$out"
    return 1
  fi
  # shellcheck disable=SC1090
  source "$out"
  rm -f "$out"
  TUI=1 ASSUME_YES=1 WIZARD=0
  [[ -z "$SUB_DOMAIN" ]] && SUB_DOMAIN="$DOMAIN"
  check_mode
}

if [[ "$MODE" == tui ]]; then
  if ! run_tui; then
    red "  the setup form is not available here, using plain questions"
    MODE=""
    bash_menu
    check_mode
  fi
fi

case "$MODE" in
  uninstall) do_uninstall ;;
  aio) do_aio ;;
  panel) do_panel ;;
  node) do_node ;;
  update) do_update ;;
  harden) do_harden ;;
esac
