import { FormEvent, useEffect, useState } from 'react'
import { get, post } from '../api'

export function Login({ onLogin, error: initialError }: { onLogin: () => void; error?: string }) {
  const [login, setLogin] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState(initialError || '')
  const [busy, setBusy] = useState(false)
  const [opts, setOpts] = useState<{ password: boolean; bot: string } | null>(null)
  useEffect(() => { get<{ password: boolean; bot: string }>('login/options').then(setOpts, () => setOpts({ password: true, bot: '' })) }, [])

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await post('login', { login, password })
      onLogin()
    } catch (err: any) {
      setError(err.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="login-wrap">
      <form className="card login" onSubmit={submit}>
        <div className="brand">
          <div className="brand-logo">v</div>
          <div className="brand-name">vyn<span>el</span></div>
        </div>
        <h2>Вход в панель</h2>
        {opts && !opts.password ? (
          <>
            {error && <div className="alert pink" style={{ marginBottom: 14 }}>{error}</div>}
            <p className="text-2">Вход по паролю выключен. Откройте Telegram-бота панели{opts.bot && <> <a href={`https://t.me/${opts.bot}`}>@{opts.bot}</a></>} и нажмите кнопку «Панель» у поля ввода или отправьте <code>/login</code>.</p>
            <p className="muted small" style={{ marginBottom: 0 }}>Нет доступа к боту — на сервере: <code>vynel admin login-link</code></p>
          </>
        ) : <>
        <div className="field">
          <label htmlFor="login">Логин</label>
          <input id="login" className="input" autoComplete="username" autoFocus value={login} onChange={(e) => setLogin(e.target.value)} />
        </div>
        <div className="field">
          <label htmlFor="password">Пароль</label>
          <input id="password" className="input" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
        </div>
        {error && <div className="alert pink" style={{ marginBottom: 14 }}>{error}</div>}
        <button className="btn primary" style={{ width: '100%' }} disabled={busy || !login || !password}>Войти</button>
        <p className="muted small" style={{ marginBottom: 0 }}>
          Без пароля: команда <code>/login</code> в Telegram-боте панели.<br />
          Забыли пароль? На сервере: <code>vynel admin web password</code>
        </p>
        </>}
      </form>
    </div>
  )
}
