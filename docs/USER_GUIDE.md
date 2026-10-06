# Руководство пользователя

Как устроен vynel и как им управлять. Установка описана в [ALL_IN_ONE.md](ALL_IN_ONE.md).

Всё управление сейчас идёт через команду `vynel admin` на сервере панели (веб-интерфейс появится позже). У любой команды есть справка: `vynel admin --help`, `vynel admin user --help`.

1. [Понятия](#1-понятия)
2. [Пользователи](#2-пользователи)
3. [Группы: кто к каким серверам имеет доступ](#3-группы)
4. [Шаблоны пользователей](#4-шаблоны-пользователей)
5. [Подписки и приложения](#5-подписки-и-приложения)
6. [Устройства (HWID)](#6-устройства-hwid)
7. [Статистика](#7-статистика)
8. [Профили и инбаунды](#8-профили-и-инбаунды)
9. [Ещё один сервер (нода)](#9-ещё-один-сервер-нода)
10. [VLESS через VK CDN](#10-vless-через-vk-cdn)
11. [Настройки](#11-настройки)
12. [Справочник команд](#12-справочник-команд)

---

## 1. Понятия

```
Шаблон (встроен)  ──►  Профиль (общие настройки)  ──►  Инбаунд ноды (VLESS_NL, VLESS_DE…)
                                                              ▲
Пользователь ──► Группы ──── правила доступа ─────────────────┘
       │
       └─► Подписка: одна ссылка → все серверы, доступные через группы
```

| Понятие | Что это | Пример |
|---------|---------|--------|
| **Нода** | Сервер, на котором работает VPN (Xray + Caddy). У all-in-one нода живёт на сервере панели | `NL` — Нидерланды, `nl.example.com` |
| **Шаблон профиля** | Готовая схема подключения, встроена в программу | `vless-reality-selfsteal`, `vless-xhttp-vkcdn` |
| **Профиль** | Твои общие настройки на основе шаблона: порт, параметры, шаблон имени | «Reality» |
| **Инбаунд ноды** | Профиль, применённый к конкретной ноде, со своими ключами и тегом. Создаётся командой `inbound attach` | `VLESS_NL`, `VLESS_DE` |
| **Группа** | Набор правил «к чему есть доступ»: весь профиль, вся нода или конкретный инбаунд | «Основная» → весь профиль «Reality» |
| **Шаблон пользователя** | Что получает новый пользователь: срок, группы, лимиты | «Стандарт»: +3 месяца, «Основная» |
| **Пользователь** | Человек с одной ссылкой подписки | `vasya` |
| **Точка подключения** | Как сервер выглядит в приложении: название, адрес, SNI. Вычисляется сама | «🇳🇱 Нидерланды» |

Связь с Remnawave: профиль + инбаунд ноды ≈ Config Profile, группа ≈ Internal Squad, точка подключения ≈ Host.

## 2. Пользователи

```bash
vynel admin user add vasya                      # срок, группы и лимиты берутся из шаблона по умолчанию
vynel admin user add petya --template Пробный   # из другого шаблона
vynel admin user add katya --group Основная --group Тест   # свои группы вместо групп шаблона

vynel admin user list                           # все; --status active|limited|expired|disabled, --search вас
vynel admin user show vasya                     # статус, срок, трафик по нодам, ссылка подписки

vynel admin user extend vasya --months 1        # продлить; считается от даты окончания, если она ещё не прошла
vynel admin user extend vasya --days 7
vynel admin user disable vasya                  # отключить (сразу пропадает со всех нод)
vynel admin user enable vasya
vynel admin user reset vasya                    # обнулить трафик за период
vynel admin user groups vasya --group Основная  # заменить группы
vynel admin user reissue vasya                  # новая ссылка подписки (старая перестаёт работать)
vynel admin user reissue vasya --uuid           # новый ключ VLESS (если ключ утёк)
vynel admin user rm vasya                       # удалить
```

**Статусы:** `active` — работает; `expired` — истёк срок; `limited` — кончился трафик; `disabled` — отключён вручную. Неактивный пользователь удаляется со всех нод за секунды. Вместо серверов он получает в приложении «⛔ причину».

**Сброс трафика** — по шаблону: `month` (1-го числа), `week` (в понедельник), `day` или `no`.

## 3. Группы

Группа решает, **к каким серверам** у пользователя есть доступ. При установке создаётся группа «Основная» с доступом ко всему профилю «Reality». Новая нода с этим профилем сразу становится доступна всем в группе.

```bash
vynel admin group list                               # группы и их правила
vynel admin group add Тест
vynel admin group grant Тест inbound VLESS_DE        # доступ к одному инбаунду
vynel admin group grant Тест node DE                 # ко всем инбаундам ноды DE
vynel admin group grant Тест profile Reality         # ко всему профилю на всех нодах, включая будущие
vynel admin group revoke Тест node DE
```

У пользователя может быть несколько групп. Его доступ — объединение доступов всех его групп.

## 4. Шаблоны пользователей

Шаблон — это то, что получает новый пользователь, если указать только имя.

```bash
vynel admin template list
vynel admin template add Пробный --days 3 --limit-gb 5 --reset no --group Основная
vynel admin template add Год --months 12 --group Основная --default   # сделать шаблоном по умолчанию
```

Изменение шаблона не затрагивает уже созданных пользователей: значения копируются в момент создания.

## 5. Подписки и приложения

Ссылка подписки выглядит как `https://<домен>/s/<токен>`, её показывает `user show`. Формат ответа выбирается сам:

| Кто запрашивает | Что получает |
|-----------------|--------------|
| Happ, v2RayTun, v2rayN/NG, Streisand, Hiddify, Shadowrocket… | Список `vless://` ссылок (base64) |
| Clash Verge, Mihomo, FlClash, Stash | Профиль Mihomo (YAML) |
| sing-box (SFA/SFI), Karing | Профиль sing-box (JSON) |
| Браузер | Страница: срок, трафик, QR-код, кнопки «добавить в приложение» |

Формат можно указать явно, добавив его в конец ссылки: `…/s/<токен>/clash`, `/singbox`, `/json` (Xray JSON), `/base64`.

- **Безопасность.** Неверный токен или любой другой путь открывает 404 сайта-заглушки. IP, который перебирает токены, банится на час.
- **Утечка ссылки.** Если ссылка попала не туда, `vynel admin user reissue vasya` выдаст новую.
- **Ссылки без подписки.** `vynel admin user links vasya` печатает `vless://` ссылки по одной.

## 6. Устройства (HWID)

Приложения вроде Happ и v2RayTun передают идентификатор устройства. vynel запоминает устройства и ограничивает их число.

```bash
vynel admin user devices vasya          # список: модель, ОС, когда последний раз обновлял подписку
vynel admin user devices vasya --rm 3   # освободить место
vynel admin setting hwid.default_limit 5      # лимит по умолчанию (0 = без лимита)
vynel admin setting hwid.allow_missing true   # пускать приложения без HWID
vynel admin setting hwid.enabled false        # выключить HWID совсем
```

Ограничение HWID работает только на выдачу подписки. Скопированную `vless://` ссылку оно не остановит. На этот случай есть `vynel admin user reissue vasya --uuid`.

## 7. Статистика

```bash
vynel admin stats            # пользователи по статусам, онлайн, трафик за день и 30 дней, топ-10
vynel admin node list        # по нодам: состояние, CPU, RAM, онлайн, трафик за сегодня, проблемы
vynel admin user show vasya  # трафик пользователя по нодам за 30 дней
vynel admin audit            # кто и что менял
```

Нода собирает счётчики каждые 10 секунд. Пока панель недоступна, данные копятся на ноде и потом доходят без потерь.

## 8. Профили и инбаунды

Обычно хватает того, что создала установка: профиль «Reality» и инбаунд `VLESS_<КОД>`.

```bash
vynel admin profile templates             # встроенные шаблоны и их переменные
vynel admin profile list
vynel admin inbound list                  # инбаунды нод: адрес, ключи pbk/sid
vynel admin inbound show VLESS_NL         # итоговый JSON инбаунда для Xray
vynel admin inbound set VLESS_NL --regenerate-keys   # новые ключи Reality (клиентам — обновить подписку)
vynel admin inbound host VLESS_NL         # как сервер выглядит в приложении
vynel admin inbound host VLESS_NL --set remark="🇳🇱 Амстердам"   # своё название
vynel admin inbound host VLESS_NL --set hidden=true             # скрыть из подписки
```

**Наследование.** Изменение профиля применяется ко всем нодам. Отличия конкретной ноды хранятся отдельно и при этом не теряются:

```bash
vynel admin profile set Reality --override '{"streamSettings":{"sockopt":{"tcpKeepAliveIdle":60}}}'
```

## 9. Ещё один сервер (нода)

На **панели**:

```bash
vynel admin node add --name Германия --country de --domain de.example.com   # печатает токен vyn1…
vynel admin inbound attach --node DE --profile Reality                      # VLESS_DE со своими ключами
```

На **новом сервере** (Ubuntu/Debian, root, свободные 80/443, A-запись `de.example.com` → его IP):

```bash
ARCH=amd64; XRAY=Xray-linux-64.zip          # для arm64: ARCH=arm64; XRAY=Xray-linux-arm64-v8a.zip
curl -fsSL -o /usr/local/bin/vynel https://github.com/vyto4ka/vynel/releases/download/edge-claude-magical-hamilton-9vnx7n/vynel-linux-$ARCH
chmod +x /usr/local/bin/vynel
# Xray
apt-get install -y unzip && curl -fsSL -o /tmp/xray.zip https://github.com/XTLS/Xray-core/releases/latest/download/$XRAY
mkdir -p /usr/local/share/xray && unzip -o /tmp/xray.zip -d /usr/local/share/xray && ln -sf /usr/local/share/xray/xray /usr/local/bin/xray
# Caddy
V=$(curl -fsSL https://api.github.com/repos/caddyserver/caddy/releases/latest | grep -o '"tag_name": *"v[^"]*"' | grep -o '[0-9.]*[0-9]')
curl -fsSL https://github.com/caddyserver/caddy/releases/download/v$V/caddy_${V}_linux_$ARCH.tar.gz | tar -xz -C /usr/local/bin caddy
# запуск с токеном из `node add`
systemd-run --unit vynel-node vynel node run --token vyn1…
```

На панели порт 9443 должен быть открыт (вопрос при установке). Проверка: `vynel admin node list` → `DE … in sync`. Пользователи группы «Основная» получат второй сервер в подписке сами.

Токен одноразовый и действует 24 часа. Новый токен: `vynel admin node token DE` (старый сертификат ноды при этом отзывается). Отдельный установщик нод и установка по SSH из панели — в плане (этапы 9, 11).

## 10. VLESS через VK CDN

Это второй встроенный шаблон: на случай, когда IP серверов блокируют, а CDN VK доступен. Подробно схема описана в [PROFILES.md §4](PROFILES.md).

```bash
vynel admin profile add --name CDN --template vless-xhttp-vkcdn
vynel admin inbound attach --node NL --profile CDN \
  --set CDN_DOMAIN=cdn.example.com --set ORIGIN_DOMAIN=origin.example.com
```

Остальное делается в кабинете VK Cloud: CDN-ресурс с источником `origin.example.com`, CNAME `cdn.example.com`, кэш выключен. Пошагово — в [PROFILES.md §4.6](PROFILES.md). Caddy на ноде настраивается сам: прокси для `origin.example.com` за Reality на том же порту 443. В подписке появится второй сервер «🇳🇱 Нидерланды · CDN». Mihomo и sing-box этот вариант не поддерживают и его не получают.

## 11. Настройки

`vynel admin setting КЛЮЧ` показывает значение, `vynel admin setting КЛЮЧ ЗНАЧЕНИЕ` меняет.

| Ключ | По умолчанию | Что делает |
|------|--------------|-----------|
| `hwid.enabled` | `true` | Учёт устройств |
| `hwid.default_limit` | `3` | Лимит устройств, если у пользователя нет своего (0 = без лимита) |
| `hwid.allow_missing` | `false` | Пускать приложения без HWID |
| `sub.domain` | домен установки | Домен в ссылках подписки |
| `sub.prefix` | `/s/` | Путь подписок, например `/api/v1/client/` |
| `sub.title` | `VPN` | Название профиля в приложениях |
| `sub.update_hours` | `12` | Как часто приложения обновляют подписку |
| `sub.support_url` | — | Ссылка «поддержка» в приложениях |
| `sub.decoy` | `docs` | Сайт-заглушка домена подписок: `cloud`, `studio`, `docs` |
| `caddy.email` | — | Email для Let's Encrypt |
| `node.caddy_enabled` | `true` | `false` — Caddy на ноде не нужен (свой веб-сервер). Для одной ноды: `node.caddy_enabled.DE` |

Изменения применяются сами, перезапуск не нужен. Исключение — `sub.listen`: после него нужен `systemctl restart vynel`.

## 12. Справочник команд

```
vynel admin
  setup                     настройка all-in-one (её вызывает установщик; повторный запуск безопасен)
  stats                     сводка
  audit [-n N]              журнал изменений
  setting KEY [VALUE]       настройки
  user      add | list | show | extend | disable | enable | reset | groups | reissue | links | devices | rm
  group     add | list | grant | revoke
  template  add | list
  node      add | list | addr | token | rm
  profile   templates | add | list | set
  inbound   attach | list | show | set | host | detach

vynel panel  …              сервер панели (запускает systemd-сервис vynel)
vynel node run --token …    агент ноды на отдельном сервере
vynel node set-panel HOST:PORT    если панель переехала
vynel version
```
