import { FormEvent, useState } from 'react'
import { post } from '../api'

export function Login({ onLogin }: { onLogin: () => void }) {
  const [login, setLogin] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

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
          Забыли пароль? На сервере: <code>vynel admin web password</code>
        </p>
      </form>
    </div>
  )
}
