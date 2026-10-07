# Шаблоны профилей

> **Статус:** шаблоны A, B и C, переменные, наследование, lint, Caddy и проверки конфигов реализованы. Свои шаблоны пишутся в веб-панели («Профили» → «Шаблоны профилей»): копия встроенного → правка YAML → проверка пробной сборкой; хранятся в базе панели (таблица `profile_templates`). Не реализованы: внешние шаги VK на странице-инструкции (§4.6), автопроверки edge CDN (§4.7), режим отладки (§2, §4.9).

Описание встроенных шаблонов профилей Xray: технические характеристики, что используется и как это автоматизировано.
Основа — две реальные рабочие схемы:

- **A. VLESS + Reality, self-steal.** Как ставилось через Remnawave + RemnaSetup (Remnanode → «полная установка»: Caddy self-steal + BBR, без WARP).
- **B. VLESS + XHTTP через VK Cloud CDN.** По гайду «Узел xHTTP за VK Cloud CDN» (packet-up, данные в куках, набивка в query).
- **C. VLESS + XHTTP + REALITY, self-steal.** По ТЗ «XHTTP + REALITY» (stream-up, резервный packet-up): XHTTP напрямую на ноду, без CDN, под маской своего сайта (§8).

Цель — чтобы в нашей панели оба профиля ставились **выбором шаблона и заполнением 1–3 полей**. Всё, что в Remnawave делается руками (ключи, shortIds, SNI, extra в хосте, nginx/certbot, BBR, видимость хоста), делает панель.

---

## 1. Как устроен шаблон

### 1.1 Понятия

```
Шаблон (встроенный, версионируется вместе с кодом)
  └─ описывает: переменные, кусок Xray (инбаунд), кусок Caddy, точку подключения,
               требования к ноде, внешние шаги, проверки

Профиль (создаётся админом из шаблона, хранится в БД) — общий конфиг
  = значения по умолчанию для всех своих инбаундов + шаблон тега ('VLESS_${NODE_CODE}')

Инбаунд ноды (создаётся автоматически при добавлении профиля на ноду) — «свой VLESS-профиль ноды»
  = профиль + свой тег (VLESS_NL) + переменные ноды (домен, ключи, shortId) + IP + локальные override

Нода
  = базовый конфиг (log, dns, outbounds, routing) + 1..N инбаундов нод (A, B, A+B, A на двух IP...)
```

Полностью модель наследования, доступ групп и несколько IP на сервере описаны в [INBOUNDS.md](INBOUNDS.md).

Чем это лучше Remnawave, где профиль — это просто JSON:

- В Remnawave на каждую ноду делают свой профиль (`NL` с `VLESS_NL`, `DE` с `VLESS_DE`), копируя JSON и правя `serverNames` и ключ. У нас остаётся **свой инбаунд на каждую ноду** (`VLESS_NL`, `VLESS_DE`, со своими ключами и тегом), но он **создаётся сам из одного общего профиля**. Общие настройки меняются в одном месте.
- Группа может ссылаться как на конкретный `VLESS_NL`, так и на «профиль целиком». Во втором случае новые ноды попадают в группу автоматически.

### 1.2 Переменные

У каждой переменной есть:

| Свойство | Значения | Зачем |
|----------|----------|-------|
| `scope` | `profile` / `node` | Общая для всех нод или своя на каждой |
| `source` | `input` / `generate` / `node.domain` / `node.ip` / `const` | Откуда берётся значение |
| `generator` | `x25519`, `shortid`, `hex(n)`, `path(set)`, `port(range)` | Как сгенерировать (при создании ноды или профиля) |
| `secret` | bool | Скрывать в UI, не показывать в логах |
| `validate` | `domain`, `port`, `path`, `regex` | Проверка ввода |

Сгенерированные значения создаются **один раз** и хранятся в БД. Перегенерировать можно кнопкой, это поднимет ревизию ноды.

### 1.3 Формат файла шаблона

Шаблоны лежат в репозитории (`templates/profiles/*.yaml`) и вшиваются в бинарь. Пользовательские шаблоны можно добавить через UI (импорт YAML).

```yaml
id: vless-reality-selfsteal
version: 1
title: "VLESS Reality (self-steal)"
summary: "Основной профиль. Нода маскируется под собственный сайт на своём домене."
variables: [...]          # §1.2
xray_inbound: {...}       # JSON-фрагмент с ${VAR}
caddy: {...}              # что должен поднять Caddy ноды
host: {...}               # точка подключения по умолчанию (что уйдёт в подписку)
node_requirements: {...}  # порты, DNS, тюнинг ядра
external_steps: [...]     # шаги вне сервера (DNS, CDN), попадают на страницу-инструкцию
checks: [...]             # автоматические проверки после установки
lint: [...]               # дополнительные правила валидации
```

