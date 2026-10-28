"""Небольшой клиент API Vynel. Нужен только requests: pip install requests

    export VYNEL_API=https://panel.example.com/<секретный путь>/api/v1
    export VYNEL_TOKEN=vyn_...

    from vynel import Vynel
    api = Vynel()
    print(api.me())
"""
import os
import time
import uuid

import requests


class VynelError(Exception):
    """Ответ с ошибкой. Ветвитесь по code, а не по message."""

    def __init__(self, status, code, message, field=None, request_id=None):
        super().__init__(f"{status} {code}: {message}" + (f" (поле {field})" if field else ""))
        self.status, self.code, self.message, self.field, self.request_id = status, code, message, field, request_id


class Vynel:
    def __init__(self, base=None, token=None, timeout=15, retries=4):
        self.base = (base or os.environ["VYNEL_API"]).rstrip("/")
        self.token = token or os.environ["VYNEL_TOKEN"]
        self.timeout, self.retries = timeout, retries
        self.http = requests.Session()
        self.http.headers["Authorization"] = f"Bearer {self.token}"

    def request(self, method, path, json=None, params=None, key=None):
        """Один запрос с повторами. Всё, что меняет данные, уходит с Idempotency-Key, и все повторы
        несут тот же ключ: если ответ потерялся, второй раз ничего не произойдёт."""
        headers = {}
        if method != "GET":
            headers["Idempotency-Key"] = key or str(uuid.uuid4())
        for attempt in range(self.retries + 1):
            try:
                r = self.http.request(method, self.base + path, json=json, params=params, headers=headers, timeout=self.timeout)
            except requests.RequestException:
                if attempt == self.retries:
                    raise
                time.sleep(min(2 ** attempt, 20))
                continue
            if r.status_code == 429 and attempt < self.retries:
                time.sleep(int(r.headers.get("Retry-After", "1")))
                continue
            if r.status_code >= 500 and attempt < self.retries:
                time.sleep(min(2 ** attempt, 20))
                continue
            if r.status_code >= 400:
                e = (r.json() or {}).get("error", {}) if r.content else {}
                raise VynelError(r.status_code, e.get("code", "unknown"), e.get("message", r.text), e.get("field"), e.get("request_id"))
            return r.json() if r.content else None

    # --- пользователи ---
    def me(self):
        return self.request("GET", "/me")

    def users(self, **filters):
        """Страница пользователей: status, search, username, telegram_id, external_id, limit, offset…"""
        return self.request("GET", "/users", params=filters)

    def all_users(self, **filters):
        offset, limit = 0, 200
        while True:
            page = self.users(limit=limit, offset=offset, **filters)
            yield from page["items"]
            offset += limit
            if offset >= page["total"]:
                return

    def find_user(self, **by):
        """Пользователь по external_id, telegram_id или username; None, если такого нет."""
        items = self.users(**by)["items"]
        return items[0] if items else None

    def user(self, user_id):
        return self.request("GET", f"/users/{user_id}")

    def create_user(self, username, key=None, **fields):
        return self.request("POST", "/users", json={"username": username, **fields}, key=key)

    def update_user(self, user_id, key=None, **fields):
        return self.request("PATCH", f"/users/{user_id}", json=fields, key=key)

    def extend(self, user_id, months=0, days=0, key=None):
        return self.request("POST", f"/users/{user_id}/extend", json={"months": months, "days": days}, key=key)

    def disable(self, user_id):
        return self.request("POST", f"/users/{user_id}/disable")

    def enable(self, user_id):
        return self.request("POST", f"/users/{user_id}/enable")

    def reset_traffic(self, user_id):
        return self.request("POST", f"/users/{user_id}/reset-traffic")

    def reissue(self, user_id, subscription_token=True, uuid_=False):
        return self.request("POST", f"/users/{user_id}/reissue", json={"subscription_token": subscription_token, "uuid": uuid_})

    def delete_user(self, user_id):
        return self.request("DELETE", f"/users/{user_id}")

    def subscription(self, user_id):
        return self.request("GET", f"/users/{user_id}/subscription")

    def traffic(self, user_id, days=30):
        return self.request("GET", f"/users/{user_id}/traffic", params={"days": days})

    # --- остальное ---
    def groups(self):
        return self.request("GET", "/groups")["items"]

    def templates(self):
        return self.request("GET", "/user-templates")["items"]

    def nodes(self):
        return self.request("GET", "/nodes")["items"]

    def overview(self):
        return self.request("GET", "/stats/overview")

    def events(self, after="now", types=None, limit=100):
        """Одна страница ленты событий. Сохраните next_cursor и передайте его в следующий раз."""
        params = {"after": after, "limit": limit}
        if types:
            params["types"] = ",".join(types)
        return self.request("GET", "/events", params=params)

    def follow_events(self, cursor="now", types=None, pause=5):
        """Бесконечно отдаёт новые события; для тех, кто не может принимать вебхуки."""
        while True:
            page = self.events(cursor, types)
            yield from page["items"]
            cursor = page["next_cursor"]
            if not page["has_more"]:
                time.sleep(pause)


if __name__ == "__main__":
    api = Vynel()
    me = api.me()
    print("токен:", me["token"]["name"], me["token"]["scopes"])
    print("пользователей:", api.users(limit=1)["total"])
