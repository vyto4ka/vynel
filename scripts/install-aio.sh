#!/usr/bin/env bash
# All-in-one install: panel + node + Caddy on ONE server and ONE IP (docs/ALL_IN_ONE.md).
#
#   bash <(curl -fsSL https://raw.githubusercontent.com/vyto4ka/vynel/claude/magical-hamilton-9vnx7n/scripts/install-aio.sh)
#
# Without --domain it asks questions (domain, subscriptions, email, name...). With flags it runs
# unattended:  ... install-aio.sh --domain nl.example.com [--sub-domain sub.example.com] [--email you@example.com] --yes
#
# Re-running it updates the code and keeps the data. The vynel binary comes from the branch's
# "edge" release (CI); if there is none, it is built from source (slow on small servers).
set -euo pipefail

REPO="${VYNEL_REPO:-https://github.com/vyto4ka/vynel.git}"
REF="${VYNEL_REF:-claude/magical-hamilton-9vnx7n}"
SRC=/opt/vynel-src
DATA=/var/lib/vynel
UNIT=/etc/systemd/system/vynel.service

DOMAIN="" SUB_DOMAIN="" EMAIL="" NAME="" COUNTRY="" PUBLIC_IP="" GATEWAY_LISTEN=":9443" ADMIN_LOGIN="admin"
ASSUME_YES=0 UNINSTALL=0 PURGE=0 WIZARD=0

red() { printf '\033[31m%s\033[0m\n' "$*"; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
info() { printf '\033[36m==>\033[0m %s\n' "$*"; }
die() { red "error: $*" >&2; exit 1; }

usage() {
  cat <<EOF
Usage: install-aio.sh                       (interactive: asks questions)
       install-aio.sh --domain DOMAIN [options] [--yes]

  --domain DOMAIN        node domain, A record -> this server
  --sub-domain DOMAIN    subscription domain (default: the node domain; one domain is enough)
  --email EMAIL          email for Let's Encrypt (optional)
  --name NAME            server name shown in VPN clients (default: country name or "VPN")
  --country CC           2-letter country code for the flag (default: detected)
  --ip IP                public IP (default: detected)
  --gateway-listen ADDR  port for additional nodes (default :9443; 127.0.0.1:9443 = none)
  --admin-login LOGIN    web panel login (default admin; the password is generated)
  --interactive          ask questions even if flags are given
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
    --admin-login) ADMIN_LOGIN="${2:-}"; shift 2 ;;
    --yes|-y) ASSUME_YES=1; shift ;;
    --interactive|-i) WIZARD=1; shift ;;
    --uninstall) UNINSTALL=1; shift ;;
    --purge) PURGE=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage; die "unknown option $1" ;;
  esac
done

[[ $EUID -eq 0 ]] || die "run as root"

# confirm "question": yes only on an explicit answer; without a terminal only with --yes.
confirm() {
  [[ $ASSUME_YES -eq 1 ]] && return 0
  local answer=""
  { read -r -p "$1 [y/N] " answer </dev/tty; } 2>/dev/null || { red "no terminal to ask: add --yes"; return 1; }
  [[ "$answer" =~ ^[YyДд] ]]
}

if [[ $UNINSTALL -eq 1 ]]; then
  info "removing vynel service"
  systemctl disable --now vynel 2>/dev/null || true
  rm -f "$UNIT"
  systemctl daemon-reload
  if [[ $PURGE -eq 1 ]]; then
    confirm "delete $DATA (users, keys, certificates)?" || die "aborted"
    rm -rf "$DATA" /etc/sysctl.d/90-vynel.conf
  fi
  green "removed"
  exit 0
fi

have_tty() { [[ -r /dev/tty ]] && { : </dev/tty; } 2>/dev/null; }
if [[ -z "$DOMAIN" ]]; then
  have_tty || { usage; die "--domain is required when there is no terminal to ask questions"; }
  WIZARD=1