---

## 2. Базовый конфиг (общий для всех профилей)

Взят из обеих схем и объединён. Редактируется в профиле галочками; режим «JSON» доступен для продвинутых.

```json
{
  "log": { "loglevel": "none" },
  "dns": {
    "servers": [ { "address": "https://dns.google/dns-query", "skipFallback": false } ],
    "queryStrategy": "UseIPv4"
  },
  "outbounds": [
    { "tag": "DIRECT", "protocol": "freedom", "settings": { "domainStrategy": "UseIPv4" } },
    { "tag": "BLOCK",  "protocol": "blackhole" }
  ],
  "routing": {
    "rules": [
      { "type": "field", "ip": ["geoip:private"],          "outboundTag": "BLOCK" },
      { "type": "field", "domain": ["geosite:private"],    "outboundTag": "BLOCK" },
      { "type": "field", "protocol": ["bittorrent"],       "outboundTag": "BLOCK" },
      { "type": "field", "network": "tcp,udp",             "outboundTag": "DIRECT" }
    ]
  }
}
```

| Галочка в UI | Что делает | По умолчанию |
|--------------|-----------|--------------|
| Блокировать локальные сети | правила `geoip:private` / `geosite:private` | вкл |
| Блокировать BitTorrent | правило `protocol: bittorrent` | вкл |
| Только IPv4 наружу | `domainStrategy: UseIPv4`, `queryStrategy: UseIPv4` | вкл |
| DNS-over-HTTPS | сервер DoH (Google / Cloudflare / свой) | Google |
| Уровень логов | `none` / `warning` / `info` | `none` |

Что панель **добавляет сама при рендере** (в редакторе этого нет):
- секции `api`, `stats`, `policy` (`statsUserUplink/Downlink`, `statsUserOnline`);
- API-инбаунд `127.0.0.1:<random>` и правило роутинга к нему;
- `clients` в каждом инбаунде (из пользователей групп).

**Режим отладки ноды** (кнопка в карточке ноды, по умолчанию на 15 минут): `loglevel: info`, лог Xray и access-лог Caddy стримятся в UI. По таймеру всё откатывается само. Это заменяет из гайда ручную правку `log` в профиле, `docker logs`, `tail -f` и `tcpdump`.

---

## 3. Шаблон A — VLESS + Reality (self-steal)

### 3.1 Технические характеристики

| Параметр | Значение |
|----------|----------|
| Протокол | VLESS, `decryption: none` |
| Транспорт | `raw` (бывший `tcp`) |
| Безопасность | REALITY, `target` = локальный Caddy (`127.0.0.1:8443`) |
| Flow | `xtls-rprx-vision` (ставится каждому клиенту) |
| Порт снаружи | `443/tcp` |
| Маскировка | self-steal: SNI = собственный домен ноды, за ним живой сайт с настоящим сертификатом Let's Encrypt |
| Sniffing | `http`, `tls`, `quic`, `routeOnly: true` |
| Sockopt | `tcpFastOpen`, `tcpcongestion: bbr`, `tcpKeepAliveIdle: 45`, `tcpKeepAliveInterval: 45` |
| Fingerprint клиента | `firefox` (как в рабочей схеме; меняется в точке подключения) |
| Что нужно | VPS с IP, не заблокированным в РФ; домен или поддомен с A-записью на IP ноды |
| Компоненты на ноде | Xray, Caddy (сайт-заглушка + ACME), BBR |

### 3.2 Переменные

| Переменная | scope | source | Пример | Комментарий |
|------------|-------|--------|--------|-------------|
| `NODE_DOMAIN` | node | домен IP инбаунда (INBOUNDS.md §2) | `nl2.vyto4ka.ru` | Вводится один раз, идёт в `serverNames` и в адрес точки |
| `TAG` | node | `tag_pattern` профиля | `VLESS_NL` | Свой на каждой ноде |
| `LISTEN_IP` | node | адрес инбаунда | `203.0.113.12` | По умолчанию основной IP; см. INBOUNDS.md §2 |
| `REALITY_PRIVATE_KEY` | node | generate `x25519` | — | secret. Публичный ключ вычисляется и уходит в подписку |
| `REALITY_SHORT_ID` | node | generate `shortid` (8 байт hex) | `a1b2c3d4e5f60718` | Вместо `for i in {1..20}; do head -c 8 /dev/urandom \| xxd -p; done` |
| `SELFSTEAL_PORT` | profile | const | `8443` | Локальный порт Caddy |
| `PORT` | profile | const | `443` | |
| `DECOY_SITE` | node | generate `pick(templates)` | `cloud-storage` | Какой шаблон сайта-заглушки показать |

