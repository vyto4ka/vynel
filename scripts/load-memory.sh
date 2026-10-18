#!/usr/bin/env bash
# Measures the memory of the real panel binary with N users: RSS when idle and its peak while
# 32 clients fetch subscriptions (docs/DEVELOPMENT.md "Нагрузочные тесты").
#   scripts/load-memory.sh 1000 20000 50000
# Builds bin/vynel and the load test binary; data goes to a temporary directory.
set -euo pipefail

cd "$(dirname "$0")/.."
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
go build -o "$work/vynel" ./cmd/vynel
go test -tags loadtest -c -o "$work/load.test" ./internal/loadtest/

rss() { awk '/VmRSS/{print int($2/1024)}' "/proc/$1/status" 2>/dev/null || echo 0; }
hwm() { awk '/VmHWM/{print int($2/1024)}' "/proc/$1/status" 2>/dev/null || echo 0; }

printf '%-8s %-10s %-10s %s\n' users idle_MB peak_MB "subscriptions over HTTP"
for n in "$@"; do
  d="$work/$n"
  mkdir -p "$d"
  LOAD_DB="$d/panel.db" LOAD_USERS="$n" LOAD_SUB_LISTEN=127.0.0.1:2296 LOAD_WEB_LISTEN=127.0.0.1:2298 \
    "$work/load.test" -test.run '^TestSeedPanel$' >/dev/null
  "$work/vynel" panel --data-dir "$d" --gateway-listen 127.0.0.1:19446 >"$d/panel.log" 2>&1 &
  pid=$!
  sleep 20
  idle="$(rss "$pid")"
  LOAD_URL=http://127.0.0.1:2296/s/ LOAD_USERS="$n" LOAD_SECONDS=30 \
    "$work/load.test" -test.run '^TestHTTPHammer$' -test.v >"$d/hammer.txt" 2>&1 &
  h=$!
  peak=0
  while kill -0 "$h" 2>/dev/null; do
    r="$(rss "$pid")"
    if [ "$r" -gt "$peak" ]; then peak="$r"; fi
    sleep 0.5
  done
  top="$(hwm "$pid")"
  if [ "$top" -gt "$peak" ]; then peak="$top"; fi
  printf '%-8s %-10s %-10s %s\n' "$n" "$idle" "$peak" "$(grep -o '[0-9]* req/s.*' "$d/hammer.txt" | head -1)"
  kill "$pid"
  wait "$pid" 2>/dev/null || true
done
