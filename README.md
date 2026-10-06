# vpn

Панель и нода для своих VPN-серверов на Xray + Caddy — аналог Remnawave с упором на простую установку.

Документация по архитектуре — в [docs/](docs/): [ARCHITECTURE](docs/ARCHITECTURE.md), [PROFILES](docs/PROFILES.md),
[INBOUNDS](docs/INBOUNDS.md), [INSTALL](docs/INSTALL.md), [STEALTH](docs/STEALTH.md), [ROADMAP](docs/ROADMAP.md).

## Что уже работает (этапы 0–3)

- Шаблоны профилей A (VLESS Reality self-steal) и B (VLESS XHTTP через VK CDN); рендер совпадает с рабочими конфигами (golden-тесты).
- Профиль → свой инбаунд на каждой ноде (`VLESS_NL`, `VLESS_DE`): свои ключи Reality и shortId, наследование с override, мульти-IP (`listen` и `sendThrough`).
- Пользователи, группы с правилами доступа (инбаунд / профиль целиком / нода), шаблоны пользователей — создание по одному имени.
- Связь панель ↔ нода: нода регистрируется одной строкой-токеном, дальше gRPC + mTLS; изменения пользователей применяются без перезапуска Xray, изменения структуры — снапшотом с перезапуском.
- Нода работает без панели на сохранённом состоянии; режим `panel --with-node`.
- Временный CLI `vpn admin` вместо веб-интерфейса.

Ещё нет: статистики, подписок, Caddy, веб-интерфейса, бота, установщика (см. ROADMAP).

## Сборка и тесты

```bash
make build              # bin/vpn
make test               # быстрые тесты
make test-integration   # + тесты с настоящим Xray (скачивается в .cache/xray)
make lint
```

## Попробовать вручную

```bash
X=.cache/xray; make build xray
# панель + локальная нода
bin/vpn panel --data-dir /tmp/vpn --gateway-listen :9443 --public-addr 127.0.0.1:9443 \
  --with-node --node-name Нидерланды --node-country nl --node-domain nl.example.com \
  --xray-bin $X/xray --xray-assets $X &

A="bin/vpn admin --data-dir /tmp/vpn"
$A profile add --name "Reality 443"                 # из шаблона, доступ группе «Основная»
$A inbound attach --node NL --profile "Reality 443"  # создаст VLESS_NL со своими ключами
$A user add vasya                                     # +3 месяца, группа «Основная»
$A node list; $A inbound list

# вторая нода
$A node add --name Германия --country de --domain de.example.com   # печатает токен
bin/vpn node run --data-dir /tmp/vpn-de --token vpn1.... --xray-bin $X/xray --xray-assets $X
```