Админу в итоге нужно ввести **только домен ноды**. Остальное генерируется.

### 3.3 Xray-инбаунд (фрагмент шаблона)

```json
{
  "tag": "${TAG}",
  "port": 443,
  "listen": "${LISTEN_IP}",
  "protocol": "vless",
  "settings": { "clients": [], "decryption": "none" },
  "sniffing": { "enabled": true, "routeOnly": true, "destOverride": ["http", "tls", "quic"] },
  "streamSettings": {
    "network": "raw",
    "sockopt": {
      "tcpFastOpen": true,
      "tcpcongestion": "bbr",
      "tcpKeepAliveIdle": 45,
      "tcpKeepAliveInterval": 45
    },
    "security": "reality",
    "realitySettings": {
      "xver": 0,
      "target": "127.0.0.1:${SELFSTEAL_PORT}",
      "serverNames": ["${NODE_DOMAIN}"],
      "privateKey": "${REALITY_PRIVATE_KEY}",
      "shortIds": ["${REALITY_SHORT_ID}"]
    }
  }
}
```

Клиенты при рендере: `{ "id": user.uuid, "email": user.id, "flow": "xtls-rprx-vision" }`.

### 3.4 Caddy на ноде

Эквивалент Caddyfile (на ноду уходит JSON через Admin API):

```caddy
{
    https_port 8443
    default_bind 127.0.0.1          # self-steal-сайт снаружи не торчит, только через Reality
    servers { protocols h1 h2 }
}

http://${NODE_DOMAIN} {
    bind 0.0.0.0                    # :80 наружу — ACME HTTP-01 и редирект
    redir https://${NODE_DOMAIN}{uri} permanent
}

https://${NODE_DOMAIN}:8443 {
    tls {
        issuer acme { disable_tlsalpn_challenge }   # 443 занят Xray → только HTTP-01
    }
    root * /var/lib/vynel-node/decoy/${DECOY_SITE}
    file_server
    header -Server
}
```

Как это работает: браузер или сканер приходит на `nl2.vyto4ka.ru:443`. Reality не видит валидной авторизации и прозрачно передаёт TCP в `127.0.0.1:8443`. Caddy отдаёт настоящий сайт с настоящим сертификатом этого домена.

### 3.5 Точка подключения (подписка) — заполняется автоматически

| Поле | Значение | В Remnawave было |
|------|----------|------------------|
| Название | `🇳🇱 Нидерланды` (флаг из страны ноды + название) | вручную, эмодзи руками |
| Адрес | `${NODE_DOMAIN}` | вручную |
| Порт | `443` | вручную |
| SNI | = адрес | галочка «брать SNI из адреса» |
| Fingerprint | `firefox` | вручную в «опциях» |
| publicKey, shortId, flow | из переменных ноды | подставляла Remnawave |
| Видимость | **включена сразу** | по умолчанию выключена, легко забыть («20 минут искал, почему не видно») |
| Группы | выбираются в шаге 3 мастера | отдельно «добавить хост в сквад» |

Итоговая ссылка:
`vless://UUID@nl2.vyto4ka.ru:443?type=tcp&security=reality&sni=nl2.vyto4ka.ru&fp=firefox&pbk=PUB&sid=SID&flow=xtls-rprx-vision#🇳🇱 Нидерланды`

### 3.6 Требования к ноде и тюнинг

| Что | Значение |
|-----|----------|
| Порты наружу | `443/tcp` (Xray), `80/tcp` (ACME) |
| DNS | A-запись `${NODE_DOMAIN}` → IP ноды |
| BBR | `net.core.default_qdisc=fq`, `net.ipv4.tcp_congestion_control=bbr` (§6) |
| Время | синхронизация NTP (chrony или systemd-timesyncd). При рассинхроне Reality отбрасывает клиентов |
| WARP | не ставим (как в рабочей схеме). Возможен будущий модуль outbound |

### 3.7 Проверки после установки

| # | Проверка | Откуда | Ожидание |
|---|----------|--------|----------|
| 1 | `getent hosts ${NODE_DOMAIN}` = IP ноды | панель + страница-инструкция | ✓ до запуска скрипта |
| 2 | Caddy получил сертификат | нода | сертификат для `${NODE_DOMAIN}` в хранилище Caddy |
| 3 | TLS на `:443` без Reality-клиента отдаёт сертификат `${NODE_DOMAIN}` | панель | маскировка работает |
| 4 | `xray` слушает `:443`, инбаунд `vless-reality` с N клиентами | нода | |
| 5 | BBR активен (`sysctl net.ipv4.tcp_congestion_control`) | нода | `bbr` |
| 6 | Самотест: служебный пользователь, запрос через ноду | панель | 204, задержка в мс |
| 7 | Доступность из РФ | **вручную**, подсказка на странице | «подключитесь к серверу по SSH без VPN: если спрашивает fingerprint, IP не заблокирован» |

