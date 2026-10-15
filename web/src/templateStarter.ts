// Starting point for the "New template" button: a complete, working template with every
// section explained in Russian (the panel's language). Checked by the sample render like any
// other template.
export const templateStarter = `# Заготовка своего шаблона: VLESS + XHTTP за Caddy на домене ноды.
# Строки с # — пояснения, их можно удалить. Справа две вкладки: «Проверка» пересобирает
# шаблон на пробной ноде при каждом изменении, «Справка» описывает все поля.

# Уникальный id: латиница, цифры, дефис. Пока на шаблоне есть профили, id не меняется.
id: my-xhttp
# Номер версии: увеличивайте при правках.
version: 1
# Название и описание — для людей, видны в списке шаблонов и при создании профиля.
title: Мой XHTTP
summary: VLESS + XHTTP за Caddy на домене ноды.
# Тег инбаунда на ноде. \${NODE_CODE} обязателен: у каждой ноды свой тег (MYX_NL, MYX_DE).
tag_pattern: MYX_\${NODE_CODE}
# Как подключение называется в приложениях.
remark_pattern: \${NODE_FLAG} \${NODE_NAME} · XHTTP
protocol: vless
client:
  flow: ""
# Дополнительные проверки готового инбаунда (список — в справке).
lint:
  - xhttp_no_nested_extra

# Переменные — всё, что отличается между профилями и нодами. В тексте ниже пишутся как \${ИМЯ}.
#   scope:  profile — одно значение на профиль, node — своё на каждой ноде.
#   source: input — вводит админ; const — константа (можно поменять в профиле);
#           generate — генерируется один раз и хранится; derived — вычисляется из другой.
variables:
  - name: DOMAIN
    scope: node
    source: input
    default_from: node.domain
    validate: domain
    description: Домен, на котором Caddy примет подключения (A-запись на ноду).
  - name: XRAY_PORT
    scope: profile
    source: const
    default: 8090
    validate: port
    description: Локальный порт Xray за Caddy, наружу не открывается.
  - name: UPLINK_PATH
    scope: profile
    source: generate
    generator: path
    options: [/api, /stream, /data]
    validate: path
    description: Путь XHTTP без слеша на конце.
  - name: FINGERPRINT
    scope: profile
    source: const
    default: chrome
    options: [chrome, firefox, safari, ios, android, edge, random]
    description: TLS-отпечаток клиента.

# Параметры XHTTP (extra). В инбаунде подставляются строкой "\${XHTTP_EXTRA}", клиенты
# получают тот же extra, если в host не задан свой (xhttp_client_extra).
xhttp_extra: |
  {
    "xPaddingBytes": "100-1000"
  }

# Инбаунд Xray — как в обычном конфиге, но с \${ПЕРЕМЕННЫМИ}. Пользователи в clients
# добавляются сами, оставьте список пустым.
xray_inbound: |
  {
    "tag": "\${TAG}",
    "port": "\${XRAY_PORT}",
    "listen": "127.0.0.1",
    "protocol": "vless",
    "settings": { "clients": [], "decryption": "none" },
    "streamSettings": {
      "network": "xhttp",
      "xhttpSettings": {
        "mode": "packet-up",
        "path": "\${UPLINK_PATH}",
        "extra": "\${XHTTP_EXTRA}"
      }
    }
  }

# Точка подключения — что уходит клиентам в подписку.
host:
  address: \${DOMAIN}
  port: 443
  security: tls
  sni: \${DOMAIN}
  host: \${DOMAIN}
  path: \${UPLINK_PATH}/
  alpn: [h2, http/1.1]
  fingerprint: \${FINGERPRINT}

# Что поднять в Caddy на ноде: сертификат на DOMAIN и проксирование пути в Xray.
caddy:
  role: reverse_proxy_xhttp
  domain: \${DOMAIN}
  upstream: 127.0.0.1:\${XRAY_PORT}
  path: \${UPLINK_PATH}

# Требования к ноде: попадают в чек-лист ноды.
node_requirements:
  ports: [443/tcp, 80/tcp]
  dns:
    - { type: A, name: "\${DOMAIN}" }
`
