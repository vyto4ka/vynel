import { FormEvent, useState } from 'react'
import { del, get, post, put } from '../api'
import { ago } from '../format'
import { Badge, CopyField, Field, Info, Loading, Switch, useAction, useConfirm, useLoad } from '../ui'
import { t, Trans } from '../i18n'

interface WebSession { id: string; method: string; actor: string; ip: string; userAgent: string; createdAt: number; lastSeenAt: number; current: boolean }

const methodNames: Record<string, string> = { password: t('account.password'), link: t('account.link_from_the_bot'), telegram: 'Telegram' }

// Браузер и система из User-Agent: «Chrome · Windows».
function browser(ua: string): string {
  const l = ua.toLowerCase()
  const pick = (pairs: [string, string][]) => pairs.find(([k]) => l.includes(k))?.[1] || ''
  const b = pick([['telegram', 'Telegram'], ['edg/', 'Edge'], ['opr/', 'Opera'], ['yabrowser', t('account.yandex')], ['firefox', 'Firefox'], ['chrome', 'Chrome'], ['safari', 'Safari']])
  const o = pick([['android', 'Android'], ['iphone', 'iPhone'], ['ipad', 'iPad'], ['windows', 'Windows'], ['mac os', 'macOS'], ['linux', 'Linux']])
  return [b, o].filter(Boolean).join(' · ') || ua.slice(0, 60) || t('common.unknown')
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
          <div><h3>{t('account.sessions')}</h3><div className="card-sub">{t('account.where_the_panel_is_signed')}</div></div>
          <button className="btn sm" disabled={others === 0} onClick={async () => {
            if (await confirm(t('account.end_other_sessions_this_browser', { others }), t('account.end'))) act(() => post('sessions/end-others'), t('account.other_sessions_ended')).then(reload)
          }}>{t('account.end_the_others')}</button>
        </div>
        <div className="table-wrap">
          <table className="table">
            <tbody>
              {data.sessions.map((s) => (
                <tr key={s.id}>
                  <td>
                    <div>{browser(s.userAgent)} {s.current && <Badge color="green">{t('account.this_browser')}</Badge>}</div>
                    <div className="small muted"><Trans k="account.sign_in" v={{ v: methodNames[s.method] || s.method, v2: s.actor && ` · ${s.actor}` }} /></div>
                  </td>
                  <td className="mono small">{s.ip}</td>
                  <td className="small text-2 nowrap"><Trans k="account.activity" v={{ v: ago(s.lastSeenAt) }} /></td>
                  <td style={{ width: 50 }}>
                    {!s.current && <button className="btn ghost icon sm" title={t('account.end')} onClick={() => act(() => del(`sessions/${s.id}`), t('account.session_ended')).then(reload)}>✕</button>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
      <div className="card card-pad">
        <div style={{ marginBottom: 10 }}>{t('account.how_to_sign_in')}</div>
        <div className="seg" style={{ marginBottom: 10, flexWrap: 'wrap' }}>
          {([['password', t('account.login_and_password')], ['link', t('account.link_only')], ['miniapp', t('account.telegram_only')]] as const).map(([m, label]) => (
            <button key={m} className={data.loginMode === m ? 'on' : ''} disabled={m === 'miniapp' && !data.hasBot} onClick={async () => {
              if (m === data.loginMode) return
              const text = m === 'miniapp'
                ? t('account.the_panel_will_open_only')
                : m === 'link' ? t('account.the_login_form_disappears_you')
                : t('account.bring_back_sign_in_with')
              if (!(await confirm(text, t('common.change')))) return
              act(() => put('account/login-mode', { mode: m, sessionPaths: false }), t('account.sign_in_mode_changed')).then(reload)
            }}>{label}</button>
          ))}
        </div>
        <div className="help muted small">
          {data.loginMode === 'password' && t('account.login_and_password_form_links')}
          {data.loginMode === 'link' && <><Trans k="account.no_password_a_one_time" /></>}
          {data.loginMode === 'miniapp' && <><Trans k="account.telegram_mini_app_only_the" /></>}
          {!data.hasBot && t('account.telegram_only_becomes_available')}
        </div>
        {data.loginMode === 'link' && (
          <div className="row between" style={{ flexWrap: 'nowrap', marginTop: 14 }}>
            <div>
              <div>{t('account.random_address_for_every_session')}{' '}<Info>{t('account.the_panel_has_no_permanent')}</Info></div>
            </div>
            <Switch checked={data.sessionPaths} onChange={(on) => act(() => put('account/login-mode', { mode: 'link', sessionPaths: on }), on ? t('account.random_addresses_are_on') : t('account.random_addresses_are_off')).then(reload)} />
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
    const r = await act(() => post<{ login: string; password: string }>('account', { current, login, password: generate ? '' : password, generate }), t('account.sign_in_details_changed'))
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
          <div className="k">{t('common.login')}</div><div>{session.data.login}</div>
          <div className="k">{t('account.panel_address')}</div><div>{session.data.webUrl ? <CopyField value={session.data.webUrl} /> : <span className="muted">{t('account.no_domain_set')}</span>}</div>
          <div className="k">{t('account.version')}</div><div className="mono">{session.data.version}</div>
        </div>
      </div>
      <div className="card">
        <div className="card-head"><h3>{t('account.change_login_or_password')}</h3></div>
        <form className="card-pad" onSubmit={(e: FormEvent) => { e.preventDefault(); submit(false) }}>
          <Field label={t('account.current_password')}><input className="input" type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} /></Field>
          <Field label={t('account.new_login')} help={t('account.empty_keep_the_current_one')}><input className="input" autoComplete="username" value={login} onChange={(e) => setLogin(e.target.value)} placeholder={session.data.login} /></Field>
          <Field label={t('account.new_password')} help={t('account.at_least_8_characters_or')}><input className="input" type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} /></Field>
          <div className="row">
            <button className="btn primary" disabled={!current || password.length < 8}>{t('common.save')}</button>
            <button type="button" className="btn pink" disabled={!current} onClick={() => submit(true)}>{t('account.generate_password')}</button>
          </div>
          {result && (
            <div className="alert green" style={{ marginTop: 14 }}><Trans k="account.done_login_password" v={{ login: result.login }} /><div style={{ marginTop: 8 }}><CopyField value={result.password} /></div>
              <div className="small" style={{ marginTop: 8 }}>{t('account.save_it_it_is_shown')}</div>
            </div>
          )}
          <p className="muted small" style={{ marginBottom: 0 }}><Trans k="account.forgot_the_password_on_the" /></p>
        </form>
      </div>
      <Sessions />
    </div>
  )
}