### 3.8 Что было руками → что делает панель

| Шаг в Remnawave + RemnaSetup | У нас |
|------------------------------|-------|
| Скопировать JSON профиля, поменять 4 поля (тег, shortIds, privateKey, serverNames) | Выбрать шаблон. Ключи и shortId генерируются, домен берётся из ноды, тег `VLESS_NL` создаётся по шаблону профиля |
| Генерировать shortIds в консоли | Генератор |
| «Три полоски → сгенерировать пару ключей» | Генератор при создании ноды |
| `apt update && apt upgrade -y && reboot` | Опция `--upgrade` в команде установки (по умолчанию выкл, предупреждение о перезагрузке) |
| Нода в панели: страна, название, адрес, порт 3001, SECRET_KEY | Страна, название, домен. Ни порта, ни ключа: нода сама подключается к панели |
| RemnaSetup: язык → 2 → 1 → домен → порт → caddy → порт → SECRET_KEY → WARP n → n → BBR y | Одна команда без вопросов, всё из токена |
| Выбрать инбаунд для ноды | Включено по умолчанию, если в профиле один инбаунд |
| Хост: имя с флагом, адрес, порт, SNI из адреса, firefox, видимость, сквад | Создаётся сам, видим сразу, группы выбираются галочками |

---

## 4. Шаблон B — VLESS + XHTTP через VK Cloud CDN

### 4.1 Технические характеристики

| Параметр | Значение |
|----------|----------|
| Протокол | VLESS, `decryption: none` |
| Транспорт | XHTTP, `mode: packet-up` |
| Цепочка | Клиент → **VK Cloud CDN** (edge MegaFon, TLS LE) → HTTPS → **Caddy** на ноде (`ORIGIN_DOMAIN`, TLS LE) → HTTP → **Xray** `127.0.0.1:${XRAY_PORT}` |
| Ограничения CDN | Пропускает только `GET`/`HEAD`, вырезает длинные нестандартные заголовки (`X-Padding` и т.п.), по умолчанию кэширует |
| Как обходим | Данные вверх в куках `pb_0, pb_1, …`, набивка в query `?cb=…`, сессия в куке `media_sid`, номер пакета в query `?offset=N`, без SSE-заголовка |
| Flow | нет |
| Порт снаружи | `443/tcp` (Caddy) или общий с Reality (§5) |
| Fingerprint клиента | `chrome`, ALPN `h2,http/1.1` |
| Что нужно | Домен источника (A-запись на ноду), персональный CDN-домен (CNAME на VK), аккаунт VK Cloud |
| Компоненты на ноде | Xray, Caddy (TLS + reverse proxy), BBR |
| Когда использовать | Когда прямые IP нод блокируются, а CDN VK доступен (белые списки, мобильные сети) |

### 4.2 Переменные

| Переменная | scope | source | Пример | Комментарий |
|------------|-------|--------|--------|-------------|
| `ORIGIN_DOMAIN` | node | input (по умолчанию = `node.domain`) | `origin.example.com` | Домен источника, A-запись на ноду |
| `CDN_DOMAIN` | node | input | `cdn.example.com` | Персональный домен в VK CDN, на него CNAME |
| `VK_CNAME` | node | input (необязательно) | `cl-xxxx.service.cdn.msk.vkcs.cloud` | Только для проверки DNS |
| `XRAY_PORT` | profile | const | `8085` | Локальный порт, наружу не торчит |
| `UPLINK_PATH` | profile | generate `path(["/upload","/hls","/segment","/media"])` | `/upload` | Без слеша на конце; слеш в точке подключения добавляется сам |
| `MAX_HEADER_BYTES` | profile | const | `131072` | Лимит заголовков на сервере (большие куки) |

Админ вводит **два домена**. Все параметры `extra` зашиты в шаблон и совпадают на сервере и клиенте автоматически.

### 4.3 Xray-инбаунд

