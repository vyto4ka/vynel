import { useState } from 'react'
import { ApiToken, del, get, Integrations, patch, post } from '../api'
import { ago, date, dateTime } from '../format'
import { Badge, CopyField, Empty, Field, Info, Loading, Modal, PageInfo, useAction, useConfirm, useLoad } from '../ui'
import { Icon } from '../ui'
import { Key, t, Trans } from '../i18n'
import { snippet } from '../apiSnippets'
import { Webhooks } from './ApiWebhooks'
import { Tester } from './ApiTester'
import { Events } from './ApiEvents'

type Tab = 'start' | 'tokens' | 'webhooks' | 'tester' | 'events'
const tabs: { id: Tab; label: () => string }[] = [
  { id: 'start', label: () => t('integrations.tab_start') },
  { id: 'tokens', label: () => t('integrations.tab_tokens') },
  { id: 'webhooks', label: () => t('integrations.tab_webhooks') },
  { id: 'tester', label: () => t('integrations.tab_tester') },
  { id: 'events', label: () => t('integrations.tab_events') },
]

function loadTab(): Tab {
  try {
    const v = localStorage.getItem('vynel.apitab') as Tab
    return tabs.some((x) => x.id === v) ? v : 'start'
  } catch {
    return 'start'
  }
}

export function Api() {
  const info = useLoad(() => get<Integrations>('integrations'), [], 15000)
  const [tab, setTabState] = useState<Tab>(loadTab)
  // The tester's token lives here, so it stays while the tabs are switched.
  const [token, setToken] = useState('')
  const setTab = (v: Tab) => {
    setTabState(v)
    try { localStorage.setItem('vynel.apitab', v) } catch { /* приватный режим */ }
  }
  if (!info.data) return <Loading error={info.error} />
  const d = info.data
  return (
    <>
      <div className="toolbar">
        <div className="seg">
          {tabs.map((x) => <button key={x.id} className={tab === x.id ? 'on' : ''} onClick={() => setTab(x.id)}>{x.label()}</button>)}
        </div>
        <PageInfo>{t('integrations.about')}</PageInfo>
      </div>
      {tab === 'start' && <Start d={d} go={setTab} />}
      {tab === 'tokens' && <Tokens d={d} reload={info.reload} />}
      {tab === 'webhooks' && <Webhooks d={d} reload={info.reload} />}
      {tab === 'tester' && <Tester d={d} token={token} setToken={setToken} reloadTokens={info.reload} />}
      {tab === 'events' && <Events d={d} />}
    </>
  )
}

// ---------------------------------------------------------------- начало

