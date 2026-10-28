"""Приёмник вебхуков Vynel на одной стандартной библиотеке.

    export VYNEL_WEBHOOK_SECRET=whsec_...
    python3 webhook_receiver.py 8000

В панели («API» → «Вебхуки») укажите адрес https://ваш-сервер/hook. Если приёмник работает на
том же сервере, что и панель, включите у вебхука «Разрешить локальные адреса».
За реальным приёмником поставьте HTTPS (nginx/Caddy), здесь его нет ради краткости.
"""
import hashlib
import hmac
import json
import os
import sys
import time
from http.server import BaseHTTPRequestHandler, HTTPServer

SECRET = os.environ["VYNEL_WEBHOOK_SECRET"]
TOLERANCE = 300  # секунд: старше — это повтор перехваченного запроса
seen = set()  # id уже обработанных событий; в настоящей программе — таблица в базе


def verify(headers, raw: bytes) -> bool:
    ts = headers.get("X-Vynel-Timestamp", "")
    got = headers.get("X-Vynel-Signature", "")
    if not ts.isdigit() or abs(time.time() - int(ts)) > TOLERANCE:
        return False
    mac = hmac.new(SECRET.encode(), ts.encode() + b"." + raw, hashlib.sha256).hexdigest()
    return hmac.compare_digest("sha256=" + mac, got)


def handle(event: dict):
    """Ваша логика. Событие может прийти дважды, поэтому действие должно быть безвредным при повторе."""
    kind, who = event["type"], event["entity"].get("username") or event["entity"].get("code")
    if kind == "user.extended":
        print(f"{who}: продлён на {event['data']}")
    elif kind == "user.status_changed":
        print(f"{who}: статус {event['data']['from']} → {event['data']['to']}")
    elif kind == "node.offline":
        print(f"нода {who} пропала со связи")
    else:
        print(kind, who, event["data"])


class Hook(BaseHTTPRequestHandler):
    def do_POST(self):
        raw = self.rfile.read(int(self.headers.get("Content-Length", 0)))
        if not verify(self.headers, raw):
            self.send_response(401)
            self.end_headers()
            return
        event = json.loads(raw)
        if event["type"] != "ping" and event["id"] not in seen:
            seen.add(event["id"])
            handle(event)
        self.send_response(204)  # быстро и 2xx, иначе панель повторит позже
        self.end_headers()

    def log_message(self, *_):
        pass


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 8000
    print(f"слушаю :{port}")
    HTTPServer(("", port), Hook).serve_forever()