```json
{
  "tag": "${TAG}",
  "port": ${XRAY_PORT},
  "listen": "127.0.0.1",
  "protocol": "vless",
  "settings": { "clients": [], "decryption": "none" },
  "streamSettings": {
    "network": "xhttp",
    "sockopt": { "tcpNoDelay": true },
    "xhttpSettings": {
      "mode": "packet-up",
      "path": "${UPLINK_PATH}",
      "extra": {
        "seqKey": "offset",            "seqPlacement": "query",
        "sessionKey": "media_sid",     "sessionIDKey": "media_sid",
        "sessionPlacement": "cookie",  "sessionIDPlacement": "cookie",
        "uplinkHTTPMethod": "GET",
        "uplinkDataPlacement": "cookie", "uplinkDataKey": "pb",
        "xPaddingObfsMode": true, "xPaddingPlacement": "query", "xPaddingKey": "cb",
        "noSSEHeader": true,
        "scMaxEachPostBytes": "4096-8192",
        "scMinPostsIntervalMs": "5-10",
        "serverMaxHeaderBytes": ${MAX_HEADER_BYTES}
      }
    }
  }
}
```

В шаблоне блок `extra` хранится **отдельно** как `xhttp_extra_shared`. Из него рендерятся и инбаунд, и точка подключения, поэтому рассинхрон «сервер ↔ хост» невозможен.

### 4.4 Caddy на ноде (вместо nginx + certbot)

```caddy
https://${ORIGIN_DOMAIN} {
    reverse_proxy 127.0.0.1:${XRAY_PORT} {
        flush_interval -1                 # = proxy_buffering off
        request_buffers 0                 # = proxy_request_buffering off
        transport http {
            versions 1.1
            read_timeout  3600s           # = proxy_read_timeout
            write_timeout 3600s           # = proxy_send_timeout
        }
    }
    request_body { max_size 0 }           # = client_max_body_size 0
    log { output file /var/log/vynel-node/xhttp_access.log }   # только в режиме отладки
}
```

Соответствие гайду:

| nginx в гайде | Caddy у нас |
|---------------|-------------|
| `certbot certonly --standalone` | автоматический ACME (HTTP-01 на `:80`) |
| `listen 443 ssl http2` (+ ловушка с `http2 on` в 1.18) | h2 по умолчанию, версия ни при чём |
| `large_client_header_buffers 16 16k` | лимит заголовков Go/Caddy 1 МБ по умолчанию, хватает с запасом. Явно задаём `servers { max_header_size 256KB }` |
| `proxy_set_header Host $host` | Caddy сохраняет Host по умолчанию |
| `proxy_set_header Connection ""` | Caddy управляет hop-by-hop заголовками сам |

### 4.5 Точка подключения — заполняется автоматически

| Поле | Значение |
|------|----------|
| Название | `🇳🇱 Нидерланды · CDN` |
| Адрес | `${CDN_DOMAIN}` |
| Порт | `443` |
| Security | TLS |
| SNI, Host | `${CDN_DOMAIN}` |
| Path | `${UPLINK_PATH}/` (со слешем, добавляется сам) |
| ALPN | `h2,http/1.1` |
| Fingerprint | `chrome` |
| `extra` | `xhttp_extra_shared` **+ клиентский `xmux`** |

Только для клиента (в инбаунд не попадает):

```json
"xmux": { "maxConcurrency": "12-24", "hMaxRequestTimes": "600-900", "hMaxReusableSecs": 1800, "hKeepAlivePeriod": 30 }
```

Форматы подписки:
- **Xray JSON, Mihomo, sing-box**: `extra` и `xmux` передаются полностью;
- **base64 `vless://`**: `extra` кладётся в параметр `extra=` (URL-encoded JSON), который понимают Happ, v2RayTun и v2rayN.

Если клиент не поддерживает XHTTP с `extra` (определяется по правилам ответа), точка для него **скрывается**, а не отдаётся в нерабочем виде.

### 4.6 Внешние шаги (на странице-инструкции, с подставленными значениями)

Эти шаги нельзя автоматизировать с сервера, поэтому страница-инструкция ноды (ARCHITECTURE.md §6.1) показывает их по порядку, как в HTML-гайде. Переменные уже подставлены, у каждого шага есть автопроверка, где она возможна.

