import { FormEvent, useEffect, useState } from 'react'
import { get, post } from '../api'
import { Logo } from '../ui'
import { t, Trans } from '../i18n'

export function Login({ onLogin, error: initialError }: { onLogin: () => void; error?: string }) {
  const [login, setLogin] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState(initialError || '')
  const [busy, setBusy] = useState(false)
  const [opts, setOpts] = useState<{ password: boolean; bot: string; mode?: string } | null>(null)
  useEffect(() => { get<{ password: boolean; bot: string; mode?: string }>('login/options').then(setOpts, () => setOpts({ password: true, bot: '' })) }, [])

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
          <Logo />
          <div className="brand-name">vyn<span>el</span></div>
        </div>
        <h2>{t('login.sign_in_to_the_panel')}</h2>
        {opts && !opts.password ? (
          <>
            {error && <div className="alert pink" style={{ marginBottom: 14 }}>{error}</div>}
            {opts.mode === 'miniapp' ? (
              <>
                <p className="text-2"><Trans k="login.the_panel_opens_only_from" v={{ v: opts.bot && <> <a href={`https://t.me/${opts.bot}`}>@{opts.bot}</a></> }} /></p>
                <p className="muted small" style={{ marginBottom: 0 }}><Trans k="login.no_access_to_the_bot" /></p>
              </>
            ) : (
              <>
                <p className="text-2"><Trans k="login.sign_in_by_one_time" v={{ v: opts.bot && <> <a href={`https://t.me/${opts.bot}`}>@{opts.bot}</a></> }} /></p>
                <p className="muted small" style={{ marginBottom: 0 }}><Trans k="login.without_the_bot_on_the" /></p>
              </>
            )}
          </>
        ) : <>
        <div className="field">
          <label htmlFor="login">{t('common.login')}</label>
          <input id="login" className="input" autoComplete="username" autoFocus value={login} onChange={(e) => setLogin(e.target.value)} />
        </div>
        <div className="field">
          <label htmlFor="password">{t('login.password')}</label>
          <input id="password" className="input" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
        </div>
        {error && <div className="alert pink" style={{ marginBottom: 14 }}>{error}</div>}
        <button className="btn primary" style={{ width: '100%' }} disabled={busy || !login || !password}>{t('common.sign_in')}</button>
        <p className="muted small" style={{ marginBottom: 0 }}><Trans k="login.without_a_password_the_login" /><br /><Trans k="login.forgot_the_password_on_the" /></p>
        </>}
      </form>
    </div>
  )
}
