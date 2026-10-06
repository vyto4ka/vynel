# Разработка

## Требования

- **Go 1.21+.** Нужная версия (1.26, её требует `xray-core`) скачается сама через `GOTOOLCHAIN=auto`.
- **Linux или WSL2.** Процессы Xray и Caddy управляются через Linux-API.
- **Xray и Caddy для интеграционных тестов.** `make test-integration` сам скачает Xray и соберёт Caddy в `.cache/`.
- **Генераторы protobuf** — только если меняешь `.proto`: `buf`, `protoc-gen-go`, `protoc-gen-go-grpc` (ставятся через `go install …`).

## Команды

```bash
make build              # bin/vynel
make build-all          # bin/vynel-linux-amd64, bin/vynel-linux-arm64
make test               # быстрые тесты (без Xray/Caddy часть тестов пропускается)
make test-integration   # всё, включая сквозные тесты на настоящих Xray и Caddy
make lint               # golangci-lint
make proto              # перегенерировать internal/proto из proto/
make web                # собрать заглушку веб-интерфейса (web/dist, вшивается в бинарь)
```

## Структура

```
cmd/vynel/                  точка входа (один бинарь на всё)
internal/
  cli/                      команды: panel, node, admin …
  xrayconf/                 шаблоны профилей (templates/*.yaml), рендер, наследование, lint, сборка конфига Xray
  xray/                     процесс Xray, проверка конфига (xray -test), gRPC API Xray
  caddyconf/                генератор JSON-конфига Caddy
  decoy/                    встроенные сайты-заглушки
  jointoken/                токен подключения ноды (vyn1.…)
  proto/                    сгенерированный gRPC (из proto/vynel/node/v1/node.proto)
  panel/
    store/                  SQLite: миграции (store/migrations), запросы
    service/                вся бизнес-логика: ноды, профили, группы, пользователи, статистика,
                            точки подключения, HWID, раскладка Caddy, желаемое состояние нод
    reconciler/             рассылка желаемого состояния нодам (снапшот / дельта), обслуживание
    gateway/                gRPC-сервер для нод (Join + Connect по mTLS)
    subscription/           HTTP подписок: форматы, правила клиентов, страница, заглушки
    ca/                     внутренний CA
    app/                    сборка процесса панели
  node/
    agent/                  агент ноды: сессия с панелью, применение к Xray/Caddy, статистика
    caddy/                  процесс Caddy, загрузка конфига через admin API
    state/                  bbolt: применённое состояние, очередь статистики
    metrics/, sysctl/       метрики хоста, BBR/fq/TFO
  e2e/                      сквозные тесты (панель + ноды + настоящие Xray/Caddy)
proto/                      .proto
scripts/                    install-aio.sh, fetch-xray.sh
docs/                       документация
```

Главное правило: **транспорты не содержат логики.** CLI, а позже веб и бот вызывают методы `internal/panel/service`. Каждое изменение записывается в журнал (`audit_log`) и в очередь событий (`events_outbox`) в той же транзакции.

## Как это работает внутри

1. **Желаемое состояние.** Любое изменение запускает в reconciler пересчёт желаемого состояния каждой ноды (`service.DesiredStates`). Оно включает конфиг Xray без клиентов, пользователей по инбаундам, конфиг Caddy и хэш.
2. **Доставка на ноду.** Если поменялись только пользователи, нода получает **дельту** и меняет их через API Xray без перезапуска. Если поменялась структура, нода получает **снапшот** и перезапускает Xray. Каждое сообщение нода подтверждает.
3. **Статистика.** Каждые 10 секунд нода складывает статистику в очередь с номером `(epoch, seq)` и досылает её, пока панель не подтвердит. Повторная доставка не учитывается дважды.
4. **Изменения из другого процесса.** Если `vynel admin` меняет данные, пока панель работает, панель замечает это, опрашивая `events_outbox` раз в секунду.

## Тесты

| Пакет | Что проверяет |
|-------|---------------|
| `xrayconf` | Рендер шаблонов совпадает с рабочими конфигами (`testdata/`: профиль из Remnawave, инбаунд из гайда VK CDN), наследование, lint |
| `xray` | Настоящий Xray: Reality, добавление и удаление пользователя на лету, статистика |
| `panel/service` | Статусы, продление, доступ групп, токены нод, статистика, сбросы, раскладка Caddy |
| `panel/subscription` | Форматы, HWID, заглушки, бан перебора. Xray принимает сгенерированные клиентские конфиги |
| `e2e` | Две ноды (gRPC и in-process), горячие изменения, работа без панели, отзыв сертификата, лимит трафика. All-in-one на одном IP и домене с настоящим Caddy |

Без `XRAY_BIN` и `CADDY_BIN` интеграционные тесты пропускаются. Эти переменные выставляет `make test-integration`.

## Как добавить шаблон профиля

1. Создай `internal/xrayconf/templates/<id>.yaml` по образцу двух существующих. Поля описаны в [PROFILES.md §1](PROFILES.md):
   - `variables` — scope, source, generator;
   - `xray_inbound` — JSON с `${VAR}`;
   - `host` — точка подключения;
   - `caddy` — роль `selfsteal` или `reverse_proxy_xhttp`;
   - `lint`.
2. Если клиенту нужен новый вид транспорта, добавь его в `service.HostFor` и в форматы `panel/subscription/formats.go`.
3. Добавь golden-тест с эталонным рабочим конфигом в `internal/xrayconf/testdata/`.

## Релизы

- **CI** (`.github/workflows/ci.yml`): тесты с Xray и Caddy, `-race`, линтер, сборка.
- **edge** (`.github/workflows/edge.yml`):
  - при каждом push собирает `vynel-linux-{amd64,arm64}` и `SHA256SUMS`;
  - публикует их как pre-release `edge-<ветка>` (в имени ветки `/` заменяется на `-`);
  - `scripts/install-aio.sh` берёт бинарь оттуда; другая ветка или релиз задаются через `VYNEL_REF=…` и `VYNEL_RELEASE=…`.

## Ручной запуск без установки

```bash
make build xray caddy
X=.cache/xray
sudo bin/vynel panel --data-dir /tmp/vynel --gateway-listen 127.0.0.1:9443 --public-addr 127.0.0.1:9443 \
  --with-node --xray-bin $X/xray --xray-assets $X --caddy-bin .cache/caddy/caddy --tune-sysctl=false
# во втором терминале
A="sudo bin/vynel admin --data-dir /tmp/vynel"
$A setup --domain nl.example.com --name Нидерланды --country nl
$A setting caddy.issuer internal          # без настоящего домена: самоподписанные сертификаты
$A user add vasya && $A user show vasya
$A node list
```
