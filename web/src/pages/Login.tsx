import { FormEvent, useEffect, useState } from 'react'
import { get, post } from '../api'
import { Icon, Logo } from '../ui'
import { lang, languages, setLang, t, Trans } from '../i18n'

export function Login({ onLogin, error: initialError }: { onLogin: () => void; error?: string }) {
  const [login, setLogin] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState(initialError || '')
  const [busy, setBusy] = useState(false)
  const [opts, setOpts] = useState<{ password: boolean; bot: string; mode?: string } | null>(null)

  useEffect(() => {
    if (initialError) setError(initialError)
  }, [initialError])

  useEffect(() => {
    get<{ password: boolean; bot: string; mode?: string }>('login/options').then(setOpts, () => setOpts({ password: true, bot: '' }))
  }, [])

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
      <div className="card login">
        <div className="login-head">
          <div className="brand">
            <Logo size={32} />
            <div className="brand-name">vyn<span>el</span></div>
          </div>
          <div className="side-lang" title={t('main.language')}>
            {languages.map((l) => (
              <button key={l.code} type="button" className={lang() === l.code ? 'on' : ''} title={l.name}
                onClick={() => { if (lang() !== l.code) setLang(l.code).then(() => location.reload()) }}>{l.short}</button>
            ))}
          </div>
        </div>
        <h2 className="login-title">{t('login.sign_in_to_the_panel')}</h2>
        {!opts ? (
          <div className="login-loading muted small">{t('common.loading')}</div>
        ) : !opts.password ? (
          <div className="login-body">
            {error && <div className="alert pink" style={{ marginBottom: 14 }}>{error}</div>}
            {opts.mode === 'miniapp' ? (
              <p className="text-2" style={{ marginTop: 0 }}>
                <Trans k="login.the_panel_opens_only_from" v={{ v: opts.bot && <> <a href={`https://t.me/${opts.bot}`}>@{opts.bot}</a></> }} />
              </p>
            ) : (
              <p className="text-2" style={{ marginTop: 0 }}>
                <Trans k="login.sign_in_by_one_time" v={{ v: opts.bot && <> <a href={`https://t.me/${opts.bot}`}>@{opts.bot}</a></> }} />
              </p>
            )}
            <div className="login-foot muted small">
              {opts.mode === 'miniapp' ? <Trans k="login.no_access_to_the_bot" /> : <Trans k="login.without_the_bot_on_the" />}
            </div>
          </div>
        ) : (
          <form className="login-body" onSubmit={submit}>
            <div className="field">
              <label htmlFor="login">{t('common.login')}</label>
              <input id="login" className="input" autoComplete="username" autoFocus value={login} onChange={(e) => setLogin(e.target.value)} />
            </div>
            <div className="field">
              <label htmlFor="password">{t('login.password')}</label>
              <input id="password" className="input" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
            </div>
            {error && <div className="alert pink" style={{ marginBottom: 14 }}>{error}</div>}
            <button className="btn primary login-submit" disabled={busy || !login || !password}>
              {busy ? <span className="spinner" /> : <Icon name="key" size={16} />}
              {t('common.sign_in')}
            </button>
            <div className="login-foot muted small">
              <div><Trans k="login.without_a_password_the_login" /></div>
              <div><Trans k="login.forgot_the_password_on_the" /></div>
            </div>
          </form>
        )}
      </div>
    </div>
  )
}