| # | Шаг | Автопроверка |
|---|-----|--------------|
| 0.1 | (Если нужен корпоративный адрес для VK Cloud) Делегировать домен в Cloudflare, включить Email Routing `CORP_EMAIL → личный ящик` | — |
| 0.2 | Зарегистрироваться в VK Cloud | — |
| 1 | A-запись `${ORIGIN_DOMAIN}` → IP ноды | ✓ DNS |
| 2 | Запустить команду установки ноды | ✓ статус агента |
| 3 | VK Cloud → CDN → Создать ресурс: доступ конечным пользователям, протокол к источнику **HTTPS**, источник `${ORIGIN_DOMAIN}` (без `https://`), персональный домен `${CDN_DOMAIN}`, Host — «Пересылать», шифрование — **Let's Encrypt** | — |
| 4 | CNAME `${CDN_DOMAIN}` → `${VK_CNAME}` (других записей у имени быть не должно) | ✓ CNAME через 8.8.8.8 / 1.1.1.1 / 77.88.8.8 |
| 5 | Дождаться сертификата на edge | ✓ `https://${CDN_DOMAIN}${UPLINK_PATH}/` даёт `400` (пока выпускается — `000`) |
| 6 | Вкладка «Кеширование»: **выключить всё** | ✓ в ответе edge нет `cache: HIT` |
| 7 | «HTTP-заголовки»: всё выключено, методы `GET`, `HEAD`. «Контент», «Безопасность»: всё выключено | — |
| ⚠ | Перед каждым «Сохранить» в VK проверять, что шифрование осталось **Let's Encrypt** | ✓ периодическая проверка edge, алерт в бот, если TLS на edge сломался |

### 4.7 Проверки

| # | Проверка | Откуда | Ожидание |
|---|----------|--------|----------|
| 1 | `http://127.0.0.1:${XRAY_PORT}${UPLINK_PATH}/` | нода | `400` (Xray жив) |
| 2 | `https://${ORIGIN_DOMAIN}${UPLINK_PATH}/` | нода | `400` (Caddy и сертификат ок) |
| 3 | `https://${CDN_DOMAIN}${UPLINK_PATH}/?t=rand` | панель | `400` (CDN, edge-сертификат, проксирование) |
| 4 | Нет `cache: HIT` в ответе edge | панель | кэш выключен |
| 5 | Самотест клиентом через CDN | панель | 204 |
| 6 | Периодически (раз в 10 минут) проверки 3 и 4 | панель | при падении алерт в бот: «edge CDN не отвечает / слетел сертификат / включился кэш» |

### 4.8 Lint (валидация профиля перед сохранением)

Ловим ошибки из раздела «Если не работает» гайда **до** применения:

| Правило | Почему |
|---------|--------|
| Внутри `xhttpSettings.extra` нет вложенного `extra` | Xray молча проигнорирует блок → `invalid padding (query, key=x_padding) length:0` |
| `path` инбаунда = `path` точки без завершающего `/` | Иначе 400 на всё |
| `extra` инбаунда ⊆ `extra` точки (без учёта `xmux`) | Рассинхрон сервер ↔ клиент |
| `uplinkHTTPMethod` ∈ {`GET`} при включённом CDN-режиме | VK пропускает только GET/HEAD |
| `xPaddingPlacement` ≠ `header`, `uplinkDataPlacement` ≠ `header` | VK вырезает заголовки |
| `serverMaxHeaderBytes` ≥ 65536 | Большие куки с данными |

### 4.9 Диагностика (вместо ручных команд гайда)

| Было в гайде | У нас |
|--------------|-------|
| Править `log` в профиле, `docker restart`, `grep` по логу | Кнопка «Отладка 15 мин», лог Xray в UI с фильтром `padding\|session\|upload\|payload\|validate` |
| `tail -f xhttp_access.log \| cut -c1-160` | Access-лог Caddy в UI (в режиме отладки) |
| `tcpdump` на `lo` | Команда агенту `capture_headers(30s)`: первые N запросов к `${UPLINK_PATH}` с заголовками, показываются в UI |
| Таблица «Симптом → причина» | Встроена в страницу проверок: результат проверки → подсказка |

---

## 5. Совмещение A + B на одной ноде

Гайд B предполагает ноду **без** self-steal, потому что Reality занимает `:443`. В нашей схеме оба модуля уживаются на одном порту: Reality отдаёт весь «чужой» трафик в Caddy.

```
:443 → Xray Reality (serverNames = [NODE_DOMAIN])
        ├─ Reality-клиент                       → vless-reality
        └─ всё остальное (браузеры, VK CDN edge) → Caddy 127.0.0.1:8443
                                                    ├─ SNI NODE_DOMAIN   → сайт-заглушка
                                                    └─ SNI ORIGIN_DOMAIN → reverse_proxy → Xray 127.0.0.1:8085 (xhttp)
:80  → Caddy (ACME HTTP-01 для обоих доменов, редирект)
```

Рендерер выбирает раскладку автоматически. Это частный случай общего резолвера портов, который работает с любым набором IP (INBOUNDS.md §2.3):

