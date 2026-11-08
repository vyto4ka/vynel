import { useEffect, useState } from 'react'
import { get, Group, GroupDefaults, post, User } from '../api'
import { bytes, date, resetLabels, statusInfo } from '../format'
import { t } from '../i18n'
import { Badge, Empty, Field, Loading, Modal, useAction, useConfirm, useLoad } from '../ui'

export function GroupPolicySummary({ defaults: d }: { defaults: GroupDefaults }) {
  const parts: string[] = []
  if (d.expiry === 'date') parts.push(t('groups.summary_date', { date: date(d.expireAt) }))
  if (d.expiry === 'period') parts.push(t('groups.summary_period', { months: d.months, days: d.days }))
  if (d.expiry === 'never') parts.push(t('groups.never'))
  if (d.traffic === 'limit') parts.push(t('groups.summary_traffic', { limit: bytes(d.trafficLimit) }))
  if (d.traffic === 'unlimited') parts.push(t('groups.unlimited'))
  if (d.reset) parts.push(t('groups.summary_reset', { reset: resetLabels[d.reset] }))
  if (d.hwidLimit != null) parts.push(`HWID: ${d.hwidLimit || '∞'}`)
  if (d.ipLimit != null) parts.push(`IP: ${d.ipLimit || '∞'}`)
  return <p className="small" style={{ marginBottom: 0 }}>{parts.join(' · ') || t('groups.keep_all')}</p>
}

