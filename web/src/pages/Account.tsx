import { FormEvent, useState } from 'react'
import { get, post } from '../api'
import { CopyField, Field, Loading, useAction, useLoad } from '../ui'

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
    <div className="cards" style={{ maxWidth: 640 }}>
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
              <div className="small" style={{ marginTop: 8 }}>Сохраните его — показывается один раз. Другие сессии разлогинены.</div>
            </div>
          )}
          <p className="muted small" style={{ marginBottom: 0 }}>Забыли пароль — на сервере: <code>vynel admin web password</code></p>
        </form>
      </div>
    </div>
  )
}
