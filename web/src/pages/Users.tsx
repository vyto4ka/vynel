import { FormEvent, useEffect, useMemo, useState } from 'react'
import { del, get, Group, patch, post, Template, User, UserDetails } from '../api'
import { UserExtras } from '../extras'
import { ago, bytes, clientTypes, date, dateTime, fromDateInput, GiB, isOnline, left, resetLabels, statusInfo, toDateInput } from '../format'
import { Badge, Bar, ChartLegend, CopyField, Drawer, Empty, Field, Icon, Info, Loading, Modal, Switch, TrafficChart, useAction, useConfirm, useLoad } from '../ui'
import { t as tr, Trans } from '../i18n'

const pageSize = 200

// Колонки, по которым сортирует сервер (список идёт страницами).
type SortKey = 'username' | 'status' | 'traffic' | 'expire' | 'groups' | 'online' | 'created'
const columns: [SortKey, string, string][] = [
  ['username', tr('users.name'), ''], ['status', tr('users.status'), ''], ['traffic', tr('common.traffic'), 'hide-sm'], ['expire', tr('users.expires'), ''],
  ['groups', tr('common.groups'), 'hide-sm'], ['online', tr('users.last_online'), 'hide-sm'], ['created', tr('users.created'), 'hide-sm'],
]

function savedSort(): { key: SortKey | ''; desc: boolean } {
  try {
    const v = JSON.parse(localStorage.getItem('vynel.users.sort') || '')
    if (v && typeof v.key === 'string') return v
  } catch { /* по умолчанию */ }
  return { key: '', desc: false }
}