export function GroupMembers({ g, groups, onClose, onChanged }: { g: Group; groups: Group[]; onClose: () => void; onChanged: () => void }) {
  const [mode, setMode] = useState<'members' | 'add' | 'create'>('members')
  const [search, setSearch] = useState('')
  const [offset, setOffset] = useState(0)
  const [selected, setSelected] = useState<number[]>([])
  const [apply, setApply] = useState(!!g.defaults)
  const [username, setUsername] = useState('')
  const [busy, setBusy] = useState(false)
  const act = useAction()
  const confirm = useConfirm()
  const pageSize = 50
  const list = useLoad(() => get<{ users: User[]; total: number }>(`groups/${g.id}/users?members=${mode !== 'add'}&search=${encodeURIComponent(search)}&limit=${pageSize}&offset=${offset}`), [mode, search, offset])
  useEffect(() => { setSelected([]) }, [mode, search, offset])
  const rows = list.data?.users || []
  const ready = !busy && !list.loading && !list.error
  const change = async () => {
    if (!ready || !selected.length) return
    if (!await confirm(<>{t(mode === 'add' ? 'groups.confirm_add' : 'groups.confirm_remove', { count: selected.length })}{mode === 'add' && apply && g.defaults && <><GroupPolicySummary defaults={g.defaults} /><p>{t('groups.apply_warning')}</p></>}</>, t(mode === 'add' ? 'groups.add_selected' : 'groups.remove_selected'), mode !== 'add')) return
    setBusy(true)
    const r = await act(() => post(`groups/${g.id}/users`, { action: mode === 'add' ? 'add' : 'remove', ids: selected, applyDefaults: apply }), t('groups.members_saved'))
    setBusy(false)
    if (r !== undefined) { setSelected([]); list.reload(); onChanged() }
  }
  return <Modal wide title={t('groups.members_title', { name: g.name })} onClose={() => { if (!busy) onClose() }} footer={<>
    <button className="btn ghost" disabled={busy} onClick={onClose}>{t('common.close')}</button>
    {mode !== 'create' && <button className={'btn ' + (mode === 'add' ? 'primary' : 'pink')} disabled={!ready || !selected.length} onClick={change}>{t(mode === 'add' ? 'groups.add_selected' : 'groups.remove_selected')} ({selected.length})</button>}
  </>}>
    <div className="row" style={{ gap: 8, marginBottom: 16 }}>{(['members', 'add', 'create'] as const).map((v) => <button key={v} className={'btn ' + (v === mode ? 'tonal' : 'ghost')} disabled={busy} onClick={() => { setMode(v); setOffset(0); setSearch('') }}>{t(v === 'members' ? 'groups.in_group' : v === 'add' ? 'groups.add_users' : 'groups.create_user')}</button>)}</div>
    {mode === 'create' ? <form onSubmit={async (e) => {
      e.preventDefault(); if (busy) return
      setBusy(true)
      const r = await act(() => post(`groups/${g.id}/users`, { action: 'create', username }), t('groups.user_created'))
      setBusy(false)
      if (r !== undefined) { setUsername(''); setMode('members'); setOffset(0); list.reload(); onChanged() }
    }}>
      <p className="muted small">{t('groups.create_help')}</p>
      {g.defaults && <div className="alert"><GroupPolicySummary defaults={g.defaults} /></div>}
      <Field label={t('groups.username')}><input className="input" value={username} required maxLength={64} onChange={(e) => setUsername(e.target.value)} /></Field>
      <button className="btn primary" disabled={busy || !username.trim()}>{t('groups.create_user')}</button>
    </form> : <>
      <input className="input" aria-label={t('groups.search_users')} placeholder={t('groups.search_users')} value={search} disabled={busy} onChange={(e) => { setSearch(e.target.value); setOffset(0) }} />
      {mode === 'add' && g.defaults && <div className="alert" style={{ marginTop: 12 }}><label className="check"><input type="checkbox" checked={apply} disabled={busy} onChange={(e) => setApply(e.target.checked)} />{t('groups.apply_policy')}</label><GroupPolicySummary defaults={g.defaults} /><p className="small">{t('groups.apply_warning')}</p></div>}
      {mode === 'members' && <p className="muted small">{t('groups.remove_help')}</p>}
      {list.loading || list.error ? <Loading error={list.error} /> : !rows.length ? <Empty>{t('groups.no_matching_users')}</Empty> : <div className="table-wrap"><table className="table"><thead><tr>
        <th><input type="checkbox" aria-label={t('groups.select_page')} disabled={busy} checked={rows.length > 0 && rows.every((u) => selected.includes(u.id))} onChange={(e) => setSelected(e.target.checked ? rows.map((u) => u.id) : [])} /></th><th>{t('groups.username')}</th><th>{t('groups.user_status')}</th><th>{t('groups.terms')}</th><th>{t('groups.user_groups')}</th>
      </tr></thead><tbody>{rows.map((u) => <tr key={u.id}><td><input type="checkbox" aria-label={t('groups.select_user', { name: u.username })} checked={selected.includes(u.id)} disabled={busy} onChange={(e) => setSelected((ids) => e.target.checked ? [...ids, u.id] : ids.filter((id) => id !== u.id))} /></td><td><b>{u.username}</b>{u.note && <div className="muted small">{u.note}</div>}</td><td><Badge color={statusInfo[u.status]?.color}>{statusInfo[u.status]?.label || u.status}</Badge></td><td className="small"><div>{u.expireAt ? date(u.expireAt) : t('groups.never')}</div><div className="muted">{bytes(u.trafficUsed)} / {u.trafficLimit == null ? '∞' : bytes(u.trafficLimit)}</div></td><td className="small muted">{u.groupIds.map((id) => groups.find((group) => group.id === id)?.name || `#${id}`).join(', ') || '—'}</td></tr>)}</tbody></table></div>}
      {list.data && <div className="row" style={{ justifyContent: 'space-between', marginTop: 12 }}><span className="muted small">{t('groups.page_count', { from: list.data.total ? offset + 1 : 0, to: Math.min(offset + pageSize, list.data.total), total: list.data.total })}</span><div className="row"><button className="btn ghost sm" disabled={!ready || !offset} onClick={() => setOffset(Math.max(0, offset - pageSize))}>{t('groups.previous')}</button><button className="btn ghost sm" disabled={!ready || offset + pageSize >= list.data.total} onClick={() => setOffset(offset + pageSize)}>{t('groups.next')}</button></div></div>}
    </>}
  </Modal>
}
