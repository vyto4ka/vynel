import { useState } from 'react'
import { get, Network, NetRole, post, put, Setting } from '../api'
import { Badge, Field, Info, Loading, Switch, useAction, useConfirm, useLoad } from '../ui'
import { t, Trans } from '../i18n'

export function Settings() {
  const { data, error, reload } = useLoad(() => get<Setting[]>('settings'))
  if (!data) return <Loading error={error} />
  const sections = [...new Set(data.map((s) => s.section))]
  return (
    <div className="cards" style={{ maxWidth: 900 }}>
      <Stealth settings={data} />
      <NetworkCard />
      {sections.map((sec) => (
        <div key={sec} className="card">
          <div className="card-head"><h3>{sec}</h3></div>
          <div className="card-pad">
            {data.filter((s) => s.section === sec).map((s) => <SettingRow key={s.key + (s.value ?? '')} s={s} onSaved={reload} />)}
          </div>
        </div>
      ))}
    </div>
  )
}

const value = (all: Setting[], key: string) => {
  const s = all.find((x) => x.key === key)
  return s ? (s.set ? s.value ?? '' : s.default ?? '') : ''
}

// Сводка скрытия (docs/STEALTH.md §1) и быстрый переезд панели на случайный порт и путь.
function Stealth({ settings }: { settings: Setting[] }) {
  const act = useAction()
  const confirm = useConfirm()
  const webPort = value(settings, 'web.port')
  const subPort = value(settings, 'sub.port') || '443'
  const ownPort = webPort !== '' && webPort !== subPort
  const pageOn = value(settings, 'sub.page_enabled') !== 'false'
  const pwOn = value(settings, 'web.password_login') !== 'false'
  const items: [boolean, string, string][] = [
    [true, t('settings.secret_path'), t('settings.everything_outside_the_path')],
    [ownPort, t('settings.own_panel_port'), ownPort ? `:${webPort}` : t('settings.on_the_shared', { subPort })],
    [!pageOn, t('settings.subscription_page_hidden'), pageOn ? t('settings.a_browser_sees_the_subscription') : t('settings.a_browser_sees_the_decoy')],
    [!pwOn, t('settings.sign_in_only_through_telegram'), pwOn ? t('settings.login_form_is_on') : t('settings.login_form_is_off')],
  ]
  const run = async () => {
    if (!(await confirm(t('settings.the_panel_will_move_to'), t('settings.move'), false))) return
    const r = await act(() => post<{ webUrl: string }>('settings/stealth', { port: true, path: true }), t('settings.the_panel_has_moved'))
    if (r?.webUrl) location.href = r.webUrl + '#/settings'
  }
  return (
    <div className="card">
      <div className="card-head">
        <div><h3>{t('settings.hiding')}</h3><div className="card-sub">{t('settings.what_a_stranger_who_found')}</div></div>
        <button className="btn sm" onClick={run}>{t('settings.random_port_and_new_path')}</button>
      </div>
      <div className="card-pad stealth-grid">
        {items.map(([on, title, sub]) => (
          <div key={title} className={'stealth-item' + (on ? ' on' : '')}>
            <span className="dot" />
            <div><div>{title}</div><div className="muted small">{sub}</div></div>
          </div>
        ))}
      </div>
    </div>
  )
}

const roleNames: Record<NetRole['kind'], string> = { panel: t('common.panel'), sub: t('settings.subscriptions'), gateway: t('settings.node_gateway'), inbound: t('settings.inbound'), egress: t('settings.outbound') }
const roleColors: Record<NetRole['kind'], string> = { panel: 'pink', sub: 'pink', gateway: 'amber', inbound: 'green', egress: '' }
const purposeNames = { sub: t('settings.subscriptions'), panel: t('common.panel'), node: t('common.node_2') }

function RoleChips({ roles }: { roles: NetRole[] }) {
  if (roles.length === 0) return <span className="muted small">{t('common.not_used')}</span>
  return <span className="badges">{roles.map((r, i) => <Badge key={i} color={roleColors[r.kind]}>{roleNames[r.kind]}{r.tag ? ` ${r.tag}` : ''}</Badge>)}</span>
}