function openId(): number | null {
  const m = location.hash.match(/^#\/users\/(\d+)/)
  return m ? Number(m[1]) : null
}

export function Users() {
  const [sort, setSort] = useState(savedSort)
  const [search, setSearch] = useState('')
  const [creating, setCreating] = useState(false)
  const [open, setOpen] = useState<number | null>(openId)
  const [page, setPage] = useState(0)
  useEffect(() => setPage(0), [sort, search])
  useEffect(() => {
    try { localStorage.setItem('vynel.users.sort', JSON.stringify(sort)) } catch { /* приватное окно */ }
  }, [sort])
  // Страницами: с десятками тысяч пользователей весь список разом тормозил и панель, и браузер.
  const list = useLoad(() => get<{ users: User[]; total: number }>(`users?search=${encodeURIComponent(search)}&sort=${sort.key}&dir=${sort.desc ? 'desc' : 'asc'}` +
    `&limit=${pageSize}&offset=${page * pageSize}`), [sort, search, page], 20000)
  const users = { ...list, data: list.data?.users }
  const total = list.data?.total ?? 0
  const groups = useLoad(() => get<Group[]>('groups'))
  const groupName = useMemo(() => new Map((groups.data || []).map((g) => [g.id, g.name])), [groups.data])

  useEffect(() => {
    const h = () => setOpen(openId())
    window.addEventListener('hashchange', h)
    return () => window.removeEventListener('hashchange', h)
  }, [])
  const show = (id: number | null) => {
    history.replaceState(null, '', id ? `#/users/${id}` : '#/users')
    setOpen(id)
  }
  // Первое нажатие — по возрастанию, второе — по убыванию, третье — сброс.
  const toggle = (k: SortKey) => setSort(sort.key !== k ? { key: k, desc: false } : !sort.desc ? { key: k, desc: true } : { key: '', desc: false })

  return (
    <>
      <div className="toolbar">
        <div className="grow search-box">
          <input className="input" placeholder={tr('users.search_by_name_or_note')} value={search} onChange={(e) => setSearch(e.target.value)} />
          <span className="muted search-icon"><Icon name="search" size={18} /></span>
        </div>
        <button className="btn primary" onClick={() => setCreating(true)}><Icon name="plus" /> <span className="hide-sm">{tr('common.user')}</span></button>
      </div>

      <div className="card">
        {!users.data ? <Loading error={users.error} /> : users.data.length === 0 ? (
          <Empty icon="🌱">{search ? tr('users.nobody_found') : <>{tr('users.no_users_yet')}<br />{tr('users.press_user_a_name_is')}</>}</Empty>
        ) : (
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  {columns.map(([k, label, cls]) => (
                    <th key={k} className={'sortable ' + cls + (sort.key === k ? ' sorted' : '')} onClick={() => toggle(k)} aria-sort={sort.key === k ? (sort.desc ? 'descending' : 'ascending') : 'none'}>
                      {label}<span className="sort-mark">{sort.key === k ? (sort.desc ? ' ↓' : ' ↑') : ' ↕'}</span>
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {users.data.map((u) => {
                  const st = statusInfo[u.status]
                  return (
                    <tr key={u.id} className={'click' + (open === u.id ? ' selected' : '')} onClick={() => show(u.id)}>
                      <td>
                        <div className="row" style={{ gap: 8, flexWrap: 'nowrap' }}>
                          {isOnline(u.onlineAt) && <span className="online-dot" title={tr('common.online_2')} />}
                          <b>{u.username}</b>
                        </div>
                        {u.note && <div className="muted small ellipsis" style={{ maxWidth: 220 }}>{u.note}</div>}
                      </td>
                      <td><Badge color={st.color} dot>{st.label}</Badge></td>
                      <td className="hide-sm" style={{ minWidth: 150 }}>
                        <div className="small text-2">{bytes(u.trafficUsed)} / {bytes(u.trafficLimit)}</div>
                        {u.trafficLimit != null && <Bar value={u.trafficUsed} max={u.trafficLimit} />}
                      </td>
                      <td className="nowrap">
                        <div>{u.expireAt ? date(u.expireAt) : '∞'}</div>
                        <div className={'small ' + (u.expireAt && u.expireAt - Date.now() / 1000 < 7 * 86400 ? 'pink-text' : 'muted')}>{left(u.expireAt)}</div>
                      </td>
                      <td className="small text-2 hide-sm">{u.groupIds.map((g) => groupName.get(g)).filter(Boolean).join(', ') || '—'}</td>
                      <td className="small muted nowrap hide-sm">{isOnline(u.onlineAt) ? <span className="miku-text">{tr('common.now')}</span> : ago(u.onlineAt)}</td>
                      <td className="small muted nowrap hide-sm">{date(u.createdAt)}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
        {total > pageSize && (
          <div className="row between card-pad small text-2">
            <span><Trans k="common.of" v={{ v: page * pageSize + 1, min: Math.min(total, (page + 1) * pageSize), total: total }} /></span>
            <div className="row" style={{ gap: 6 }}>
              <button className="btn sm ghost" disabled={page === 0} onClick={() => setPage(page - 1)}>{tr('common.back')}</button>
              <button className="btn sm ghost" disabled={(page + 1) * pageSize >= total} onClick={() => setPage(page + 1)}>{tr('common.next')}</button>
            </div>
          </div>
        )}
      </div>

      {creating && <CreateUser groups={groups.data || []} onClose={() => setCreating(false)} onCreated={(u) => { setCreating(false); users.reload(); show(u.id) }} />}
      {open && <UserDrawer id={open} groups={groups.data || []} onClose={() => show(null)} onChanged={users.reload} />}
    </>
  )
}

// Число из поля: пусто — «как в шаблоне» (null).
const num = (v: string): number | null => (v.trim() === '' ? null : Math.max(0, Number(v.replace(',', '.')) || 0))

function CreateUser({ groups, onClose, onCreated }: { groups: Group[]; onClose: () => void; onCreated: (u: User) => void }) {
  const templates = useLoad(() => get<Template[]>('templates'))
  const [username, setUsername] = useState('')
  const [templateId, setTemplateId] = useState(0)
  const [note, setNote] = useState('')
  const [ownGroups, setOwnGroups] = useState<number[] | null>(null)
  const [subToken, setSubToken] = useState('')
  // Пустые поля берутся из шаблона: он показан в них серым.
  const [months, setMonths] = useState('')
  const [days, setDays] = useState('')
  const [never, setNever] = useState(false)
  const [traffic, setTraffic] = useState('')
  const [reset, setReset] = useState('')
  const [hwid, setHwid] = useState('')
  const [ip, setIp] = useState('')
  const session = useLoad(() => get<{ subBase: string; ipDefault: number; hwidDefault: number }>('session'))
  const act = useAction()
  const tpl = templates.data?.find((t) => (templateId ? t.id === templateId : t.isDefault))

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const body: Record<string, unknown> = { username, templateId, note, groupIds: ownGroups ?? undefined, subToken: subToken.trim() }
    if (never) body.neverExpires = true
    else if (num(months) != null || num(days) != null) {
      const d = new Date()
      d.setMonth(d.getMonth() + (num(months) ?? 0))
      d.setDate(d.getDate() + (num(days) ?? 0))
      body.expireAt = Math.floor(d.getTime() / 1000)
    }
    if (num(traffic) != null) body.trafficLimit = Math.round(num(traffic)! * GiB)
    if (reset) body.resetStrategy = reset
    if (num(hwid) != null) body.hwidLimit = num(hwid)
    if (num(ip) != null) body.ipLimit = num(ip)
    const u = await act(() => post<User>('users', body), tr('users.user_created', { username }))
    if (u) onCreated(u)
  }
  const tplExpiry = tpl && (tpl.expireMonths || tpl.expireDays)
  const fromTpl = <span className="from-tpl">{tr('users.from_the_template')}</span>

  return (
    <Modal title={tr('users.new_user')} onClose={onClose}
      footer={<><button className="btn ghost" onClick={onClose}>{tr('common.cancel')}</button><button className="btn primary" form="create-user" disabled={!username.trim()}>{tr('common.create')}</button></>}>
      <form id="create-user" onSubmit={submit}>
        <Field label={tr('users.name')} help={tr('users.latin_letters_digits_and_e')}>
          <input className="input" autoFocus value={username} onChange={(e) => setUsername(e.target.value)} placeholder="vasya" />
        </Field>
        <Field label={tr('common.template')} help={tr('users.grey_template_values')} info={tr('users.leave_a_field_alone_and')}>
          <select className="input" value={templateId} onChange={(e) => setTemplateId(Number(e.target.value))}>
            <option value={0}><Trans k="common.default_2" v={{ v: templates.data?.find((t) => t.isDefault) ? ` (${templates.data.find((t) => t.isDefault)!.name})` : '' }} /></option>
            {templates.data?.filter((t) => !t.isDefault).map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
          </select>
        </Field>
        <div className="grid2 keep2">
          <Field label={<><Trans k="users.term_months" v={{ v: months === '' && !never && fromTpl }} /></>}>
            <input className="input" inputMode="numeric" disabled={never} value={months} onChange={(e) => setMonths(e.target.value.replace(/\D/g, ''))}
              placeholder={tplExpiry ? String(tpl!.expireMonths) : '—'} />
          </Field>
          <Field label={<><Trans k="users.and_days" v={{ v: days === '' && !never && fromTpl }} /></>}>
            <input className="input" inputMode="numeric" disabled={never} value={days} onChange={(e) => setDays(e.target.value.replace(/\D/g, ''))}
              placeholder={tplExpiry ? String(tpl!.expireDays) : tr('users.no_expiry')} />
          </Field>
        </div>
        <label className="check" style={{ marginTop: -6, marginBottom: 12 }}>
          <input type="checkbox" checked={never} onChange={(e) => setNever(e.target.checked)} />{' '}{tr('users.no_expiry')}</label>
        <div className="grid2 keep2">
          <Field label={<><Trans k="users.traffic_gb" v={{ v: traffic === '' && fromTpl }} /></>} help={tr('users.n0_no_limit')}>
            <input className="input" inputMode="decimal" value={traffic} onChange={(e) => setTraffic(e.target.value.replace(/[^\d.,]/g, ''))}
              placeholder={tpl?.trafficLimit ? String(+(tpl.trafficLimit / GiB).toFixed(2)) : tr('users.no_limit')} />
          </Field>
          <Field label={<><Trans k="users.traffic_reset" v={{ v: reset === '' && fromTpl }} /></>}>
            <select className={'input' + (reset === '' ? ' placeholder' : '')} value={reset} onChange={(e) => setReset(e.target.value)}>
              <option value="">{resetLabels[tpl?.resetStrategy || 'no']}</option>
              {Object.entries(resetLabels).map(([k, l]) => <option key={k} value={k}>{l}</option>)}
            </select>
          </Field>
          <Field label={<><Trans k="users.devices" v={{ v: hwid === '' && fromTpl }} /></>} help={tr('users.n0_no_limit')}>
            <input className="input" inputMode="numeric" value={hwid} onChange={(e) => setHwid(e.target.value.replace(/\D/g, ''))}
              placeholder={tpl?.hwidLimit != null ? String(tpl.hwidLimit || '∞') : tr('users.shared', { v: session.data?.hwidDefault || '∞' })} />
          </Field>
          <Field label={<>{tr('users.ip_limit_at_once')}{' '}{ip === '' && <span className="from-tpl">{tr('users.shared_limit')}</span>}</>} help={tr('users.n0_no_limit')}>
            <input className="input" inputMode="numeric" value={ip} onChange={(e) => setIp(e.target.value.replace(/\D/g, ''))}
              placeholder={String(session.data?.ipDefault || '∞')} />
          </Field>
        </div>
        <Field label={<><Trans k="users.groups" v={{ v: ownGroups === null && fromTpl }} /></>}>
          <div className="row">
            {groups.map((g) => {
              const on = (ownGroups ?? tpl?.groupIds ?? []).includes(g.id)
              return (
                <label key={g.id} className={'check' + (ownGroups === null ? ' muted' : '')}>
                  <input type="checkbox" checked={on} onChange={() => {
                    const cur = ownGroups ?? tpl?.groupIds ?? []
                    setOwnGroups(on ? cur.filter((x) => x !== g.id) : [...cur, g.id])
                  }} />
                  {g.name}
                </label>
              )
            })}
          </div>
        </Field>
        <Field label={tr('users.note')}>
          <input className="input" value={note} onChange={(e) => setNote(e.target.value)} placeholder={tpl?.note || tr('users.e_g_telegram_vasya')} />
        </Field>
        <Field label={tr('users.subscription_link')} help={tr('users.link_help')} info={tr('users.link_info')}>
          <div className="link-input">
            <span className="mono muted">{session.data?.subBase || '…/s/'}</span>
            <input className="input mono" value={subToken} onChange={(e) => setSubToken(e.target.value.replace(/[^A-Za-z0-9_-]/g, ''))} placeholder={tr('users.random')} maxLength={64} />
          </div>
        </Field>
      </form>
    </Modal>
  )
}

export function templateSummary(t: Template, groups: Group[]): string {
  const parts: string[] = []
  if (t.expireMonths || t.expireDays) parts.push('срок ' + [t.expireMonths && tr('users.mo', { expireMonths: t.expireMonths }), t.expireDays && tr('users.d', { expireDays: t.expireDays })].filter(Boolean).join(' '))
  else parts.push(tr('users.no_expiry'))
  parts.push(t.trafficLimit ? tr('users.reset', { v: bytes(t.trafficLimit), v2: resetLabels[t.resetStrategy] }) : tr('users.unlimited_traffic'))
  if (t.hwidLimit != null) parts.push(tr('users.devices_2', { v: t.hwidLimit || '∞' }))
  const g = t.groupIds.map((id) => groups.find((x) => x.id === id)?.name).filter(Boolean)
  if (g.length) parts.push('группы: ' + g.join(', '))
  if (t.grants?.length) parts.push(tr('users.cascades_and_subscriptions', { length: t.grants.length }))
  return parts.join(' · ')
}

function UserDrawer({ id, groups, onClose, onChanged }: { id: number; groups: Group[]; onClose: () => void; onChanged: () => void }) {
  const { data, error, reload } = useLoad(() => get<UserDetails>(`users/${id}`), [id], 20000)
  const act = useAction()
  const confirm = useConfirm()
  const [editing, setEditing] = useState(false)
  const [showQR, setShowQR] = useState(false)
  const [linkEdit, setLinkEdit] = useState<string | null>(null)

  const run = async (fn: () => Promise<unknown>, ok: string) => {
    const r = await act(fn, ok)
    if (r !== undefined) {
      reload()
      onChanged()
    }
  }

  if (!data) return <Drawer title="…" onClose={onClose}><Loading error={error} /></Drawer>
  const u = data.user
  const st = statusInfo[u.status]

  return (
    <Drawer title={<>{u.username} <Badge color={st.color} dot>{st.label}</Badge></>} onClose={onClose}
      actions={<button className="btn sm" onClick={() => setEditing(true)}><Icon name="edit" />{' '}{tr('common.edit')}</button>}>

      <div className="card card-pad">
        <div className="label" style={{ marginBottom: 8 }}>{tr('users.subscription_link_send_it_to')}<Info>{tr('users.on_a_phone_the_link')}</Info></div>
        {data.subUrl ? (
          <>
            <CopyField value={data.subUrl} />
            {linkEdit !== null && (
              <div className="link-input" style={{ marginTop: 10 }}>
                <span className="mono muted">{data.subUrl.slice(0, data.subUrl.length - (data.subUrl.split('/').pop() || '').length)}</span>
                <input className="input mono" autoFocus value={linkEdit} maxLength={64}
                  onChange={(e) => setLinkEdit(e.target.value.replace(/[^A-Za-z0-9_-]/g, ''))} />
                <button className="btn sm primary" disabled={linkEdit.length < 8} onClick={async () => {
                  if (!(await confirm(tr('users.the_old_link_will_stop'), tr('common.change')))) return
                  await run(() => post(`users/${u.id}/link`, { token: linkEdit }), tr('users.link_changed'))
                  setLinkEdit(null)
                }}>{tr('common.save')}</button>
                <button className="btn sm ghost" onClick={() => setLinkEdit(null)}>{tr('common.cancel')}</button>
              </div>
            )}
            <div className="row" style={{ marginTop: 10 }}>
              <button className="btn sm" onClick={() => setShowQR(!showQR)}>{showQR ? tr('users.hide_qr') : tr('users.show_qr')}</button>
              {linkEdit === null && <button className="btn sm" onClick={() => setLinkEdit(data.subUrl!.split('/').pop() || '')}><Icon name="edit" />{' '}{tr('users.custom_link')}</button>}
              <a className="btn sm" href={data.subUrl} target="_blank" rel="noreferrer"><Icon name="link" />{' '}{tr('users.open_the_page')}</a>
            </div>
            {showQR && <img className="qr" style={{ marginTop: 12 }} src={`api/qr?text=${encodeURIComponent(data.subUrl)}`} alt="QR" />}
          </>
        ) : <div className="alert amber"><Trans k="users.no_link_check_subscription" v={{ subError: data.subError }} /></div>}
      </div>

      <div className="card card-pad">
        <div className="kv">
          <div className="k">{tr('users.expires')}</div>
          <div>{u.expireAt ? <>{dateTime(u.expireAt)} <span className="muted">({left(u.expireAt)})</span></> : tr('users.no_expiry')}</div>
          <div className="k">{tr('common.traffic')}</div>
          <div><Trans k="users.of_reset" v={{ v: bytes(u.trafficUsed), v2: bytes(u.trafficLimit), v3: resetLabels[u.resetStrategy] || u.resetStrategy, v4: u.trafficLimit != null && <div style={{ marginTop: 6 }}><Bar value={u.trafficUsed} max={u.trafficLimit} /></div> }} /></div>
          <div className="k">{tr('users.total_over_all_time')}</div><div>{bytes(u.lifetimeUsed)}</div>
          <div className="k">{tr('common.devices')}</div><div>{u.hwidOff
            ? <span className="amber-text">{tr('users.hwid_is_not_checked_any')}</span>
            : <><Trans k="users.of" v={{ length: data.devices.length, v: (u.hwidLimit ?? data.hwidDefault) || '∞' }} />{' '}{u.hwidLimit == null && <span className="muted">{tr('users.default')}</span>}</>}</div>
          <div className="k">{tr('users.addresses_at_once_2')}</div><div><Trans k="users.of" v={{ length: data.ips.length, v: (u.ipLimit ?? data.ipDefault) || '∞' }} />{' '}{u.ipLimit == null && <span className="muted">{tr('users.default')}</span>}
            {data.ips.length > 0 && <div className="row small" style={{ marginTop: 6, gap: 6 }}>
              {data.ips.map((a) => (
                <span key={a.addr} className={'chip static' + (a.blocked ? ' pink-text' : '')}
                  title={a.blocked ? tr('users.over_the_limit_blocked_on') : a.admitted ? tr('users.connected_from', { v: dateTime(a.since) }) : tr('users.being_checked_short_connections')}>
                  {a.blocked ? '⛔ ' : a.admitted ? '' : '… '}<span className="mono">{a.addr}</span>
                </span>
              ))}
            </div>}
          </div>
          <div className="k">{tr('common.groups')}</div><div>{u.groupIds.map((g) => groups.find((x) => x.id === g)?.name).filter(Boolean).join(', ') || <span className="pink-text">{tr('users.none_the_subscription_will')}</span>}</div>
          <div className="k">{tr('common.online')}</div><div>{isOnline(u.onlineAt) ? <span className="miku-text">{tr('common.now')}</span> : ago(u.onlineAt)}</div>
          <div className="k">{tr('users.subscription_refreshed')}</div><div>{ago(u.subLastAt)} {u.subLastUA && <span className="muted small">· {u.subLastUA}</span>}</div>
          <div className="k">{tr('users.format')}</div><div>{clientTypes[u.clientType] || u.clientType}</div>
          {u.note && <><div className="k">{tr('users.note')}</div><div>{u.note}</div></>}
          <div className="k">{tr('users.created')}</div><div>{dateTime(u.createdAt)}</div>
        </div>
      </div>

      <div className="card card-pad">
        <div className="label" style={{ marginBottom: 10 }}>{tr('common.actions')}</div>
        <div className="row">
          <span className="text-2 small">{tr('users.extend')}</span>
          {[1, 3, 6, 12].map((m) => (
            <button key={m} className="btn sm primary" onClick={() => run(() => post(`users/${id}/extend`, { months: m }), tr('users.extended_by_mo', { m }))}><Trans k="users.mo_2" v={{ m: m }} /></button>
          ))}
        </div>
        <div className="row" style={{ marginTop: 10 }}>
          {u.enabled
            ? <button className="btn sm" onClick={() => run(() => patch(`users/${id}`, { enabled: false }), tr('users.disabled'))}>{tr('users.disable')}</button>
            : <button className="btn sm primary" onClick={() => run(() => patch(`users/${id}`, { enabled: true }), tr('users.enabled'))}>{tr('users.enable')}</button>}
          <button className="btn sm" onClick={() => run(() => post(`users/${id}/reset`), tr('users.traffic_reset_2'))}>{tr('users.reset_traffic')}</button>
          <button className="btn sm" onClick={async () => {
            if (await confirm(tr('users.the_old_subscription_link_will'), tr('users.issue_a_new_one')))
              run(() => post(`users/${id}/reissue`, { token: true }), tr('users.new_link_issued'))
          }}>{tr('users.new_link')}</button>
          <button className="btn sm" onClick={async () => {
            if (await confirm(tr('users.change_the_uuid_all_devices'), tr('common.change')))
              run(() => post(`users/${id}/reissue`, { uuid: true }), tr('users.uuid_changed'))
          }}>{tr('users.change_uuid')}</button>
          <button className="btn sm danger" onClick={async () => {
            if (await confirm(<><Trans k="users.delete_the_user_this_cannot" v={{ username: u.username }} /></>, tr('common.delete'))) {
              const r = await act(() => del(`users/${id}`), tr('users.user_deleted'))
              if (r !== undefined) { onChanged(); onClose() }
            }
          }}><Icon name="trash" />{' '}{tr('common.delete')}</button>
        </div>
      </div>

      <div className="card">
        <div className="card-head">
          <div>
            <h3>{tr('users.devices_hwid')}</h3>
            <div className="card-sub">{u.hwidOff ? tr('users.the_check_is_off_for') : tr('users.of', { length: data.devices.length, v: (u.hwidLimit ?? data.hwidDefault) || '∞' })}</div>
          </div>
          <label className="row small text-2" style={{ gap: 8, flexWrap: 'nowrap' }}>
            <span>{tr('users.check_hwid')}<Info>{tr('users.turn_it_off_for_apps')}</Info></span>
            <Switch checked={!u.hwidOff} onChange={(v) => run(() => patch(`users/${id}`, { hwidOff: !v }), v ? tr('users.hwid_check_is_on') : tr('users.hwid_is_off_for_this'))} />
          </label>
        </div>
        {data.devices.length === 0 ? <Empty>{tr('users.devices_appear_after_the_first')}</Empty> : (
          <div className="table-wrap">
            <table className="table">
              <tbody>
                {data.devices.map((d) => (
                  <tr key={d.id}>
                    <td>
                      <div>{d.model || tr('users.device')} <span className="muted small">{d.platform} {d.osVersion}</span></div>
                      <div className="muted small ellipsis" style={{ maxWidth: 300 }}>{d.userAgent}</div>
                    </td>
                    <td className="small muted nowrap">{ago(d.lastSeen)}<br />{d.lastIp}</td>
                    <td style={{ width: 40 }}>
                      <button className="btn ghost icon sm" title={tr('common.unlink')} onClick={async () => {
                        if (await confirm(tr('users.unlink_the_device_the_slot'), tr('common.unlink')))
                          run(() => del(`users/${id}/devices/${d.id}`), tr('users.device_unlinked'))
                      }}><Icon name="trash" /></button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      <div className="card">
        <div className="card-head"><h3>{tr('users.traffic_in_30_days')}</h3><ChartLegend /></div>
        <div className="card-pad">
          <TrafficChart days={data.daily} />
          {data.trafficByNode.length > 0 && (
            <div className="row small text-2" style={{ marginTop: 8 }}>
              {data.trafficByNode.map((t) => <span key={t.code} className="chip static">{t.code}: {bytes(t.bytes)}</span>)}
            </div>
          )}
          <a className="small" href={`#/insights/traffic/${id}`} style={{ display: 'inline-block', marginTop: 8 }}>{tr('users.traffic_by_profile')}</a>
        </div>
      </div>

      <UserExtras userId={id} grants={data.grants} groups={groups} onChanged={reload} />

      <div className="card">
        <div className="card-head"><h3>{tr('users.servers_in_the_subscription')}</h3><span className="card-sub">{tr('users.separate_links_for_manual_import')}</span></div>
        <div className="card-pad">
          {data.links.length === 0 ? <div className="muted">{tr('users.no_servers_available_check')}</div> :
            data.links.map((l, i) => (
              <div key={i} style={{ marginBottom: 12 }}>
                <div className="small text-2" style={{ marginBottom: 5 }}>{l.remark} <span className="muted">· {l.tag}{l.hidden ? tr('users.hidden') : ''}</span></div>
                <CopyField value={l.link} />
              </div>
            ))}
        </div>
      </div>

      {editing && <EditUser u={u} hwidDefault={data.hwidDefault} ipDefault={data.ipDefault} groups={groups} onClose={() => setEditing(false)}
        onSaved={() => { setEditing(false); reload(); onChanged() }} />}
    </Drawer>
  )
}

function EditUser({ u, hwidDefault, ipDefault, groups, onClose, onSaved }: { u: User; hwidDefault: number; ipDefault: number; groups: Group[]; onClose: () => void; onSaved: () => void }) {
  const [expire, setExpire] = useState(toDateInput(u.expireAt))
  const [limitGB, setLimitGB] = useState(u.trafficLimit ? String(Math.round((u.trafficLimit / GiB) * 100) / 100) : '')
  const [hwid, setHwid] = useState(u.hwidLimit == null ? '' : String(u.hwidLimit))
  const [ipLimit, setIPLimit] = useState(u.ipLimit == null ? '' : String(u.ipLimit))
  const [reset, setReset] = useState(u.resetStrategy)
  const [clientType, setClientType] = useState(u.clientType)
  const [note, setNote] = useState(u.note)
  const [groupIds, setGroupIds] = useState(u.groupIds)
  const act = useAction()

  const save = async (e: FormEvent) => {
    e.preventDefault()
    const exp = fromDateInput(expire)
    const body: any = {
      note, clientType, resetStrategy: reset, groupIds,
      hwidLimit: hwid === '' ? -1 : Number(hwid),
      ipLimit: ipLimit === '' ? -1 : Number(ipLimit),
      setTrafficLimit: true, trafficLimit: limitGB ? Math.round(Number(limitGB) * GiB) : 0,
    }
    if (exp) body.expireAt = exp
    else body.neverExpires = true
    const r = await act(() => patch(`users/${u.id}`, body), tr('common.saved'))
    if (r !== undefined) onSaved()
  }

  return (
    <Modal title={tr('users.edit', { username: u.username })} onClose={onClose}
      footer={<><button className="btn ghost" onClick={onClose}>{tr('common.cancel')}</button><button className="btn primary" form="edit-user">{tr('common.save')}</button></>}>
      <form id="edit-user" onSubmit={save}>
        <div className="grid2 keep2">
          <Field label={tr('users.valid_until')} help={tr('users.empty_no_expiry')}>
            <input className="input" type="date" value={expire} onChange={(e) => setExpire(e.target.value)} />
          </Field>
          <Field label={tr('common.traffic_limit_gb')} help={tr('common.empty_no_limit')}>
            <input className="input" inputMode="decimal" value={limitGB} onChange={(e) => setLimitGB(e.target.value.replace(',', '.'))} placeholder="∞" />
          </Field>
          <Field label={tr('common.traffic_reset')}>
            <select className="input" value={reset} onChange={(e) => setReset(e.target.value)}>
              {Object.entries(resetLabels).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
            </select>
          </Field>
          <Field label={tr('common.device_limit')} help={tr('users.empty_default_0_no_limit', { v: hwidDefault || '∞' })}>
            <input className="input" inputMode="numeric" value={hwid} onChange={(e) => setHwid(e.target.value.replace(/\D/g, ''))} placeholder={String(hwidDefault)} />
          </Field>
          <Field label={tr('users.ip_limit_at_once')} help={tr('users.empty_default_0_no_limit', { v: ipDefault || '∞' })}>
            <input className="input" inputMode="numeric" value={ipLimit} onChange={(e) => setIPLimit(e.target.value.replace(/\D/g, ''))} placeholder={String(ipDefault)} />
          </Field>
        </div>
        <Field label={tr('common.subscription_format')} help={tr('users.usually_no_need_to_change')} info={tr('users.auto_picks_the_format_by')}>
          <select className="input" value={clientType} onChange={(e) => setClientType(e.target.value)}>
            {Object.entries(clientTypes).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
          </select>
        </Field>
        <Field label={tr('common.groups')}>
          <div className="row">
            {groups.map((g) => (
              <label key={g.id} className="check">
                <input type="checkbox" checked={groupIds.includes(g.id)}
                  onChange={(e) => setGroupIds(e.target.checked ? [...groupIds, g.id] : groupIds.filter((x) => x !== g.id))} />
                {g.name}
              </label>
            ))}
          </div>
        </Field>
        <Field label={tr('users.note')}>
          <input className="input" value={note} onChange={(e) => setNote(e.target.value)} />
        </Field>
      </form>
    </Modal>
  )
}
