#!/usr/bin/env bash
# Основные вызовы API на curl. Нужны curl и jq.
#
#   export VYNEL_API=https://panel.example.com/<секретный путь>/api/v1
#   export VYNEL_TOKEN=vyn_...
#   ./quickstart.sh
set -euo pipefail
: "${VYNEL_API:?задайте VYNEL_API}" "${VYNEL_TOKEN:?задайте VYNEL_TOKEN}"

api() { # api МЕТОД ПУТЬ [JSON]; у запросов, которые меняют данные, свой Idempotency-Key
  local method=$1 path=$2 body=${3:-}
  local args=(-fsS -X "$method" "$VYNEL_API$path" -H "Authorization: Bearer $VYNEL_TOKEN")
  if [ -n "$body" ]; then args+=(-H "Content-Type: application/json" -d "$body"); fi
  if [ "$method" != GET ]; then args+=(-H "Idempotency-Key: ${KEY:-$(cat /proc/sys/kernel/random/uuid)}"); fi
  curl "${args[@]}"
}

echo "== токен";               api GET /me | jq '.token | {name, scopes}'
echo "== создать пользователя"; USER=$(KEY=demo-create-$$ api POST /users '{"username":"demo-'$$'","external_id":"order-'$$'"}'); echo "$USER" | jq '{id, username, status, expire_at, subscription_url}'
ID=$(echo "$USER" | jq .id)
echo "== продлить на месяц";   api POST "/users/$ID/extend" '{"months":1}' | jq '{status, expire_at}'
echo "== найти по своему id";  api GET "/users?external_id=order-$$" | jq '.items[0].username'
echo "== изменить";            api PATCH "/users/$ID" '{"note":"оплачен","traffic_limit_bytes":107374182400}' | jq '{note, traffic_limit_bytes}'
echo "== выключить";           api POST "/users/$ID/disable" | jq '{status}'
echo "== удалить";             api DELETE "/users/$ID" && echo "удалён (нужно право users:delete)"
echo "== события";             api GET "/events?after=now" | jq '{next_cursor, has_more}'