function NetworkCard() {
  const { data, error, reload } = useLoad(() => get<Network>('network'))
  return (
    <div className="card">
      <div className="card-head">
        <div><h3>{t('settings.addresses_and_roles')}</h3><div className="card-sub">{t('settings.which_ip_serves_what_and')}</div></div>
        <button className="btn ghost sm" onClick={reload}>{t('settings.check_dns_again')}</button>
      </div>
      {!data ? <div className="card-pad"><Loading error={error} /></div> : (
        <div className="card-pad">
          {data.domains.length > 0 && (
            <div className="dns-list">
              {data.domains.map((d) => (
                <div key={d.name} className="dns-row">
                  <Badge color={d.ok ? 'green' : 'red'} dot>{d.ok ? t('settings.dns_is_fine') : d.error || t('common.error')}</Badge>
                  <span className="mono">{d.name}</span>
                  <span className="muted small">{purposeNames[d.purpose]}{d.purpose === 'node' ? ` ${d.node}` : ''}</span>
                  <span className="small text-2 grow" style={{ textAlign: 'right' }}>
                    {d.resolved.length > 0 ? <>→ <span className="mono">{d.resolved.join(', ')}</span></> : null}
                    {!d.ok && d.expected.length > 0 && <>{' '}<Trans k="settings.needs" v={{ join: d.expected.join(t('settings.or')) }} /></>}
                  </span>
                </div>
              ))}
              <div className="muted small" style={{ marginTop: 6 }}>{t('settings.domain_behind_a_cdn')}{' '}<Info>{t('settings.a_domain_behind_a_cdn')}</Info></div>
            </div>
          )}
          {data.nodes.map((n) => (
            <div key={n.id} className="net-node">
              <div className="net-node-head"><b>{n.name}</b> <span className="muted mono small">{n.code}</span> {n.local && <Badge>{t('settings.panel_server')}</Badge>}</div>
              {n.allRoles.length > 0 && <div className="small text-2" style={{ margin: '4px 0 8px' }}>{t('settings.on_all_addresses')}{' '}<RoleChips roles={n.allRoles} /></div>}
              {n.addresses.length === 0 ? <div className="muted small">{t('settings.the_node_has_not_reported')}</div> : (
                <table className="table compact">
                  <tbody>
                    {n.addresses.map((a) => (
                      <tr key={a.id}>
                        <td className="mono nowrap" style={{ width: 170 }}>{a.ip}{a.primary && <span className="muted">{' '}{t('settings.primary')}</span>}</td>
                        <td className="small muted nowrap" style={{ width: 90 }}>{a.interface || '—'}{!a.onInterface && <div className="pink-text">{t('settings.not_on_the_interface')}</div>}</td>
                        <td><div className="row" style={{ gap: 4 }}><RoleChips roles={a.roles} /></div></td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          ))}
          <div className="muted small">{t('settings.where_addresses_are_chosen')}{' '}<Info>{t('settings.the_panel_and_subscriptions')}</Info></div>
        </div>
      )}
    </div>
  )
}

function SettingRow({ s, onSaved }: { s: Setting; onSaved: () => void }) {
  const initial = s.value ?? ''
  const [val, setValue] = useState(initial)
  const act = useAction()
  const confirm = useConfirm()
  const dirty = val !== initial
  const effective = s.set ? initial : s.default ?? ''
  const ip = s.key === 'web.address' || s.key === 'sub.address'

  const save = async (v: string) => {
    if (s.movesPanel && !(await confirm(t('settings.the_panel_will_move_to_2'), t('common.change'), false))) return
    const r = await act(() => put<{ webUrl: string }>('settings', { key: s.key, value: v }), t('common.saved'))
    if (r === undefined) return
    if (s.movesPanel && r.webUrl && r.webUrl !== location.origin + location.pathname) {
      location.href = r.webUrl + '#/settings'
      return
    }
    onSaved()
  }

  if (s.type === 'bool') {
    const on = effective === 'true'
    return (
      <div className="field">
        <div className="row between" style={{ flexWrap: 'nowrap' }}>
          <div>
            <div>{s.title}{s.help && <Info>{s.help}</Info>}</div>
            {s.hint && <div className="help muted small">{s.hint}</div>}
          </div>
          <Switch checked={on} onChange={(v) => save(v ? 'true' : 'false')} />
        </div>
      </div>
    )
  }
  const defaultLabel = ip ? (s.key === 'web.address' ? t('settings.same_as_subscriptions') : t('common.all_addresses')) : t('common.default_2', { v: s.default ? ` (${s.default})` : '' })
  return (
    <Field label={<>{s.title} <span className="muted mono" style={{ fontWeight: 400 }}>{s.key}</span></>} help={s.hint} info={s.help}>
      <div className="row" style={{ flexWrap: 'nowrap' }}>
        {s.type === 'select' ? (
          <select className="input" value={val} onChange={(e) => setValue(e.target.value)}>
            <option value="">{defaultLabel}</option>
            {s.options?.map((o) => <option key={o} value={o}>{o}</option>)}
          </select>
        ) : (
          <input className="input" value={val} inputMode={s.type === 'int' ? 'numeric' : undefined}
            onChange={(e) => setValue(e.target.value)} placeholder={s.default ? t('common.default_3', { default: s.default }) : ''}
            onKeyDown={(e) => e.key === 'Enter' && dirty && save(val)} />
        )}
        {dirty && <button className="btn primary" onClick={() => save(val)}>{t('common.save')}</button>}
        {dirty && <button className="btn ghost" onClick={() => setValue(initial)}>{t('common.cancel')}</button>}
      </div>
    </Field>
  )
}
