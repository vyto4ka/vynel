import { FormEvent, useState } from 'react'
import { del, get, post, put } from '../api'
import { dateTime } from '../format'
import { Badge, CopyField, Field, Icon, Loading, Switch, useAction, useConfirm, useLoad } from '../ui'

interface BotInfo {
  hasToken: boolean
  status: { running: boolean; username?: string; error?: string }
  admins: { id: number; name: string; username?: string; boundAt: number }[]
  backupTime: string
  timezone: string
  alerts: boolean
  alertDelay: string
}

const zones = ['Europe/Moscow', 'Europe/Kaliningrad', 'Europe/Samara', 'Asia/Yekaterinburg', 'Asia/Omsk', 'Asia/Novosibirsk',
  'Asia/Krasnoyarsk', 'Asia/Irkutsk', 'Asia/Vladivostok', 'Europe/Minsk', 'Europe/Kyiv', 'Asia/Almaty', 'Asia/Tbilisi', 'Europe/Berlin', 'UTC']

export function Telegram() {
  const info = useLoad(() => get<BotInfo>('bot'), [], 10000)
  const [token, setToken] = useState('')
  const [code, setCode] = useState<{ code: string; minutes: number; link?: string; username?: string } | null>(null)
  const act = useAction()
  const confirm = useConfirm()

  if (!info.data) return <Loading error={info.error} />
  const d = info.data
  const save = async (body: Record<string, unknown>, ok = 'Сохранено') => {
    const r = await act(() => put<BotInfo>('bot', body), ok)
    if (r) info.setData(r)
    return r
  }
  const st = d.status

  return (
    <div className="cards" style={{ maxWidth: 860 }}>
      <div className="card">
        <div className="card-head">
          <div>
            <h3>Бот</h3>
            <div className="card-sub">Управление пользователями, вход в панель без пароля, бэкапы и уведомления о нодах — в личном чате с ботом</div>
          </div>
          {!d.hasToken ? <Badge>не настроен</Badge> : st.running ? <Badge color="green" dot>@{st.username}</Badge> : st.error ? <Badge color="pink" dot>ошибка</Badge> : <Badge color="amber" dot>запускается</Badge>}
        </div>
        <div className="card-pad">
          {st.error && <div className="alert pink" style={{ marginBottom: 14 }}>{st.error}</div>}
          {!d.hasToken && (
            <ol className="list-plain small text-2" style={{ marginTop: 0 }}>
              <li>В Telegram откройте <a href="https://t.me/BotFather" target="_blank" rel="noreferrer">@BotFather</a> → <code>/newbot</code> → имя и username бота.</li>
              <li>Скопируйте токен вида <code>123456789:AA…</code> и вставьте ниже.</li>
              <li>Затем привяжите свой аккаунт кодом — бот отвечает только привязанным админам.</li>
            </ol>
          )}
          <form className="row" style={{ flexWrap: 'nowrap' }} onSubmit={async (e: FormEvent) => {
            e.preventDefault()
            if (await save({ token }, 'Токен сохранён — бот запустится через несколько секунд')) setToken('')
          }}>
            <input className="input mono" type="password" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)}
              placeholder={d.hasToken ? 'токен задан — вставьте новый, чтобы сменить' : '123456789:AA…'} />
            <button className="btn primary" disabled={!token.trim()}>Сохранить</button>
            {d.hasToken && <button type="button" className="btn danger" onClick={async () => {
              if (await confirm('Выключить бота? Уведомления и ночные бэкапы приходить перестанут.', 'Выключить')) save({ token: '' }, 'Бот выключен')
            }}>Выключить</button>}
          </form>
        </div>
      </div>

      <div className="card">
        <div className="card-head">
          <div>
            <h3>Администраторы</h3>
            <div className="card-sub">Все с полными правами. Остальным бот не отвечает вовсе</div>
          </div>
          <button className="btn sm" disabled={!d.hasToken} onClick={async () => {
            const r = await act(() => post<{ code: string; minutes: number; link?: string; username?: string }>('bot/code'))
            if (r) setCode(r)
          }}><Icon name="plus" /> Привязать аккаунт</button>
        </div>
        <div className="card-pad">
          {code && (
            <div className="alert green" style={{ marginBottom: 14 }}>
              {code.link ? <>Нажмите «Открыть бота» в том Telegram-аккаунте, который хотите привязать, и затем «Start»:</> : <>Отправьте боту сообщение:</>}
              <div className="row" style={{ marginTop: 10 }}>
                {code.link && <a className="btn primary" href={code.link} target="_blank" rel="noreferrer"><Icon name="telegram" /> Открыть бота</a>}
                <div className="grow" style={{ minWidth: 200 }}><CopyField value={`/start ${code.code}`} /></div>
              </div>
              <div className="small" style={{ marginTop: 8 }}>Код одноразовый и действует {code.minutes} минут. Список ниже обновится сам.</div>
            </div>
          )}
          {d.admins.length === 0 ? <div className="muted small">Пока никого. Привязанный аккаунт будет получать уведомления и бэкапы.</div> : (
            <table className="table">
              <tbody>
                {d.admins.map((a) => (
                  <tr key={a.id}>
                    <td><b>{a.name || a.id}</b> {a.username && <span className="muted">@{a.username}</span>}<div className="small muted mono">id {a.id}</div></td>
                    <td className="small muted nowrap">с {dateTime(a.boundAt)}</td>
                    <td style={{ width: 40 }}>
                      <button className="btn ghost icon sm" title="Отвязать" onClick={async () => {
                        if (await confirm(<>Отвязать <b>{a.name || a.id}</b>? Бот перестанет ему отвечать.</>, 'Отвязать'))
                          act(() => del(`bot/admins/${a.id}`), 'Отвязан').then(() => info.reload())
                      }}><Icon name="trash" /></button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>

      <div className="card">
        <div className="card-head"><h3>Бэкапы</h3></div>
        <div className="card-pad">
          <div className="muted small" style={{ marginBottom: 14 }}>
            Архив: база панели (пользователи, ноды, ключи, настройки, статистика) и внутренний CA, которому доверяют ноды. Из него поднимается новая панель —
            установщик с <code>--restore файл</code> или <code>vynel restore файл</code>, ноды переподключаются сами. Последние 7 ночных бэкапов лежат и на сервере в <code>/var/lib/vynel/backups</code>.
          </div>
          <div className="grid2">
            <Field label="Каждый день в" help="Пусто — не присылать">
              <input className="input" type="time" defaultValue={d.backupTime === 'off' ? '' : d.backupTime}
                onBlur={(e) => { if ((e.target.value || 'off') !== d.backupTime) save({ backupTime: e.target.value }) }} />
            </Field>
            <Field label="Часовой пояс" help="Для расписания и времени в сообщениях">
              <select className="input" value={d.timezone} onChange={(e) => save({ timezone: e.target.value })}>
                {(zones.includes(d.timezone) ? zones : [d.timezone, ...zones]).map((z) => <option key={z} value={z}>{z}</option>)}
              </select>
            </Field>
          </div>
          <div className="row">
            <button className="btn" disabled={!st.running || d.admins.length === 0} onClick={() => act(() => post('bot/backup'), 'Бэкап отправлен в Telegram')}>
              <Icon name="telegram" /> Отправить бэкап сейчас
            </button>
            <a className="btn" href="api/backup" download><Icon name="refresh" /> Скачать бэкап</a>
          </div>
          <div className="small pink-text" style={{ marginTop: 10 }}>В бэкапе ключи всех нод и пользователей: храните его как пароль.</div>
        </div>
      </div>

      <div className="card">
        <div className="card-head"><h3>Уведомления о нодах</h3></div>
        <div className="card-pad">
          <div className="field">
            <div className="row between" style={{ flexWrap: 'nowrap' }}>
              <div>
                <div>Сообщать, если нода недоступна</div>
                <div className="help muted small">Одно сообщение на сбой: оно обновляется (с какого времени, сколько уже, последняя проверка), а когда нода вернётся — превращается в «снова в строю».</div>
              </div>
              <Switch checked={d.alerts} onChange={(v) => save({ alerts: v })} />
            </div>
          </div>
          <Field label="Через сколько секунд без связи сообщать" help="Короткие перезапуски нод не будут тревожить">
            <input className="input" style={{ maxWidth: 160 }} inputMode="numeric" defaultValue={d.alertDelay}
              onBlur={(e) => { if (e.target.value !== d.alertDelay) save({ alertDelay: Number(e.target.value) || 0 }) }} />
          </Field>
        </div>
      </div>
    </div>
  )
}
