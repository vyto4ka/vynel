"""Telegram-бот на aiogram 3, который выдаёт и показывает подписки через API Vynel.

    pip install aiogram requests
    export BOT_TOKEN=123456:AA...        # токен бота от @BotFather
    export VYNEL_API=... VYNEL_TOKEN=... # права users:read, users:write
    python3 aiogram_bot.py

Пользователь находится по telegram_id, поэтому бот не хранит ничего своего: всё лежит в панели.
Оплату здесь не разобрать: /renew продлевает сразу, в настоящем боте это делается после оплаты
(см. billing_flow.py). Клиент API синхронный, поэтому вызовы идут через asyncio.to_thread.
"""
import asyncio
import os
from datetime import datetime

from aiogram import Bot, Dispatcher
from aiogram.filters import Command
from aiogram.types import Message

from vynel import Vynel, VynelError

api = Vynel()
dp = Dispatcher()


def call(fn, *a, **kw):
    return asyncio.to_thread(fn, *a, **kw)


async def my_user(m: Message):
    return await call(api.find_user, telegram_id=m.from_user.id)


@dp.message(Command("start"))
async def start(m: Message):
    user = await my_user(m)
    if user is None:
        name = f"tg{m.from_user.id}"
        try:
            user = await call(api.create_user, name, telegram_id=m.from_user.id, note=f"@{m.from_user.username or ''}", key=f"start-{m.from_user.id}")
        except VynelError as e:
            return await m.answer(f"Не получилось создать подписку: {e.message}")
    sub = await call(api.subscription, user["id"])
    await m.answer(f"Ваша подписка: {sub['url'] or 'ссылка появится, когда администратор настроит домен'}")


@dp.message(Command("status"))
async def status(m: Message):
    user = await my_user(m)
    if user is None:
        return await m.answer("Подписки нет, нажмите /start")
    until = user["expire_at"] and datetime.fromisoformat(user["expire_at"].replace("Z", "+00:00")).strftime("%d.%m.%Y")
    limit = user["traffic_limit_bytes"]
    used = user["traffic_used_bytes"] / 1024**3
    await m.answer(f"Статус: {user['status']}\nДо: {until or 'без срока'}\nТрафик: {used:.1f} ГБ" + (f" из {limit / 1024**3:.0f} ГБ" if limit else ""))


@dp.message(Command("renew"))
async def renew(m: Message):
    user = await my_user(m)
    if user is None:
        return await m.answer("Подписки нет, нажмите /start")
    # В настоящем боте — после успешной оплаты; ключ из номера платежа защитит от двойного продления.
    user = await call(api.extend, user["id"], months=1)
    await m.answer("Продлено ещё на месяц.")


@dp.message(Command("newlink"))
async def newlink(m: Message):
    user = await my_user(m)
    if user is None:
        return await m.answer("Подписки нет, нажмите /start")
    user = await call(api.reissue, user["id"])
    await m.answer(f"Новая ссылка: {user['subscription_url']}\nСтарая больше не работает.")


async def main():
    await dp.start_polling(Bot(os.environ["BOT_TOKEN"]))


if __name__ == "__main__":
    asyncio.run(main())
