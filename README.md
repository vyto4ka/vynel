# vynnel

Панель и нода для своих VPN-серверов на Xray + Caddy — аналог Remnawave с упором на простую установку.

**Развернуть всё на одном сервере (один IP, один домен):** [docs/ALL_IN_ONE.md](docs/ALL_IN_ONE.md). Как попробовать руками — [docs/TRYING.md](docs/TRYING.md). Документация по архитектуре — в [docs/](docs/): [ARCHITECTURE](docs/ARCHITECTURE.md), [PROFILES](docs/PROFILES.md),
[INBOUNDS](docs/INBOUNDS.md), [INSTALL](docs/INSTALL.md), [STEALTH](docs/STEALTH.md), [ROADMAP](docs/ROADMAP.md).

## Что уже работает (этапы 0–6)

- Шаблоны профилей A (VLESS Reality self-steal) и B (VLESS XHTTP через VK CDN); рендер совпадает с рабочими конфигами.
- Профиль → свой инбаунд на каждой ноде (`VLESS_NL`, `VLESS_DE`): свои ключи, наследование с override, мульти-IP.
- Пользователи по одному имени (шаблоны), группы с правилами доступа, статусы, продление, сбросы трафика.
- Связь панель ↔ нода по токену (gRPC + mTLS), изменения без перезапуска Xray, работа ноды без панели.
- Статистика: трафик по пользователям и нодам, онлайн, CPU/RAM/сеть, лимиты трафика.
- Подписки: base64, Mihomo, sing-box, Xray JSON, HTML-страница с QR; HWID-лимиты; заглушки с причиной; неизвестные токены выглядят как обычный сайт.
- Caddy: self-steal-сайт за Reality, сертификаты Let's Encrypt, XHTTP-origin, домен подписок; BBR/fq/TFO.
- **All-in-one**: панель + нода + Caddy на одном сервере, одном IP и (минимум) одном домене — `scripts/install-aio.sh`.
- Временный CLI `vynnel admin` вместо веб-интерфейса.

Ещё нет: веб-интерфейса, Telegram-бота, бэкапов, установки нод по SSH, полного скрытия (см. ROADMAP).

## Сборка и тесты

```bash
make build              # bin/vynnel
make test               # быстрые тесты
make test-integration   # + тесты с настоящими Xray и Caddy (в .cache/)
make lint
```

## Попробовать вручную

```bash
X=.cache/xray; make build xray
# панель + локальная нода
bin/vynnel panel --data-dir /tmp/vynnel --gateway-listen :9443 --public-addr 127.0.0.1:9443 \
  --with-node --node-name Нидерланды --node-country nl --node-domain nl.example.com \
  --xray-bin $X/xray --xray-assets $X &

A="bin/vynnel admin --data-dir /tmp/vynnel"
$A profile add --name "Reality 443"                 # из шаблона, доступ группе «Основная»
$A inbound attach --node NL --profile "Reality 443"  # создаст VLESS_NL со своими ключами
$A user add vasya                                     # +3 месяца, группа «Основная»
$A node list; $A inbound list

# вторая нода
$A node add --name Германия --country de --domain de.example.com   # печатает токен
bin/vynnel node run --data-dir /tmp/vynnel-de --token vyn1.... --xray-bin $X/xray --xray-assets $X
```
