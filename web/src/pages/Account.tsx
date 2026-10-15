import { FormEvent, useState } from 'react'
import { del, get, post, put } from '../api'
import { ago } from '../format'
import { Badge, CopyField, Field, Loading, Switch, useAction, useConfirm, useLoad } from '../ui'

interface WebSession { id: string; method: string; actor: string; ip: string; userAgent: string; createdAt: number; lastSeenAt: number; current: boolean }

const methodNames: Record<string, string> = { password: 'пароль', link: 'ссылка из бота', telegram: 'Telegram' }

// Браузер и система из User-Agent: «Chrome · Windows».
function browser(ua: string): string {
  const l = ua.toLowerCase()
  const pick = (pairs: [string, string][]) => pairs.find(([k]) => l.includes(k))?.[1] || ''
  const b = pick([['telegram', 'Telegram'], ['edg/', 'Edge'], ['opr/', 'Opera'], ['yabrowser', 'Яндекс'], ['firefox', 'Firefox'], ['chrome', 'Chrome'], ['safari', 'Safari']])
  const o = pick([['android', 'Android'], ['iphone', 'iPhone'], ['ipad', 'iPad'], ['windows', 'Windows'], ['mac os', 'macOS'], ['linux', 'Linux']])
  return [b, o].filter(Boolean).join(' · ') || ua.slice(0, 60) || 'неизвестно'
}

