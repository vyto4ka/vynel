import { FormEvent, useState } from 'react'
import { del, get, GrantRow, Group, Node, patch, post, Profile } from '../api'
import { GroupExtras } from '../extras'
import { Empty, Field, Icon, Loading, Modal, PageInfo, useAction, useConfirm, useLoad } from '../ui'
import { t, Trans } from '../i18n'

const kindLabel: Record<string, string> = { profile: t('groups.profile'), node: t('common.node_2'), node_inbound: t('groups.inbound'), all: t('groups.all') }

export function Groups() {
  const groups = useLoad(() => get<Group[]>('groups'))
  const nodes = useLoad(() => get<Node[]>('nodes'))
  const profiles = useLoad(() => get<Profile[]>('profiles'))
  const grants = useLoad(() => get<GrantRow[]>('grants'))
  const [edit, setEdit] = useState<Group | 'new' | null>(null)
  const act = useAction()
  const confirm = useConfirm()

  const options: { kind: string; refId: number; label: string }[] = [
    { kind: 'all', refId: 0, label: t('groups.everything_on_the_servers_all') },
    ...(profiles.data || []).map((p) => ({ kind: 'profile', refId: p.id, label: t('groups.profile_on_all_nodes', { name: p.name }) })),
    ...(nodes.data || []).map((n) => ({ kind: 'node', refId: n.id, label: t('groups.node_all_its_inbounds', { flag: n.flag, name: n.name, code: n.code }) })),
    ...(nodes.data || []).flatMap((n) => n.inbounds.map((i) => ({ kind: 'node_inbound', refId: i.id, label: t('groups.inbound_2', { tag: i.tag }) }))),
  ]

  return (
    <>
      <div className="toolbar">
        <PageInfo>{t('groups.a_group_decides_which_servers')}</PageInfo>
        <button className="btn primary" onClick={() => setEdit('new')}><Icon name="plus" />{' '}{t('groups.group')}</button>
      </div>
      {!groups.data ? <Loading error={groups.error} /> : groups.data.length === 0 ? <div className="card"><Empty>{t('groups.no_groups')}</Empty></div> : (
        <div className="cards cols">
          {groups.data.map((g) => (
            <div key={g.id} className="card">
              <div className="card-head">
                <div>
                  <h3>{g.name}</h3>
                  <div className="card-sub"><Trans k="groups.users" v={{ users: g.users, v: g.description && ` · ${g.description}` }} /></div>
                </div>
                <div className="row" style={{ gap: 4 }}>
                  <button className="btn ghost icon sm" title={t('groups.rename')} onClick={() => setEdit(g)}><Icon name="edit" /></button>
                  <button className="btn ghost icon sm" title={t('common.delete')} onClick={async () => {
                    if (await confirm(<><Trans k="groups.delete_the_group" v={{ name: g.name }} />{' '}{g.users > 0 && t('groups.users_will_lose_the_access', { users: g.users })}</>, t('common.delete')))
                      act(() => del(`groups/${g.id}`), t('groups.group_deleted')).then((r) => { if (r !== undefined) groups.reload() })
                  }}><Icon name="trash" /></button>
                </div>
              </div>
              <div className="card-pad">
                <div className="label" style={{ marginBottom: 8 }}>{t('groups.access')}</div>
                <div className="row" style={{ gap: 6 }}>
                  {g.rules.length === 0 && <span className="pink-text small">{t('groups.nothing_members_get_no_servers')}</span>}
                  {g.rules.some((r) => r.kind === 'all') && g.rules.length > 1 && <span className="muted small">{t('groups.all_already_includes_the_rest')}</span>}
                  {g.rules.map((r) => (
                    <span key={r.kind + r.refId} className="chip">
                      <span className="muted">{kindLabel[r.kind] || r.kind}</span> {r.label}
                      <button title={t('common.remove')} onClick={() => act(() => del(`groups/${g.id}/access?kind=${r.kind}&refId=${r.refId}`), t('groups.access_removed')).then(() => groups.reload())}>×</button>
                    </span>
                  ))}
                </div>
                <select className="input" style={{ marginTop: 12 }} value="" onChange={(e) => {
                  const o = options[Number(e.target.value)]
                  if (o) act(() => post(`groups/${g.id}/access`, { kind: o.kind, refId: o.refId }), t('groups.access_added')).then(() => groups.reload())
                }}>
                  <option value="">{t('groups.grant_access')}</option>
                  {options.map((o, i) => g.rules.some((r) => r.kind === o.kind && r.refId === o.refId) ? null : <option key={i} value={i}>{o.label}</option>)}
                </select>
                <GroupExtras group={g} grants={grants.data || []} onChanged={grants.reload} />
              </div>
            </div>
          ))}
        </div>
      )}
      {edit && <GroupForm g={edit === 'new' ? null : edit} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); groups.reload() }} />}
    </>
  )
}

function GroupForm({ g, onClose, onSaved }: { g: Group | null; onClose: () => void; onSaved: () => void }) {
  const [name, setName] = useState(g?.name || '')
  const [description, setDescription] = useState(g?.description || '')
  const act = useAction()
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const r = await act(() => (g ? patch(`groups/${g.id}`, { name, description }) : post('groups', { name, description })), t('common.saved'))
    if (r !== undefined) onSaved()
  }
  return (
    <Modal title={g ? t('groups.group') : t('groups.new_group')} onClose={onClose}
      footer={<><button className="btn ghost" onClick={onClose}>{t('common.cancel')}</button><button className="btn primary" form="group-form" disabled={!name.trim()}>{t('common.save')}</button></>}>
      <form id="group-form" onSubmit={submit}>
        <Field label={t('common.name')}><input className="input" autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder={t('groups.e_g_premium')} /></Field>
        <Field label={t('groups.description')}><input className="input" value={description} onChange={(e) => setDescription(e.target.value)} /></Field>
      </form>
    </Modal>
  )
}
