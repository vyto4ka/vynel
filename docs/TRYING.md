# Как попробовать

> **Для настоящего развёртывания** используй [ALL_IN_ONE.md](ALL_IN_ONE.md): одна команда, свой домен, подписки и Caddy.
> Ниже — ручные сценарии для разработки и экспериментов (без Caddy и подписок по HTTPS).

Веб-интерфейса пока нет. Всё делается через `vynnel admin`; ссылка для клиента печатается командой `vynnel admin user links`.

Панель и нода работают только на Linux. На Windows используй WSL2 (Ubuntu) или сразу тестовый VPS.

---

## 1. Прогнать тесты (WSL2 или любой Linux)

```bash
sudo apt update && sudo apt install -y git make unzip curl
# Go 1.21+ с https://go.dev/dl (нужный 1.26 подтянется сам), например:
curl -fsSL https://go.dev/dl/go1.26.0.linux-amd64.tar.gz | sudo tar -C /usr/local -xz
export PATH=$PATH:/usr/local/go/bin

git clone -b claude/magical-hamilton-9vnx7n https://github.com/vyto4ka/vynnel && cd vynnel
make test-integration
```

`make test-integration` скачивает Xray в `.cache/xray` и прогоняет все тесты. Сквозной тест поднимает панель и две ноды на настоящем Xray и гонит через них трафик. Все строки должны закончиться на `ok`.

## 2. Посмотреть руками на одной машине

```bash
make build xray
X=.cache/xray
sudo bin/vynnel panel --data-dir /tmp/vynnel --gateway-listen 127.0.0.1:9443 --public-addr 127.0.0.1:9443 \
  --with-node --node-name Нидерланды --node-country nl --node-domain nl.example.com \
  --xray-bin $X/xray --xray-assets $X
```

`sudo` нужен потому, что инбаунд слушает порт 443. Во втором терминале:

```bash
A="sudo bin/vynnel admin --data-dir /tmp/vynnel"
$A profile add --name "Reality 443"                  # профиль из шаблона A; доступ получает группа «Основная»
$A inbound attach --node NL --profile "Reality 443"   # VLESS_NL со своими ключами
$A user add vasya                                      # +3 месяца, группа «Основная»
$A node list                                           # NL: in sync
$A inbound list; $A user list; $A audit
$A user disable vasya; $A user enable vasya            # в логе панели: delta applied
```

## 3. Подключиться с телефона (тестовый VPS)

Нужен VPS с Ubuntu 22.04/24.04 и **свободным портом 443**. Сервер с Remnawave для этого не подойдёт.

Self-steal требует Caddy, а Caddy появится только на этапе 6. Поэтому для теста Reality маскируется под чужой сайт (`www.microsoft.com`), и домен не нужен.

```bash
# на VPS под root
apt update && apt install -y git make unzip curl
curl -fsSL https://go.dev/dl/go1.26.0.linux-amd64.tar.gz | tar -C /usr/local -xz
export PATH=$PATH:/usr/local/go/bin
git clone -b claude/magical-hamilton-9vnx7n https://github.com/vyto4ka/vynnel && cd vynnel
make build xray
install -m755 bin/vynnel /usr/local/bin/vynnel
mkdir -p /usr/local/share/xray && cp .cache/xray/* /usr/local/share/xray/ && ln -sf /usr/local/share/xray/xray /usr/local/bin/xray

IP=$(curl -4 -s ifconfig.me)
systemd-run --unit vynnel vynnel panel --with-node --public-addr $IP:9443 \
  --node-name Тест --node-country nl --node-domain test.example.com

vynnel admin profile add --name "Reality test" \
  --override '{"streamSettings":{"realitySettings":{"target":"www.microsoft.com:443","serverNames":["www.microsoft.com"]}}}'
vynnel admin inbound attach --node NL --profile "Reality test"
vynnel admin user add vasya
vynnel admin user links vasya --address $IP     # скопируй vless://… в Happ / v2RayTun → «Добавить из буфера»
```

Что смотреть:
- `journalctl -u vynnel -f` — лог панели и ноды;
- `vynnel admin user disable vasya` — клиент перестаёт подключаться через пару секунд, без перезапуска Xray;
- `vynnel admin user enable vasya` — снова работает.

Убрать за собой: `systemctl stop vynnel && rm -rf /var/lib/vynnel`.

## 4. Вторая нода (ещё один VPS)

На сервере панели должен быть открыт порт 9443. Порядок:

1. На втором VPS повтори установку из раздела 3 до `ln -sf …` включительно.
2. На панели создай ноду и привяжи к ней профиль:
   ```bash
   vynnel admin node add --name Германия --country de --domain test2.example.com   # печатает токен
   vynnel admin inbound attach --node DE --profile "Reality test"
   ```
3. На втором VPS запусти ноду с этим токеном:
   ```bash
   systemd-run --unit vynnel-node vynnel node run --token vyn1....
   ```
4. Проверь на панели: `vynnel admin node list` → DE `in sync`.

`user links --address` подставляет один и тот же IP во все ссылки. Поэтому с двумя нодами либо пропиши им настоящие домены (`--node-domain`, `--domain`), либо бери ссылку на вторую ноду и меняй в ней IP руками.

## Docker

В `deploy/dev/docker-compose.yml` описаны панель и вторая нода. Файл ещё **ни разу не запускался**: в среде разработки нет Docker. Используй его с этой оговоркой.