fi
[[ $WIZARD -eq 1 && $ASSUME_YES -eq 1 ]] && die "--yes and the interactive mode do not mix"

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
  PUBLIC_IP="$(curl -4 -fsS --max-time 10 https://api.ipify.org 2>/dev/null || true)"
  [[ -n "$PUBLIC_IP" ]] || PUBLIC_IP="$(ip -4 route get 1.1.1.1 | awk '{for (i=1;i<NF;i++) if ($i=="src") print $(i+1)}')"
fi
[[ -n "$PUBLIC_IP" ]] || die "cannot detect the public IP, pass --ip"
if [[ -z "$COUNTRY" ]]; then
  COUNTRY="$(curl -fsS --max-time 5 "https://ipinfo.io/${PUBLIC_IP}/country" 2>/dev/null | tr -d '[:space:]' || true)"
  [[ "$COUNTRY" =~ ^[A-Za-z]{2}$ ]] || COUNTRY=""
fi
COUNTRY="$(printf '%s' "$COUNTRY" | tr '[:lower:]' '[:upper:]')"

# ---- interactive questions ----
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

# ask VAR "question" "default"
ask() {
  local answer prompt="$2"
  [[ -n "$3" ]] && prompt+=" [$3]"
  read -r -p "  $prompt: " answer </dev/tty || true
  printf -v "$1" '%s' "${answer:-$3}"
}

# ask_yes "question" y|n
ask_yes() {
  local answer hint="y/N"
  [[ "$2" == y ]] && hint="Y/n"
  read -r -p "  $1 [$hint]: " answer </dev/tty || true
  answer="${answer:-$2}"
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

if [[ $WIZARD -eq 1 ]]; then
  echo
  green "Установка vynel: панель + нода на этом сервере"
  echo "  Enter — оставить значение в скобках."
  echo
  ask PUBLIC_IP "Публичный IP сервера" "$PUBLIC_IP"
  ask_domain DOMAIN "Домен сервера (A-запись на этот IP; на нём VPN, сайт-заглушка и подписки)" "$DOMAIN"
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
  [[ "$ADMIN_LOGIN" =~ ^[A-Za-z0-9_.@-]{1,64}$ ]] || die "логин — латиница, цифры и _ . - @"
  SUB_DOMAIN="${SUB_DOMAIN:-$DOMAIN}"
  echo
  echo "  Сервер:    $PUBLIC_IP"
  echo "  Домен:     $DOMAIN"
  echo "  Подписки:  https://$SUB_DOMAIN/s/…"
  echo "  Клиенты:   ${NAME} (${COUNTRY:-без флага})"
  echo "  Email:     ${EMAIL:-—}"
  echo "  Панель:    логин $ADMIN_LOGIN, пароль будет показан в конце"
  echo "  Ноды:      $([[ "$GATEWAY_LISTEN" == 127.0.0.1:* ]] && echo "только этот сервер" || echo "порт 9443 открыт")"
  echo
  ask_yes "Устанавливаем?" y || die "отменено"
fi

[[ -n "$DOMAIN" ]] || { usage; die "--domain is required"; }
SUB_DOMAIN="${SUB_DOMAIN:-$DOMAIN}"
NAME="${NAME:-$(country_name "$COUNTRY")}"
info "server $PUBLIC_IP, country ${COUNTRY:-?}, domain $DOMAIN, subscriptions on $SUB_DOMAIN"

domains=("$DOMAIN")
[[ "$SUB_DOMAIN" != "$DOMAIN" ]] && domains+=("$SUB_DOMAIN")
for d in "${domains[@]}"; do
  resolved="$(getent ahostsv4 "$d" | awk 'NR==1 {print $1}' || true)"
  if [[ "$resolved" != "$PUBLIC_IP" && $WIZARD -eq 0 ]]; then
    red "DNS: $d -> ${resolved:-nothing}, expected $PUBLIC_IP"
    red "     create an A record $d -> $PUBLIC_IP, otherwise the certificate cannot be issued"
    confirm "continue anyway?" || die "aborted"
  fi
done

# Ports 80/443 must be free (except for our own service when updating).
systemctl stop vynel 2>/dev/null || true
busy="$(ss -Hltnp '( sport = :443 or sport = :80 )' 2>/dev/null || true)"
if [[ -n "$busy" ]]; then
  red "ports 80/443 are in use:"
  echo "$busy"
  die "stop the web server / old VPN using them (e.g. systemctl disable --now nginx) and run again"
fi

# ---- binaries ----
# vynel: a prebuilt binary from the branch's "edge" release (built by .github/workflows/edge.yml).
# Building from source is the fallback only: on a 1 vCPU / 1 GB VPS it takes 10–20 minutes.
RELEASE="${VYNEL_RELEASE:-edge-${REF//\//-}}"
RELEASE_URL="https://github.com/vyto4ka/vynel/releases/download/$RELEASE"

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

TEMP_SWAP=0
info "downloading vynel ($RELEASE)"
# Right after a push the release is being re-created for about a minute: retry before building.
got=0
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

install_xray() {
  local asset tmp
  case "$GOARCH" in amd64) asset=Xray-linux-64.zip ;; arm64) asset=Xray-linux-arm64-v8a.zip ;; esac
  tmp="$(mktemp)"
  curl -fsSL -o "$tmp" "https://github.com/XTLS/Xray-core/releases/latest/download/$asset"
  mkdir -p /usr/local/share/xray
  unzip -o -q "$tmp" -d /usr/local/share/xray
  rm -f "$tmp"
  chmod +x /usr/local/share/xray/xray
}
if [[ ! -x /usr/local/share/xray/xray ]]; then
  info "installing Xray"
  install_xray
