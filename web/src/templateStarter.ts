import { lang } from './i18n'

// Starting point for the "New template" button: a complete, working template with every
// section explained in the panel's language. Checked by the sample render like any
// other template.
const starterRu = `# Заготовка своего шаблона: VLESS + XHTTP за nginx на домене ноды.
# Строки с # — пояснения, их можно удалить. Справа две вкладки: «Проверка» пересобирает
# шаблон на пробной ноде при каждом изменении, «Справка» описывает все поля.

# Уникальный id: латиница, цифры, дефис. Пока на шаблоне есть профили, id не меняется.
id: my-xhttp
# Номер версии: увеличивайте при правках.
version: 1
# Название и описание — для людей, видны в списке шаблонов и при создании профиля.
title: Мой XHTTP
summary: VLESS + XHTTP за nginx на домене ноды.
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
    description: Домен, на котором nginx примет подключения (A-запись на ноду).
  - name: XRAY_PORT
    scope: profile
    source: const
    default: 8090
    validate: port
    description: Локальный порт Xray за nginx, наружу не открывается.
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

# Что поднять в nginx на ноде: сертификат на DOMAIN и проксирование пути в Xray.
# Свои директивы nginx можно вписать в location: | (внутрь location пути) и server: |.
nginx:
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

const starterEn = `# A blueprint of your own template: VLESS + XHTTP behind nginx on the node’s domain.
# Lines with # are comments and can be deleted. Two tabs on the right: “Check” rebuilds the
# template on a trial node on every change, “Reference” describes all the fields.

# A unique id: Latin letters, digits, hyphen. It does not change while profiles use the template.
id: my-xhttp
# The version number: raise it with edits.
version: 1
# The title and summary are for people; they show in the template list and when creating a profile.
title: My XHTTP
summary: VLESS + XHTTP behind nginx on the node’s domain.
# The inbound tag on the node. \${NODE_CODE} is required: each node has its own tag (MYX_NL, MYX_DE).
tag_pattern: MYX_\${NODE_CODE}
# What the connection is called in apps.
remark_pattern: \${NODE_FLAG} \${NODE_NAME} · XHTTP
protocol: vless
client:
  flow: ""
# Extra checks of the finished inbound (the list is in the reference).
lint:
  - xhttp_no_nested_extra

# Variables are everything that differs between profiles and nodes. In the text below they are written as \${NAME}.
#   scope:  profile — one value per profile, node — its own on each node.
#   source: input — typed by the admin; const — a constant (can be changed in the profile);
#           generate — generated once and stored; derived — computed from another one.
variables:
  - name: DOMAIN
    scope: node
    source: input
    default_from: node.domain
    validate: domain
    description: The domain nginx accepts connections on (an A record to the node).
  - name: XRAY_PORT
    scope: profile
    source: const
    default: 8090
    validate: port
    description: Xray’s local port behind nginx; it is not opened to the outside.
  - name: UPLINK_PATH
    scope: profile
    source: generate
    generator: path
    options: [/api, /stream, /data]
    validate: path
    description: The XHTTP path without a trailing slash.
  - name: FINGERPRINT
    scope: profile
    source: const
    default: chrome
    options: [chrome, firefox, safari, ios, android, edge, random]
    description: The client’s TLS fingerprint.

# XHTTP parameters (extra). In the inbound they are substituted as the string "\${XHTTP_EXTRA}", and clients
# get the same extra unless host has its own (xhttp_client_extra).
xhttp_extra: |
  {
    "xPaddingBytes": "100-1000"
  }

# The Xray inbound — as in an ordinary config, but with \${VARIABLES}. Users in clients
# are added automatically; leave the list empty.
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

# The connection point — what clients get in the subscription.
host:
  address: \${DOMAIN}
  port: 443
  security: tls
  sni: \${DOMAIN}
  host: \${DOMAIN}
  path: \${UPLINK_PATH}/
  alpn: [h2, http/1.1]
  fingerprint: \${FINGERPRINT}

# What to set up in nginx on the node: a certificate for DOMAIN and proxying the path to Xray.
# Your own nginx directives can go into location: | (inside the path’s location) and server: |.
nginx:
  role: reverse_proxy_xhttp
  domain: \${DOMAIN}
  upstream: 127.0.0.1:\${XRAY_PORT}
  path: \${UPLINK_PATH}

# Node requirements: they go into the node checklist.
node_requirements:
  ports: [443/tcp, 80/tcp]
  dns:
    - { type: A, name: "\${DOMAIN}" }
`

export const templateStarter = () => (lang() === 'en' ? starterEn : starterRu)