| Инбаунды на одном IP | Кто на `:443` | Где Caddy TLS |
|---------------------------|---------------|---------------|
| только A | Xray (Reality) | `127.0.0.1:8443` |
| только B | Caddy | `0.0.0.0:443` |
| A + B | Xray (Reality) | `127.0.0.1:8443`, два сайта по SNI |

Замечания:
- В режиме A+B Caddy видит клиентов (включая edge VK) с адреса `127.0.0.1`. Для статистики это не важно, трафик считает Xray по пользователям. Если нужны реальные IP в логах Caddy, включаем `xver: 1` в Reality и `proxy_protocol` в Caddy. Это опция, по умолчанию выключена.
- Один сервер — две точки подключения в подписке: «🇳🇱 Нидерланды» (прямая) и «🇳🇱 Нидерланды · CDN» (через VK). Если они в одной группе, клиент видит обе. Можно развести по группам, например CDN только для группы «Мобильные».

---

## 6. Системная подготовка ноды (общая для всех шаблонов)

Выполняется установщиком ноды и проверяется агентом при каждом старте. Отклонение показывается в карточке ноды.

| Что | Как | Источник |
|-----|-----|----------|
| BBR + fq | `/etc/sysctl.d/90-vynel.conf`: `net.core.default_qdisc=fq`, `net.ipv4.tcp_congestion_control=bbr` | RemnaSetup «BBR», гайд B шаг 8 |
| TCP Fast Open | `net.ipv4.tcp_fastopen=3` | под `tcpFastOpen: true` в шаблоне A |
| Лимит файлов | `LimitNOFILE=1048576` в юните `vynel-node` и Xray | много соединений |
| Время | `systemd-timesyncd` или chrony, проверка смещения < 1 с | Reality |
| IPv6 | Переключатель в карточке ноды: оставить / отключить | RemnaSetup «Управление IPv6» |
| Обновление ОС | Флаг `--upgrade` в команде установки | PDF «Подготовка на ноде» |
| Firewall | Если найден ufw или firewalld, открываются порты из `node_requirements` | |
| Сайты-заглушки | Набор статичных шаблонов в бинаре, выбирается случайно на ноду | RemnaSetup self-steal |

Docker на ноде **не нужен**: Xray и Caddy — отдельные бинарники под systemd (в отличие от `remnanode` в контейнере).

---

## 7. Рендер: от шаблона к конфигу ноды

```
1. Нода: базовый конфиг + инбаунды ноды
2. Каждый инбаунд ноды: шаблон ⊕ профиль (defaults + override) ⊕ переменные ноды ⊕ override ноды
   + listen/egress IP (INBOUNDS.md §1.4, §2.4)
3. Подстановка ${VAR} → проверка: все переменные заданы, типы валидны
4. Сборка Xray: base + инбаунды ноды + системные секции (api/stats/policy) + clients
5. Сборка Caddy: резолвер портов по (IP, порт) (INBOUNDS.md §2.3) → JSON для Admin API
   + outbounds DIRECT@ip и правила inboundTag → outbound для sendThrough
6. Lint инбаундов (§4.8) + `xray run -test` на панели
7. Точки подключения: host-шаблоны профилей + переменные → hosts (с учётом ручных override)
8. revision++ → Snapshot ноде
```

Изменения, которые требуют внешних действий (например, смена `UPLINK_PATH` или `CDN_DOMAIN`), показываются с предупреждением: «после сохранения клиентам нужно обновить подписку» или «проверьте настройки ресурса в VK CDN».

---

## 8. Шаблон C — VLESS + XHTTP + REALITY (self-steal)

Файл: `internal/xrayconf/templates/vless-xhttp-reality.yaml`. Источник — ТЗ «XHTTP + REALITY» (Remnawave + Nginx); Nginx заменён на Caddy из шаблона A.

### 8.1 Технические характеристики

| Что | Значение |
|-----|----------|
| Вход | TCP `:443`, Xray REALITY; всё «чужое» уходит на свой сайт в Caddy `127.0.0.1:8443` (как в A) |
| Транспорт | XHTTP, режим `stream-up` (основной) или `packet-up` (резервный) — переменная `XHTTP_MODE` |
| Flow | пусто: Vision поверх XHTTP не работает |
| Маскировка запросов | сессия в куке `media_sid`, номер пакета `offset` и набивка `cb` в query (`repeat-x`, 100–1000 байт) |
| Сертификат | не нужен Xray: REALITY; сайт в Caddy получает сертификат сам (ACME на `:80`) |
| Fingerprint | `chrome` |
| Mihomo | отдаётся (`xhttp-opts`, `reuse-settings`, `support-x25519mlkem768: true`); нужен Mihomo 1.19+ |
| sing-box | не отдаётся: в sing-box нет XHTTP |