function Start({ d, go }: { d: Integrations; go: (t: Tab) => void }) {
  return (
    <div className="cards" style={{ maxWidth: 860 }}>
      <div className="card">
        <div className="card-head"><h3>{t('integrations.start_title')}</h3></div>
        <div className="card-pad">
          <ol className="list-plain" style={{ marginTop: 0 }}>
            <li style={{ marginBottom: 14 }}>
              <b>{t('integrations.step1_title')}</b>
              <div className="small text-2">{t('integrations.step1_text')}</div>
              <button className="btn sm" style={{ marginTop: 8 }} onClick={() => go('tokens')}><Icon name="key" />{' '}{t('integrations.create_token')}</button>
            </li>
            <li style={{ marginBottom: 14 }}>
              <b>{t('integrations.step2_title')}</b>
              <div className="small text-2"><Trans k="integrations.step2_text" /></div>
              <pre className="snippet">{snippet('curl', { method: 'GET', url: d.baseUrl + '/me' })}</pre>
            </li>
            <li>
              <b>{t('integrations.step3_title')}</b>
              <div className="small text-2">{t('integrations.step3_text')}</div>
              <button className="btn sm" style={{ marginTop: 8 }} onClick={() => go('webhooks')}>{t('integrations.tab_webhooks')}</button>
            </li>
          </ol>
        </div>
      </div>

      <div className="card">
        <div className="card-head"><h3>{t('integrations.base_url')}</h3></div>
        <div className="card-pad">
          <CopyField value={d.baseUrl} />
          <div className="row" style={{ marginTop: 14, alignItems: 'flex-start' }}>
            <div className="grow">
              <b>{t('integrations.spec_title')}</b>
              <div className="small text-2">{t('integrations.spec_text')}</div>
            </div>
            <a className="btn sm" href={'api/v1/openapi.json'} target="_blank" rel="noreferrer"><Icon name="external" />{' '}{t('integrations.spec_json')}</a>
            <a className="btn sm" href={'api/v1/openapi.yaml'} target="_blank" rel="noreferrer"><Icon name="external" />{' '}{t('integrations.spec_yaml')}</a>
          </div>
          <div className="small muted" style={{ marginTop: 12 }}><Trans k="integrations.docs_text" /></div>
        </div>
      </div>

      <div className="card">
        <div className="card-head"><h3>{t('integrations.safe_title')}</h3></div>
        <div className="card-pad">
          <ul className="list-plain small" style={{ marginTop: 0 }}>
            {(['integrations.safe_1', 'integrations.safe_2', 'integrations.safe_3', 'integrations.safe_4', 'integrations.safe_5'] as Key[]).map((k) => <li key={k} style={{ marginBottom: 6 }}>{t(k)}</li>)}
          </ul>
        </div>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------- токены

export const scopeText: Record<string, [Key, Key]> = {
  'users:read': ['integrations.scope_users_read', 'integrations.scope_users_read_d'],
  'users:write': ['integrations.scope_users_write', 'integrations.scope_users_write_d'],
  'users:delete': ['integrations.scope_users_delete', 'integrations.scope_users_delete_d'],
  'groups:read': ['integrations.scope_groups_read', 'integrations.scope_groups_read_d'],
  'templates:read': ['integrations.scope_templates_read', 'integrations.scope_templates_read_d'],
  'nodes:read': ['integrations.scope_nodes_read', 'integrations.scope_nodes_read_d'],
  'stats:read': ['integrations.scope_stats_read', 'integrations.scope_stats_read_d'],
  'events:read': ['integrations.scope_events_read', 'integrations.scope_events_read_d'],
}

const stateText: Record<ApiToken['state'], Key> = { active: 'integrations.state_active', expired: 'integrations.state_expired', revoked: 'integrations.state_revoked' }

const presets: { id: string; label: Key; scopes: string[] }[] = [
  { id: 'billing', label: 'integrations.preset_billing', scopes: ['users:read', 'users:write', 'groups:read', 'templates:read', 'events:read'] },
  { id: 'bot', label: 'integrations.preset_bot', scopes: ['users:read', 'users:write', 'groups:read', 'templates:read', 'nodes:read', 'stats:read'] },
  { id: 'read', label: 'integrations.preset_read', scopes: ['users:read', 'groups:read', 'templates:read', 'nodes:read', 'stats:read', 'events:read'] },
  { id: 'full', label: 'integrations.preset_full', scopes: ['users:read', 'users:write', 'groups:read', 'templates:read', 'nodes:read', 'stats:read', 'events:read'] },
]

function Tokens({ d, reload }: { d: Integrations; reload: () => void }) {
  const [form, setForm] = useState<ApiToken | 'new' | null>(null)
  const [made, setMade] = useState<{ token: ApiToken; secret: string } | null>(null)
  const act = useAction()
  const confirm = useConfirm()
  return (
    <div className="card">
      <div className="card-head">
        <div className="grow" />
        <button className="btn primary" onClick={() => setForm('new')}><Icon name="plus" />{' '}{t('integrations.create_token')}</button>
      </div>
      {d.tokens.length === 0 ? <div className="card-pad"><Empty icon="key">{t('integrations.tokens_empty')}</Empty></div> : (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th>{t('integrations.col_name')}</th><th>{t('integrations.col_rights')}</th><th>{t('integrations.col_from')}</th>
                <th>{t('integrations.col_used')}</th><th>{t('integrations.col_state')}</th><th />
              </tr>
            </thead>
            <tbody>
              {d.tokens.map((k) => (
                <tr key={k.id} style={k.state !== 'active' ? { opacity: 0.6 } : undefined}>
                  <td><b>{k.name}</b><div className="small muted mono">{k.prefix}…</div></td>
                  <td><div className="badges">{k.scopes.map((s) => <Badge key={s} color={s === 'users:delete' ? 'pink' : undefined}>{s}</Badge>)}</div></td>
                  <td className="small">{k.cidrs.length ? k.cidrs.join(', ') : <span className="muted">{t('integrations.anywhere')}</span>}</td>
                  <td className="small nowrap">{k.lastUsedAt ? <>{ago(k.lastUsedAt)}<div className="muted mono">{k.lastUsedIp}</div></> : <span className="muted">{t('integrations.never_used')}</span>}
                    {k.uses > 0 && <div className="muted">{t('integrations.uses', { n: k.uses })}</div>}</td>
                  <td className="small nowrap">
                    <Badge color={k.state === 'active' ? 'green' : k.state === 'expired' ? 'amber' : 'pink'} dot>{t(stateText[k.state])}</Badge>
                    <div className="muted">{k.expiresAt ? t('integrations.expires', { date: date(k.expiresAt) }) : t('integrations.no_expiry')}</div>
                  </td>
                  <td className="nowrap" style={{ textAlign: 'right' }}>
                    {k.state === 'active' && <button className="btn ghost icon sm" title={t('common.edit')} onClick={() => setForm(k)}><Icon name="edit" /></button>}
                    {k.state === 'active'
                      ? <button className="btn ghost icon sm" title={t('integrations.revoke')} onClick={async () => {
                        if (await confirm(t('integrations.revoke_confirm', { name: k.name }), t('integrations.revoke')))
                          act(() => post(`integrations/tokens/${k.id}/revoke`), t('integrations.revoked')).then(reload)
                      }}><Icon name="power" /></button>
                      : <button className="btn ghost icon sm" title={t('integrations.remove_from_list')} onClick={async () => {
                        if (await confirm(t('integrations.remove_confirm', { name: k.name }), t('integrations.remove_from_list')))
                          act(() => del(`integrations/tokens/${k.id}`)).then(reload)
                      }}><Icon name="trash" /></button>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {form && <TokenForm d={d} token={form === 'new' ? null : form} onClose={() => setForm(null)}
        onSaved={(r) => { setForm(null); reload(); if (r) setMade(r) }} />}
      {made && (
        <Modal title={t('integrations.token_created')} onClose={() => setMade(null)} footer={<button className="btn primary" onClick={() => setMade(null)}>{t('integrations.done')}</button>}>
          <div className="alert amber" style={{ marginBottom: 14 }}>{t('integrations.token_warn')}</div>
          <CopyField value={made.secret} wrap />
          <div className="small muted" style={{ margin: '14px 0 6px' }}>{t('integrations.token_try')}</div>
          <pre className="snippet">{'export VYNEL_TOKEN=' + made.secret + '\n' + snippet('curl', { method: 'GET', url: d.baseUrl + '/me' })}</pre>
        </Modal>
      )}
    </div>
  )
}

const expiryOptions: { id: string; label: Key; days: number }[] = [
  { id: 'never', label: 'integrations.exp_never', days: 0 },
  { id: '7', label: 'integrations.exp_7', days: 7 },
  { id: '30', label: 'integrations.exp_30', days: 30 },
  { id: '90', label: 'integrations.exp_90', days: 90 },
  { id: '365', label: 'integrations.exp_365', days: 365 },
]

function TokenForm({ d, token, onClose, onSaved }: { d: Integrations; token: ApiToken | null; onClose: () => void; onSaved: (r?: { token: ApiToken; secret: string }) => void }) {
  const [name, setName] = useState(token?.name ?? '')
  const [scopes, setScopes] = useState<string[]>(token?.scopes ?? presets[0].scopes)
  const [cidrs, setCidrs] = useState((token?.cidrs ?? []).join('\n'))
  const [rate, setRate] = useState(token?.rateLimit ? String(token.rateLimit) : '')
  const [expiry, setExpiry] = useState(token ? 'keep' : '90')
  const act = useAction()
  const preset = presets.find((p) => p.scopes.length === scopes.length && p.scopes.every((s) => scopes.includes(s)))?.id ?? 'custom'

  const save = async () => {
    let expiresAt: number | null = null
    if (expiry === 'keep') expiresAt = token?.expiresAt ?? null
    else {
      const days = expiryOptions.find((o) => o.id === expiry)?.days ?? 0
      if (days) expiresAt = Math.floor(Date.now() / 1000) + days * 86400
    }
    const body = { name, scopes, cidrs: cidrs.split(/[\s,]+/).filter(Boolean), rateLimit: Number(rate) || 0, expiresAt }
    if (token) {
      if (await act(() => patch(`integrations/tokens/${token.id}`, body), t('integrations.token_saved'))) onSaved()
    } else {
      const r = await act(() => post<{ token: ApiToken; secret: string }>('integrations/tokens', body))
      if (r) onSaved(r)
    }
  }
  return (
    <Modal title={token ? t('integrations.edit_token', { name: token.name }) : t('integrations.create_token')} onClose={onClose}
      footer={<><button className="btn" onClick={onClose}>{t('integrations.cancel')}</button><button className="btn primary" disabled={!name.trim() || scopes.length === 0} onClick={save}>{token ? t('integrations.save') : t('integrations.create_token')}</button></>}>
      <Field label={t('integrations.f_name')} help={t('integrations.f_name_help')}>
        <input className="input" value={name} maxLength={64} autoFocus onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field label={t('integrations.f_preset')} info={t('integrations.preset_info')}>
        <select className="input" value={preset} onChange={(e) => { const p = presets.find((x) => x.id === e.target.value); if (p) setScopes(p.scopes) }}>
          {presets.map((p) => <option key={p.id} value={p.id}>{t(p.label)}</option>)}
          {preset === 'custom' && <option value="custom">{t('integrations.preset_custom')}</option>}
        </select>
      </Field>
      <Field label={t('integrations.f_rights')}>
        <div style={{ display: 'grid', gap: 8 }}>
          {d.scopes.map((s) => {
            const [label, hint] = scopeText[s] ?? [s as Key, s as Key]
            return (
              <label key={s} className="check" style={{ alignItems: 'flex-start' }}>
                <input type="checkbox" checked={scopes.includes(s)} onChange={(e) => setScopes(e.target.checked ? [...scopes, s] : scopes.filter((x) => x !== s))} />
                <span>{t(label)}{' '}<span className="mono muted small">{s}</span><div className="small muted">{t(hint)}</div></span>
              </label>
            )
          })}
        </div>
      </Field>
      <Field label={t('integrations.f_cidrs')} help={t('integrations.f_cidrs_help')} info={t('integrations.f_cidrs_info')}>
        <textarea className="input mono" rows={2} value={cidrs} placeholder="203.0.113.5" onChange={(e) => setCidrs(e.target.value)} />
      </Field>
      <div className="grid2">
        <Field label={t('integrations.f_rate')} help={t('integrations.f_rate_help', { n: d.defaultRate })} info={t('integrations.f_rate_info')}>
          <input className="input" inputMode="numeric" value={rate} onChange={(e) => setRate(e.target.value.replace(/\D/g, ''))} />
        </Field>
        <Field label={t('integrations.f_expiry')} help={token ? (token.expiresAt ? dateTime(token.expiresAt) : t('integrations.no_expiry')) : undefined}>
          <select className="input" value={expiry} onChange={(e) => setExpiry(e.target.value)}>
            {token && <option value="keep">{t('integrations.exp_keep')}</option>}
            {expiryOptions.map((o) => <option key={o.id} value={o.id}>{t(o.label)}</option>)}
          </select>
        </Field>
      </div>
    </Modal>
  )
}
