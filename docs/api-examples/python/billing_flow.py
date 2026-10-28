"""Подписка по оплате: что делает биллинг, когда клиент заплатил.

    export VYNEL_API=... VYNEL_TOKEN=...   # права users:read, users:write, templates:read
    python3 billing_flow.py order-1042 customer-77 1

Логика та же, что в любом биллинге: заказ оплачен → у клиента есть доступ на оплаченный срок.
Заказ может прийти дважды (платёжная система повторяет уведомления), поэтому ключ идемпотентности
берётся из номера заказа: повтор не продлит подписку второй раз.
"""
import sys

from vynel import Vynel, VynelError

api = Vynel()


def on_paid(order_id: str, customer_id: str, months: int) -> str:
    """Возвращает ссылку подписки, которую нужно показать клиенту."""
    # 1. Пользователь клиента: ищем по своему идентификатору, нет — создаём.
    user = api.find_user(external_id=customer_id)
    if user is None:
        try:
            user = api.create_user(f"c-{customer_id}", external_id=customer_id, key=f"create-{customer_id}")
        except VynelError as e:
            if e.code != "username_taken":
                raise
            user = api.find_user(username=f"c-{customer_id}")
    # 2. Продлеваем. Ключ привязан к заказу: второй вызов с тем же заказом вернёт первый ответ.
    user = api.extend(user["id"], months=months, key=f"order-{order_id}")
    # 3. Если пользователя до этого выключали (например, за неоплату), включаем.
    if not user["enabled"]:
        user = api.enable(user["id"])
    return user["subscription_url"] or ""


if __name__ == "__main__":
    order, customer, months = sys.argv[1], sys.argv[2], int(sys.argv[3])
    print(on_paid(order, customer, months))