### 8.2 Переменные

| Переменная | Где | Откуда |
|------------|-----|--------|
| `NODE_DOMAIN` | нода | домен ноды |
| `REALITY_PRIVATE_KEY`, `REALITY_SHORT_ID` | нода | генерируются, свои на каждой ноде |
| `XHTTP_PATH` | профиль | случайный из `/assets/sync`, `/api/stream`, `/static/chunks`, `/media/feed` |
| `XHTTP_MODE` | профиль | `stream-up` (по умолчанию) или `packet-up` — выпадающий список |
| `SELFSTEAL_PORT`, `PORT` | профиль | `8443`, `443` |

### 8.3 Серверный и клиентский extra

Серверный `extra` (в инбаунде) — дословно из ТЗ: `uplinkDataPlacement: auto` (принимает и body, и куки), `noSSEHeader`, `scMaxEachPostBytes: "262144"`, `serverMaxHeaderBytes: 32768`. Рендер шаблона совпадает с инбаундом ТЗ (golden-тест `TestGoldenXHTTPRealityMatchesGuide`; сверх него только `sniffing` и `sockopt`).

Клиентский `extra` — **отдельный полный объект** (`host.xhttp_client_extra`), а не слияние с серверным, как требует ТЗ: клиенту не уходят серверные ключи. Один объект подходит для обоих режимов:

```json
{ "sessionIDPlacement": "cookie", "sessionIDKey": "media_sid", "sessionPlacement": "cookie", "sessionKey": "media_sid",
  "seqPlacement": "query", "seqKey": "offset",
  "xPaddingObfsMode": true, "xPaddingPlacement": "query", "xPaddingKey": "cb", "xPaddingMethod": "repeat-x", "xPaddingBytes": "100-1000",
  "mode": "${XHTTP_MODE}", "uplinkHTTPMethod": "POST", "uplinkDataPlacement": "body",
  "scMaxEachPostBytes": "65536-262144", "scMinPostsIntervalMs": "5-15",
  "xmux": { "maxConcurrency": "4-8", "hMaxRequestTimes": "600-900", "hMaxReusableSecs": "600-1800" },
  "noGRPCHeader": true }
```

`scMinPostsIntervalMs` и `scMaxEachPostBytes` действуют только в packet-up, `noGRPCHeader` — только в stream-up. Режим стоит и в `mode=` ссылки, и в `xhttpSettings.mode`, и в `xhttp-opts.mode` у Mihomo.

### 8.4 Точка подключения

| Поле | Значение |
|------|----------|
| Название | `🇳🇱 Нидерланды · XHTTP` |
| Адрес, SNI, Host | `${NODE_DOMAIN}` (адрес можно заменить на IP в карточке ноды) |
| Path | `${XHTTP_PATH}/` — со слешем |
| Security | REALITY, `pbk` и `sid` из ключей ноды |

Слеш в пути обязателен для Mihomo. Xray-сервер обслуживает путь `/assets/sync/`. Клиенты Xray добавляют слеш сами, а Mihomo шлёт путь как есть и получает `failed to validate path`. Это поймал тест с настоящим Mihomo.

### 8.5 Ограничения

- Один режим на один `IP:443`. Шаблоны A и C тоже не встанут на один IP: оба хотят Xray на внешнем `:443`. Панель покажет конфликт портов. Для C нужен второй IP ноды или другой порт.
- Шаблон B (CDN) с C совмещается так же, как с A (§5): CDN-домен уходит в Caddy за REALITY.
- На ноде должна быть A-запись `NODE_DOMAIN`, иначе Caddy не получит сертификат и маскировочный сайт не откроется.

### 8.6 Как проверено

`TestXHTTPRealityTrafficOnXray` запускает настоящий Xray с конфигом, который панель собирает для ноды. Пользователи добавляются через API, как это делает агент. Дальше трафик гоняется в обоих режимах:
- клиентом Xray из JSON-подписки;
- если задан `MIHOMO_BIN` — ещё и Mihomo из YAML-подписки.

Трафик сверяется по статистике пользователя на инбаунде.

---

## 9. Будущие шаблоны (не в MVP)

| Шаблон | Зачем |
|--------|-------|
| VLESS Reality «чужой сайт» (без своего домена) | Быстрый старт без DNS |
| VLESS XHTTP через Cloudflare / другой CDN | Те же модули, другой набор `extra` и внешних шагов |
| Shadowsocks-2022 / Trojan | Совместимость |
| WARP outbound | Модуль выхода (RemnaSetup WARP-NATIVE), для сервисов, блокирующих IP хостинга |