function Sessions() {
  const { data, error, reload } = useLoad(() => get<{ sessions: WebSession[]; passwordLogin: boolean; loginMode: string; sessionPaths: boolean; hasBot: boolean }>('sessions'), [], 30000)
  const act = useAction()
  const confirm = useConfirm()
  if (!data) return <div className="card card-pad"><Loading error={error} /></div>
  const others = data.sessions.filter((s) => !s.current).length
  return (
    <>
      <div className="card">
        <div className="card-head">
          <div><h3>Сессии</h3><div className="card-sub">где выполнен вход в панель; о каждом новом входе пишет Telegram-бот</div></div>
          <button className="btn sm" disabled={others === 0} onClick={async () => {
            if (await confirm(`Завершить ${others} других сессий? Этот браузер останется.`, 'Завершить')) act(() => post('sessions/end-others'), 'Другие сессии завершены').then(reload)
          }}>Завершить остальные</button>
        </div>
        <div className="table-wrap">
          <table className="table">
            <tbody>
              {data.sessions.map((s) => (
                <tr key={s.id}>
                  <td>
                    <div>{browser(s.userAgent)} {s.current && <Badge color="green">этот браузер</Badge>}</div>
                    <div className="small muted">вход: {methodNames[s.method] || s.method}{s.actor && ` · ${s.actor}`}</div>
                  </td>
                  <td className="mono small">{s.ip}</td>
                  <td className="small text-2 nowrap">активность {ago(s.lastSeenAt)}</td>
                  <td style={{ width: 50 }}>
                    {!s.current && <button className="btn ghost icon sm" title="Завершить" onClick={() => act(() => del(`sessions/${s.id}`), 'Сессия завершена').then(reload)}>✕</button>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
      <div className="card card-pad">
        <div style={{ marginBottom: 10 }}>Как входить в панель</div>
        <div className="seg" style={{ marginBottom: 10, flexWrap: 'wrap' }}>
          {([['password', 'Логин и пароль'], ['link', 'Только по ссылке'], ['miniapp', 'Только Telegram']] as const).map(([m, label]) => (
            <button key={m} className={data.loginMode === m ? 'on' : ''} disabled={m === 'miniapp' && !data.hasBot} onClick={async () => {
              if (m === data.loginMode) return
              const text = m === 'miniapp'
                ? 'Панель будет открываться только кнопкой «Панель» в Telegram-боте. Вход из браузера и по ссылкам выключится.'
                : m === 'link' ? 'Форма логина пропадёт: войти можно будет по одноразовой ссылке — /login в боте или vynel admin login-link на сервере.'
                : 'Вернуть вход по логину и паролю?'
              if (!(await confirm(text, 'Сменить'))) return
              act(() => put('account/login-mode', { mode: m, sessionPaths: false }), 'Режим входа изменён').then(reload)
            }}>{label}</button>
          ))}
        </div>
        <div className="help muted small">
          {data.loginMode === 'password' && 'Форма логина и пароля. Ссылки из бота и Telegram Mini App тоже работают.'}
          {data.loginMode === 'link' && <>Без пароля: одноразовая ссылка из бота (/login) или с сервера (<code>vynel admin login-link</code>), а также кнопка «Панель» в боте.</>}
          {data.loginMode === 'miniapp' && <>Только Telegram Mini App: кнопка «Панель» в боте. Вернуть ссылки с сервера: <code>vynel admin web mode link</code></>}
          {!data.hasBot && ' «Только Telegram» станет доступно после подключения бота.'}
        </div>
        {data.loginMode === 'link' && (
          <div className="row between" style={{ flexWrap: 'nowrap', marginTop: 14 }}>
            <div>
              <div>Случайный адрес на каждую сессию</div>
              <div className="help muted small">
                У панели не будет постоянного адреса: каждая ссылка открывает свой случайный путь, и сессия работает только на нём. Всё остальное на домене — сайт-заглушка.
              </div>
            </div>
            <Switch checked={data.sessionPaths} onChange={(on) => act(() => put('account/login-mode', { mode: 'link', sessionPaths: on }), on ? 'Случайные адреса включены' : 'Случайные адреса выключены').then(reload)} />
          </div>
        )}
      </div>
    </>
  )
}

export function Account() {
  const session = useLoad(() => get<{ login: string; webUrl: string; version: string }>('session'))
  const [current, setCurrent] = useState('')
  const [login, setLogin] = useState('')
  const [password, setPassword] = useState('')
  const [result, setResult] = useState<{ login: string; password: string } | null>(null)
  const act = useAction()

  if (!session.data) return <Loading error={session.error} />

  const submit = async (generate: boolean) => {
    const r = await act(() => post<{ login: string; password: string }>('account', { current, login, password: generate ? '' : password, generate }), 'Данные входа изменены')
    if (r) {
      setResult(r)
      setCurrent('')
      setPassword('')
    }
  }

  return (
    <div className="cards" style={{ maxWidth: 760 }}>
      <div className="card card-pad">
        <div className="kv">
          <div className="k">Логин</div><div>{session.data.login}</div>
          <div className="k">Адрес панели</div><div>{session.data.webUrl ? <CopyField value={session.data.webUrl} /> : <span className="muted">домен не задан</span>}</div>
          <div className="k">Версия</div><div className="mono">{session.data.version}</div>
        </div>
      </div>
      <div className="card">
        <div className="card-head"><h3>Сменить логин или пароль</h3></div>
        <form className="card-pad" onSubmit={(e: FormEvent) => { e.preventDefault(); submit(false) }}>
          <Field label="Текущий пароль"><input className="input" type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} /></Field>
          <Field label="Новый логин" help="Пусто — оставить прежний"><input className="input" autoComplete="username" value={login} onChange={(e) => setLogin(e.target.value)} placeholder={session.data.login} /></Field>
          <Field label="Новый пароль" help="Минимум 8 символов. Или сгенерируйте случайный."><input className="input" type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} /></Field>
          <div className="row">
            <button className="btn primary" disabled={!current || password.length < 8}>Сохранить</button>
            <button type="button" className="btn pink" disabled={!current} onClick={() => submit(true)}>Сгенерировать пароль</button>
          </div>
          {result && (
            <div className="alert green" style={{ marginTop: 14 }}>
              Готово. Логин <b>{result.login}</b>, пароль:
              <div style={{ marginTop: 8 }}><CopyField value={result.password} /></div>
              <div className="small" style={{ marginTop: 8 }}>Сохраните его — показывается один раз. Другие сессии завершены.</div>
            </div>
          )}
          <p className="muted small" style={{ marginBottom: 0 }}>Забыли пароль — на сервере: <code>vynel admin web password</code></p>
        </form>
      </div>
      <Sessions />
    </div>
  )
}