fi
ln -sf /usr/local/share/xray/xray /usr/local/bin/xray

if [[ ! -x /usr/local/bin/caddy ]]; then
  info "installing Caddy"
  CV="$(curl -fsSL https://api.github.com/repos/caddyserver/caddy/releases/latest 2>/dev/null | grep -o '"tag_name": *"v[^"]*"' | grep -o 'v[0-9.]*' || true)"
  if [[ -n "$CV" ]] && curl -fsSL "https://github.com/caddyserver/caddy/releases/download/${CV}/caddy_${CV#v}_linux_${GOARCH}.tar.gz" | tar -xz -C /usr/local/bin caddy; then
    :
  else
    red "  Caddy download failed, building it from source"
    ensure_go
    ensure_memory
    with_progress "building Caddy (several minutes)" env GOBIN=/usr/local/bin go install github.com/caddyserver/caddy/v2/cmd/caddy@latest
  fi
fi
chmod +x /usr/local/bin/caddy

if [[ $TEMP_SWAP -eq 1 ]]; then
  swapoff /swapfile-vynel && rm -f /swapfile-vynel
fi

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
vynel admin --data-dir "$DATA" setup "${setup_args[@]}"
# The admin is created once; on updates the login and password stay as they are.
WEB_INFO="$(vynel admin --data-dir "$DATA" web init --login "$ADMIN_LOGIN")"
WEB_URL="$(awk '$1=="url" {print $2}' <<<"$WEB_INFO")"
WEB_LOGIN="$(awk '$1=="login" {print $2}' <<<"$WEB_INFO")"
WEB_PASSWORD="$(awk '$1=="password" && NF==2 {print $2}' <<<"$WEB_INFO")"

cat >"$UNIT" <<EOF
[Unit]
Description=VPN panel with a local node (Xray + Caddy)
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/vynel panel --data-dir $DATA --gateway-listen $GATEWAY_LISTEN --public-addr $PUBLIC_IP:${GATEWAY_LISTEN##*:} --with-node
Restart=always
RestartSec=3
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable --now vynel >/dev/null 2>&1
systemctl restart vynel

info "waiting for the node to start"
ok=0
for _ in $(seq 1 60); do
  if vynel admin --data-dir "$DATA" node list 2>/dev/null | grep -q "in sync"; then ok=1; break; fi
  sleep 2
done
vynel admin --data-dir "$DATA" node list || true
[[ $ok -eq 1 ]] || red "the node is not in sync yet: journalctl -u vynel -e"

info "waiting for the certificate of $SUB_DOMAIN"
cert=0
for _ in $(seq 1 45); do
  code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 --resolve "$SUB_DOMAIN:443:$PUBLIC_IP" "https://$SUB_DOMAIN/" || true)"
  if [[ "$code" == "200" ]]; then cert=1; break; fi
  sleep 2
done
[[ $cert -eq 1 ]] || red "https://$SUB_DOMAIN/ does not answer yet (DNS or port 80 not reachable?). Caddy keeps retrying; see journalctl -u vynel"

VERSION="$(vynel version 2>/dev/null | awk '{print $2}')"
SUB_PREFIX="$(vynel admin --data-dir "$DATA" setting sub.prefix 2>/dev/null)"
SUB_PREFIX="${SUB_PREFIX:-/s/}"
NODE_STATE="$([[ $ok -eq 1 ]] && echo "работает" || echo "запускается — см. логи")"
CERT_STATE="$([[ $cert -eq 1 ]] && echo "выпущен" || echo "ещё нет — проверьте DNS и порт 80")"
NODES_STATE="$([[ "$GATEWAY_LISTEN" == 127.0.0.1:* ]] && echo "закрыто (только этот сервер)" || echo "порт ${GATEWAY_LISTEN##*:} открыт")"

# Pads by characters, not bytes (Cyrillic is two bytes per letter in UTF-8).
line() {
  local n pad
  n="$(printf '%s' "$1" | LC_ALL=C.UTF-8 wc -m 2>/dev/null || printf '%s' "$1" | wc -c)"
  pad=$(( 26 - n )); (( pad < 1 )) && pad=1
  printf '  %s%*s%s\n' "$1" "$pad" "" "$2"
}
echo
green "══════════════════════════  vynel установлен  ══════════════════════════"
echo
line "Версия" "$VERSION"
line "Сервер" "$PUBLIC_IP ($NAME${COUNTRY:+, $COUNTRY})"
line "Нода (VPN)" "$DOMAIN:443 — $NODE_STATE"
line "Сертификат" "$CERT_STATE"
line "Сайт-заглушка" "https://$DOMAIN/"
line "Подписки" "https://$SUB_DOMAIN${SUB_PREFIX}<токен>"
line "Подключение других нод" "$NODES_STATE"
line "Данные" "$DATA  (бэкап: скопировать папку)"
echo
green "  Веб-панель"
line "  адрес" "$WEB_URL"
line "  логин" "$WEB_LOGIN"
if [[ -n "$WEB_PASSWORD" ]]; then
  line "  пароль" "$WEB_PASSWORD"
  red "  Сохраните пароль: он показывается один раз. Адрес секретный — без него панель не найти."
else
  line "  пароль" "прежний (новый: vynel admin web password)"
fi
echo
echo "  Пользователи — в панели: «Пользователи» → «+ Пользователь», ввести имя, скопировать ссылку."
echo "  То же из терминала:"
line "  добавить" "vynel admin user add vasya"
line "  ссылка подписки" "vynel admin user show vasya"
line "  список" "vynel admin user list"
line "  продлить" "vynel admin user extend vasya --months 1"
line "  отключить / включить" "vynel admin user disable vasya / enable vasya"
line "  устройства (HWID)" "vynel admin user devices vasya [--rm ID]"
echo
echo "  Сервер"
line "  статистика" "vynel admin stats"
line "  состояние ноды" "vynel admin node list"
line "  настройки" "vynel admin setting hwid.default_limit 5"
line "  адрес и логин панели" "vynel admin web"
line "  новый пароль панели" "vynel admin web password"
line "  все команды" "vynel admin --help"
line "  логи" "journalctl -u vynel -f"
line "  перезапуск" "systemctl restart vynel"
line "  обновить" "запустить этот скрипт ещё раз"
line "  удалить" "install-aio.sh --uninstall [--purge]"
echo
echo "  Ссылку подписки откройте на телефоне — там QR-код и кнопки для Happ, v2RayTun, Hiddify."
echo "  Подробно: https://github.com/vyto4ka/vynel/blob/$REF/docs/ALL_IN_ONE.md"
echo
