import { FormEvent, useState } from 'react'
import { Cascade, del, ExternalSub, get, GrantRow, Group, Node, patch, post, TemplateGrant, UserGrant } from './api'
import { ago, date, fromDateInput } from './format'
import { Badge, Empty, Field, Icon, Info, Loading, Modal, PageInfo, Switch, useAction, useConfirm, useLoad } from './ui'
import { t, Trans } from './i18n'

// ================================================================ каскады

export function CascadeList() {
  const cascades = useLoad(() => get<Cascade[]>('cascades'))
  const nodes = useLoad(() => get<Node[]>('nodes'))
  const [edit, setEdit] = useState<Cascade | 'new' | null>(null)
  const act = useAction()
  const confirm = useConfirm()
  return (
    <>
      <div className="toolbar">
        <PageInfo>
          <p>{t('extras.a_cascade_the_app_connects')}</p>
          <p>{t('extras.a_group_with_the_all')}</p>
        </PageInfo>
        <button className="btn primary" onClick={() => setEdit('new')}><Icon name="plus" />{' '}{t('extras.cascade')}</button>
      </div>
      {!cascades.data ? <Loading error={cascades.error} /> : cascades.data.length === 0 ? <div className="card"><Empty>{t('extras.no_cascades')}</Empty></div> : (
        <div className="card">
          <div className="table-wrap">
            <table className="table">
              <thead><tr><th /><th>{t('extras.cascade')}</th><th className="hide-sm">{t('extras.entry')}</th><th className="hide-sm">{t('extras.exit')}</th><th /></tr></thead>
              <tbody>
                {cascades.data.map((c) => (
                  <tr key={c.id}>
                    <td style={{ width: 50 }}>
                      <Switch checked={c.enabled} onChange={(v) => act(() => patch(`cascades/${c.id}`, { ...c, enabled: v }), v ? t('extras.cascade_turned_on') : t('extras.cascade_turned_off')).then(() => cascades.reload())} />
                    </td>
                    <td>
                      <div>{c.name}</div><div className="muted small">{c.title}</div>
                      {c.problem && <div className={'small ' + (c.enabled ? 'pink-text' : 'muted')}>⚠ {c.problem}</div>}
                    </td>
                    <td className="small hide-sm">{c.entry}</td>
                    <td className="small hide-sm">{c.exit}</td>
                    <td className="nowrap" style={{ width: 90 }}>
                      <button className="btn ghost icon sm" title={t('common.edit')} onClick={() => setEdit(c)}><Icon name="edit" /></button>
                      <button className="btn ghost icon sm" title={t('common.delete')} onClick={async () => {
                        if (await confirm(<><Trans k="extras.delete_the_cascade_it_disappears" v={{ name: c.name }} /></>, t('common.delete')))
                          act(() => del(`cascades/${c.id}`), t('extras.cascade_deleted')).then(() => cascades.reload())
                      }}><Icon name="trash" /></button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
      {edit && <CascadeForm c={edit === 'new' ? null : edit} nodes={nodes.data || []} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); cascades.reload() }} />}
    </>
  )
}

function CascadeForm({ c, nodes, onClose, onSaved }: { c: Cascade | null; nodes: Node[]; onClose: () => void; onSaved: () => void }) {
  const [name, setName] = useState(c?.name || '')
  const [entry, setEntry] = useState(c?.entryInboundId || 0)
  const [exit, setExit] = useState(c?.exitInboundId || 0)
  const [remark, setRemark] = useState(c?.remark || '')
  const act = useAction()
  const inbounds = nodes.flatMap((n) => n.inbounds.map((i) => ({ id: i.id, nodeId: n.id, label: `${n.flag} ${n.name} · ${i.tag}` })))
  const entryNode = inbounds.find((i) => i.id === entry)?.nodeId
  const exitNode = inbounds.find((i) => i.id === exit)?.nodeId
  const nodeName = (id?: number) => nodes.find((n) => n.id === id)?.name || ''
  // Название по умолчанию — из выбранных нод: «Нидерланды → Германия».
  const auto = entryNode && exitNode ? `${nodeName(entryNode)} → ${nodeName(exitNode)}` : ''
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const body = { name: name.trim() || auto, entryInboundId: entry, exitInboundId: exit, remark, enabled: c ? c.enabled : true }
    const r = await act(() => (c ? patch(`cascades/${c.id}`, body) : post('cascades', body)), t('extras.saved_nodes_get_the_changes'))
    if (r !== undefined) onSaved()
  }
  return (
    <Modal title={c ? t('extras.cascade') : t('extras.new_cascade')} onClose={onClose}
      footer={<><button className="btn ghost" onClick={onClose}>{t('common.cancel')}</button><button className="btn primary" form="cascade-form" disabled={!(name.trim() || auto) || !entry || !exit}>{t('common.save')}</button></>}>
      <form id="cascade-form" onSubmit={submit}>
        <Field label={t('extras.entry_node_1')} info={t('extras.where_the_app_connects_the')}>
          <select className="input" value={entry} onChange={(e) => setEntry(Number(e.target.value))}>
            <option value={0}>{t('extras.choose_an_inbound')}</option>
            {inbounds.map((i) => <option key={i.id} value={i.id}>{i.label}</option>)}
          </select>
        </Field>
        <Field label={t('extras.exit_node_2')} info={t('extras.which_inbound_of_another_node')}>
          <select className="input" value={exit} onChange={(e) => setExit(Number(e.target.value))}>
            <option value={0}>{t('extras.choose_an_inbound')}</option>
            {inbounds.filter((i) => i.nodeId !== entryNode).map((i) => <option key={i.id} value={i.id}>{i.label}</option>)}
          </select>
        </Field>
        <Field label={t('extras.name_in_the_panel')} help={t('extras.empty_from_the_entry_and')}>
          <input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder={auto || t('extras.netherlands_germany')} />
        </Field>
        <Field label={t('common.server_name_in_apps')} help={t('extras.empty_entry_flag_exit_flag')}>
          <input className="input" value={remark} onChange={(e) => setRemark(e.target.value)} placeholder={t('extras.netherlands_germany_2')} />
        </Field>
      </form>
    </Modal>
  )
}

// ================================================================ внешние подписки

export function ExternalList() {
  const subs = useLoad(() => get<ExternalSub[]>('externals'))
  const [edit, setEdit] = useState<ExternalSub | 'new' | null>(null)
  const act = useAction()
  const confirm = useConfirm()
  return (
    <>
      <div className="card">
        <div className="card-head">
          <h3>{t('extras.external_subscriptions')}{' '}<Info>
            <p>{t('extras.someone_else_s_subscription')}</p>
            <p>{t('extras.when_an_app_refreshes_the')}</p>
          </Info></h3>
          <button className="btn sm" onClick={() => setEdit('new')}><Icon name="plus" />{' '}{t('common.subscription')}</button>
        </div>
        {!subs.data ? <Loading error={subs.error} /> : subs.data.length === 0 ? <Empty>{t('extras.none_yet')}</Empty> : (
          <div className="table-wrap">
            <table className="table">
              <tbody>
                {subs.data.map((x) => (
                  <tr key={x.id}>
                    <td style={{ width: 50 }}>
                      <Switch checked={x.enabled} onChange={(v) => act(() => patch(`externals/${x.id}`, { ...x, enabled: v }), v ? t('extras.on') : t('extras.off')).then(() => subs.reload())} />
                    </td>
                    <td>
                      <div>{x.name} <Badge color={x.error ? 'pink' : 'green'}><Trans k="extras.servers" v={{ length: x.servers.length }} /></Badge></div>
                      <div className="muted small ellipsis mono" style={{ maxWidth: 420 }}>{x.url}</div>
                      {x.error && <div className="small pink-text">{x.error}</div>}
                      {x.servers.length > 0 && <div className="small text-2 ellipsis" style={{ maxWidth: 520 }}>{x.servers.join(' · ')}</div>}
                    </td>
                    <td className="small muted nowrap">{x.fetchedAt ? t('extras.updated', { v: ago(x.fetchedAt) }) : t('extras.not_downloaded_yet')}</td>
                    <td className="nowrap" style={{ width: 120 }}>
                      <button className="btn ghost icon sm" title={t('extras.download_now')} onClick={() => act(() => post(`externals/${x.id}/refresh`), t('common.updated')).then(() => subs.reload())}>↻</button>
                      <button className="btn ghost icon sm" title={t('common.edit')} onClick={() => setEdit(x)}><Icon name="edit" /></button>
                      <button className="btn ghost icon sm" title={t('common.delete')} onClick={async () => {
                        if (await confirm(<><Trans k="extras.delete_the_subscription_its" v={{ name: x.name }} /></>, t('common.delete')))
                          act(() => del(`externals/${x.id}`), t('extras.deleted')).then(() => subs.reload())
                      }}><Icon name="trash" /></button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
      {edit && <ExternalForm x={edit === 'new' ? null : edit} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); subs.reload() }} />}
    </>
  )
}

function ExternalForm({ x, onClose, onSaved }: { x: ExternalSub | null; onClose: () => void; onSaved: () => void }) {
  const [name, setName] = useState(x?.name || '')
  const [url, setURL] = useState(x?.url || '')
  const act = useAction()
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const body = { name, url, enabled: x ? x.enabled : true }
    const r = await act(() => (x ? patch(`externals/${x.id}`, body) : post('externals', body)), t('common.saved'))
    if (r !== undefined) onSaved()
  }
  return (
    <Modal title={x ? t('extras.external_subscription') : t('extras.new_external_subscription')} onClose={onClose}
      footer={<><button className="btn ghost" onClick={onClose}>{t('common.cancel')}</button><button className="btn primary" form="external-form" disabled={!url.trim()}>{t('common.save')}</button></>}>
      <form id="external-form" onSubmit={submit}>
        <Field label={t('extras.link')} help={sourceHelp}>
          <textarea className="input mono" rows={3} autoFocus value={url} onChange={(e) => setURL(e.target.value)} placeholder={sourcePlaceholder} />
        </Field>
        <Field label={t('common.name')} help={t('extras.empty_by_address_or_by')}><input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder={t('extras.pete_s_subscription')} /></Field>
      </form>
    </Modal>
  )
}

const sourceHelp = t('extras.a_subscription_link_https_the')
const sourcePlaceholder = t('extras.https_sub_example_com_s')

// PasteExternal — вставить чужую подписку или ссылки серверов прямо на месте: она создаётся
// в общем списке «Внешних» и сразу отдаётся тому, кто вызвал (пользователю или шаблону).
function PasteExternal({ withDate, onAdded }: { withDate?: boolean; onAdded: (id: number, until: number | null) => Promise<unknown> }) {
  const [url, setURL] = useState('')
  const [until, setUntil] = useState('')
  const [open, setOpen] = useState(false)
  const act = useAction()
  if (!open) return <button className="btn sm ghost" type="button" onClick={() => setOpen(true)}><Icon name="plus" />{' '}{t('extras.paste_a_subscription_or_links')}</button>
  const add = async () => {
    const x = await act(() => post<ExternalSub>('externals', { name: '', url, enabled: true }))
    if (!x) return
    await onAdded(x.id, fromDateInput(until))
    setURL(''); setUntil(''); setOpen(false)
  }
  return (
    <div className="paste-external">
      <textarea className="input mono" rows={3} autoFocus value={url} onChange={(e) => setURL(e.target.value)} placeholder={sourcePlaceholder} />
      <div className="help"><Trans k="extras.the_servers_go_into_the" v={{ sourceHelp: sourceHelp }} /></div>
      <div className="row" style={{ marginTop: 8 }}>
        {withDate && <input className="input" type="date" style={{ flex: 1, minWidth: 140 }} value={until} onChange={(e) => setUntil(e.target.value)} title={t('extras.until_empty_as_long_as')} />}
        <button className="btn sm ghost" type="button" onClick={() => setOpen(false)}>{t('common.cancel')}</button>
        <button className="btn sm primary" type="button" disabled={!url.trim()} onClick={add}>{t('common.add')}</button>
      </div>
    </div>
  )
}

// TemplateExtras — каскады и подписки, которые шаблон выдаёт каждому новому пользователю.
export function TemplateExtras({ value, onChange }: { value: TemplateGrant[]; onChange: (v: TemplateGrant[]) => void }) {
  const { name, options, reload } = useExtras()
  const has = (k: string, id: number) => value.some((g) => g.kind === k && g.refId === id)
  return (
    <div>
      <div className="row" style={{ gap: 6, marginBottom: value.length ? 8 : 0 }}>
        {value.map((g) => (
          <span key={g.kind + g.refId} className="chip">
            <span className="muted">{kindText[g.kind]}</span> {name(g.kind, g.refId)}
            <button type="button" title={t('common.remove')} onClick={() => onChange(value.filter((x) => !(x.kind === g.kind && x.refId === g.refId)))}>×</button>
          </span>
        ))}
      </div>
      {options.some((o) => !has(o.kind, o.refId)) && (
        <select className="input" value="" onChange={(e) => {
          const o = options[Number(e.target.value)]
          if (o) onChange([...value, { kind: o.kind, refId: o.refId }])
        }}>
          <option value="">{t('extras.cascade_or_subscription')}</option>
          {options.map((o, i) => has(o.kind, o.refId) ? null : <option key={i} value={i}>{o.label}</option>)}
        </select>
      )}
      <div style={{ marginTop: 8 }}>
        <PasteExternal onAdded={async (id) => { reload(); onChange([...value, { kind: 'external', refId: id }]) }} />
      </div>
    </div>
  )
}

// ================================================================ кому выдано

function useExtras() {
  const cascades = useLoad(() => get<Cascade[]>('cascades'))
  const externals = useLoad(() => get<ExternalSub[]>('externals'))
  const name = (kind: string, id: number) =>
    kind === 'cascade' ? cascades.data?.find((c) => c.id === id)?.title || t('extras.cascade_2', { id }) : externals.data?.find((x) => x.id === id)?.name || t('extras.subscription', { id })
  const options = [
    ...(cascades.data || []).map((c) => ({ kind: 'cascade' as const, refId: c.id, label: t('extras.cascade_3', { title: c.title }) })),
    ...(externals.data || []).map((x) => ({ kind: 'external' as const, refId: x.id, label: t('extras.subscription_2', { name: x.name }) })),
  ]
  return { name, options, reload: externals.reload }
}

const kindText = { cascade: t('common.cascade'), external: t('extras.subscription_3') }

// GroupExtras — каскады и внешние подписки группы (без срока: срок ставится пользователю).
export function GroupExtras({ group, grants, onChanged }: { group: Group; grants: GrantRow[]; onChanged: () => void }) {
  const { name, options } = useExtras()
  const act = useAction()
  const mine = grants.filter((g) => g.groupId === group.id)
  if (options.length === 0 && mine.length === 0) return null
  return (
    <>
      <div className="label" style={{ margin: '14px 0 8px' }}>{t('common.cascades_and_subscriptions')}</div>
      <div className="row" style={{ gap: 6 }}>
        {mine.map((g) => (
          <span key={g.id} className="chip">
            <span className="muted">{kindText[g.kind]}</span> {name(g.kind, g.refId)}
            <button title={t('common.remove')} onClick={() => act(() => del(`grants/${g.id}`), t('extras.removed')).then(onChanged)}>×</button>
          </span>
        ))}
      </div>
      <select className="input" style={{ marginTop: 10 }} value="" onChange={(e) => {
        const o = options[Number(e.target.value)]
        if (o) act(() => post('grants', { groupId: group.id, kind: o.kind, refId: o.refId }), t('extras.added')).then(onChanged)
      }}>
        <option value="">{t('extras.cascade_or_subscription')}</option>
        {options.map((o, i) => mine.some((g) => g.kind === o.kind && g.refId === o.refId) ? null : <option key={i} value={i}>{o.label}</option>)}
      </select>
    </>
  )
}

// UserExtras — каскады и внешние подписки пользователя: свои (со своим сроком) и через группы.
export function UserExtras({ userId, grants, groups, onChanged }: { userId: number; grants: UserGrant[]; groups: Group[]; onChanged: () => void }) {
  const { name, options, reload } = useExtras()
  const [adding, setAdding] = useState<number | ''>('')
  const [until, setUntil] = useState('')
  const act = useAction()
  const add = async () => {
    const o = options[Number(adding)]
    if (!o) return
    const r = await act(() => post('grants', { userId, kind: o.kind, refId: o.refId, expireAt: fromDateInput(until) }), t('extras.granted'))
    if (r !== undefined) { setAdding(''); setUntil(''); onChanged() }
  }
  return (
    <div className="card">
      <div className="card-head">
        <div><h3>{t('common.cascades_and_subscriptions')}</h3><div className="card-sub">{t('extras.own_ones_have_their_own')}</div></div>
      </div>
      <div className="card-pad">
        {grants.length === 0 ? <div className="muted small">{t('extras.none')}</div> : grants.map((g) => (
          <div key={g.kind + g.refId} className="row between" style={{ marginBottom: 8 }}>
            <div>
              <span className="muted small">{kindText[g.kind]}</span> {name(g.kind, g.refId)}{' '}
              {g.problem && <div className="pink-text small">⚠ {g.problem}</div>}
              {!g.active ? <Badge color="pink"><Trans k="extras.ended" v={{ v: date(g.expireAt) }} /></Badge> : g.expireAt ? <Badge><Trans k="extras.until" v={{ v: date(g.expireAt) }} /></Badge> : null}
              {g.groups.length > 0 && <span className="muted small">{' '}<Trans k="extras.via" v={{ join: g.groups.map((id) => groups.find((x) => x.id === id)?.name || id).join(', ') }} /></span>}
            </div>
            {g.grantId > 0 && (
              <button className="btn ghost icon sm" title={t('extras.take_back_their_own_grant')} onClick={() => act(() => del(`grants/${g.grantId}`), t('extras.removed')).then(onChanged)}><Icon name="trash" /></button>
            )}
          </div>
        ))}
        {options.length > 0 && (
          <div className="row" style={{ marginTop: 10 }}>
            <select className="input" style={{ flex: 2, minWidth: 180 }} value={adding} onChange={(e) => setAdding(e.target.value === '' ? '' : Number(e.target.value))}>
              <option value="">{t('extras.grant_a_cascade_or_subscription')}</option>
              {options.map((o, i) => <option key={i} value={i}>{o.label}</option>)}
            </select>
            <input className="input" type="date" style={{ flex: 1, minWidth: 140 }} value={until} onChange={(e) => setUntil(e.target.value)} title={t('extras.until_empty_as_long_as')} />
            <button className="btn sm primary" disabled={adding === ''} onClick={add}>{t('extras.grant')}</button>
          </div>
        )}
        <div style={{ marginTop: 10 }}>
          <PasteExternal withDate onAdded={async (id, until) => {
            const r = await act(() => post('grants', { userId, kind: 'external', refId: id, expireAt: until }), t('extras.subscription_added'))
            reload()
            if (r !== undefined) onChanged()
          }} />
        </div>
      </div>
    </div>
  )
}
