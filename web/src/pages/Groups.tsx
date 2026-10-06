import { FormEvent, useState } from 'react'
import { del, get, Group, Node, patch, post, Profile } from '../api'
import { Empty, Field, Icon, Loading, Modal, useAction, useConfirm, useLoad } from '../ui'

const kindLabel: Record<string, string> = { profile: 'профиль', node: 'нода', node_inbound: 'инбаунд' }

export function Groups() {
  const groups = useLoad(() => get<Group[]>('groups'))
  const nodes = useLoad(() => get<Node[]>('nodes'))
  const profiles = useLoad(() => get<Profile[]>('profiles'))
  const [edit, setEdit] = useState<Group | 'new' | null>(null)
  const act = useAction()
  const confirm = useConfirm()

  const options: { kind: string; refId: number; label: string }[] = [
    ...(profiles.data || []).map((p) => ({ kind: 'profile', refId: p.id, label: `Профиль «${p.name}» — на всех нодах` })),
    ...(nodes.data || []).map((n) => ({ kind: 'node', refId: n.id, label: `Нода ${n.flag} ${n.name} (${n.code}) — все её инбаунды` })),
    ...(nodes.data || []).flatMap((n) => n.inbounds.map((i) => ({ kind: 'node_inbound', refId: i.id, label: `Инбаунд ${i.tag}` }))),
  ]

  return (
    <>
      <div className="toolbar">
        <div className="text-2 grow small">
          Группа решает, какие серверы попадут в подписку. Пользователь видит всё, что доступно хотя бы одной его группе.
        </div>
        <button className="btn primary" onClick={() => setEdit('new')}><Icon name="plus" /> Группа</button>
      </div>
      {!groups.data ? <Loading error={groups.error} /> : groups.data.length === 0 ? <div className="card"><Empty>Групп нет</Empty></div> : (
        <div className="cards cols">
          {groups.data.map((g) => (
            <div key={g.id} className="card">
              <div className="card-head">
                <div>
                  <h3>{g.name}</h3>
                  <div className="card-sub">{g.users} польз.{g.description && ` · ${g.description}`}</div>
                </div>
                <div className="row" style={{ gap: 4 }}>
                  <button className="btn ghost icon sm" title="Переименовать" onClick={() => setEdit(g)}><Icon name="edit" /></button>
                  <button className="btn ghost icon sm" title="Удалить" onClick={async () => {
                    if (await confirm(<>Удалить группу <b>{g.name}</b>? {g.users > 0 && `${g.users} польз. потеряют доступ, который давала она.`}</>, 'Удалить'))
                      act(() => del(`groups/${g.id}`), 'Группа удалена').then((r) => { if (r !== undefined) groups.reload() })
                  }}><Icon name="trash" /></button>
                </div>
              </div>
              <div className="card-pad">
                <div className="label" style={{ marginBottom: 8 }}>Доступ</div>
                <div className="row" style={{ gap: 6 }}>
                  {g.rules.length === 0 && <span className="pink-text small">ничего — у участников не будет серверов</span>}
                  {g.rules.map((r) => (
                    <span key={r.kind + r.refId} className="chip">
                      <span className="muted">{kindLabel[r.kind] || r.kind}</span> {r.label}
                      <button title="Убрать" onClick={() => act(() => del(`groups/${g.id}/access?kind=${r.kind}&refId=${r.refId}`), 'Доступ убран').then(() => groups.reload())}>×</button>
                    </span>
                  ))}
                </div>
                <select className="input" style={{ marginTop: 12 }} value="" onChange={(e) => {
                  const o = options[Number(e.target.value)]
                  if (o) act(() => post(`groups/${g.id}/access`, { kind: o.kind, refId: o.refId }), 'Доступ добавлен').then(() => groups.reload())
                }}>
                  <option value="">+ дать доступ…</option>
                  {options.map((o, i) => g.rules.some((r) => r.kind === o.kind && r.refId === o.refId) ? null : <option key={i} value={i}>{o.label}</option>)}
                </select>
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
    const r = await act(() => (g ? patch(`groups/${g.id}`, { name, description }) : post('groups', { name, description })), 'Сохранено')
    if (r !== undefined) onSaved()
  }
  return (
    <Modal title={g ? 'Группа' : 'Новая группа'} onClose={onClose}
      footer={<><button className="btn ghost" onClick={onClose}>Отмена</button><button className="btn primary" form="group-form" disabled={!name.trim()}>Сохранить</button></>}>
      <form id="group-form" onSubmit={submit}>
        <Field label="Название"><input className="input" autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="Например, Премиум" /></Field>
        <Field label="Описание"><input className="input" value={description} onChange={(e) => setDescription(e.target.value)} /></Field>
      </form>
    </Modal>
  )
}
