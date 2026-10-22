import { FormEvent, useState } from 'react'
import { Cascade, del, ExternalSub, get, GrantRow, Group, Node, patch, post, UserGrant } from './api'
import { ago, date, fromDateInput } from './format'
import { Badge, Empty, Field, Icon, Loading, Modal, Switch, useAction, useConfirm, useLoad } from './ui'

// ================================================================ каскады

export function CascadeList() {
  const cascades = useLoad(() => get<Cascade[]>('cascades'))
  const nodes = useLoad(() => get<Node[]>('nodes'))
  const [edit, setEdit] = useState<Cascade | 'new' | null>(null)
  const act = useAction()
  const confirm = useConfirm()
  return (
    <>
      <div className="card card-pad small text-2" style={{ marginBottom: 14 }}>
        Каскад: приложение подключается к ноде входа, а трафик выходит в интернет через ноду выхода. Для пользователя это отдельный сервер
        в подписке («🇳🇱 Нидерланды → 🇩🇪 Германия»). Новых портов не нужно: на входе у пользователя свой ключ того же инбаунда, по нему
        нода входа отправляет трафик на выход. Группа с доступом «всё» (по умолчанию «Основная») получает каждый каскад сразу; другим —
        в «Группах» или в карточке пользователя (там можно поставить свой срок).
      </div>
      <div className="toolbar">
        <div className="grow" />
        <button className="btn primary" onClick={() => setEdit('new')}><Icon name="plus" /> Каскад</button>
      </div>
      {!cascades.data ? <Loading error={cascades.error} /> : cascades.data.length === 0 ? <div className="card"><Empty>Каскадов нет</Empty></div> : (
        <div className="card">
          <div className="table-wrap">
            <table className="table">
              <thead><tr><th /><th>Каскад</th><th className="hide-sm">Вход</th><th className="hide-sm">Выход</th><th /></tr></thead>
              <tbody>
                {cascades.data.map((c) => (
                  <tr key={c.id}>
                    <td style={{ width: 50 }}>
                      <Switch checked={c.enabled} onChange={(v) => act(() => patch(`cascades/${c.id}`, { ...c, enabled: v }), v ? 'Каскад включён' : 'Каскад выключен').then(() => cascades.reload())} />
                    </td>
                    <td>
                      <div>{c.name}</div><div className="muted small">{c.title}</div>
                      {c.problem && <div className={'small ' + (c.enabled ? 'pink-text' : 'muted')}>⚠ {c.problem}</div>}
                    </td>
                    <td className="small hide-sm">{c.entry}</td>
                    <td className="small hide-sm">{c.exit}</td>
                    <td className="nowrap" style={{ width: 90 }}>
                      <button className="btn ghost icon sm" title="Изменить" onClick={() => setEdit(c)}><Icon name="edit" /></button>
                      <button className="btn ghost icon sm" title="Удалить" onClick={async () => {
                        if (await confirm(<>Удалить каскад <b>{c.name}</b>? Он пропадёт из подписок у всех, кому выдан.</>, 'Удалить'))
                          act(() => del(`cascades/${c.id}`), 'Каскад удалён').then(() => cascades.reload())
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
    const r = await act(() => (c ? patch(`cascades/${c.id}`, body) : post('cascades', body)), 'Сохранено — ноды получат изменения за несколько секунд')
    if (r !== undefined) onSaved()
  }
  return (
    <Modal title={c ? 'Каскад' : 'Новый каскад'} onClose={onClose}
      footer={<><button className="btn ghost" onClick={onClose}>Отмена</button><button className="btn primary" form="cascade-form" disabled={!(name.trim() || auto) || !entry || !exit}>Сохранить</button></>}>
      <form id="cascade-form" onSubmit={submit}>
        <Field label="Вход (нода 1)" help="Куда подключается приложение: его адрес и ключи Reality уйдут в подписку">
          <select className="input" value={entry} onChange={(e) => setEntry(Number(e.target.value))}>
            <option value={0}>— выберите инбаунд —</option>
            {inbounds.map((i) => <option key={i.id} value={i.id}>{i.label}</option>)}
          </select>
        </Field>
        <Field label="Выход (нода 2)" help="Через какой инбаунд другой ноды трафик выходит в интернет">
          <select className="input" value={exit} onChange={(e) => setExit(Number(e.target.value))}>
            <option value={0}>— выберите инбаунд —</option>
            {inbounds.filter((i) => i.nodeId !== entryNode).map((i) => <option key={i.id} value={i.id}>{i.label}</option>)}
          </select>
        </Field>
        <Field label="Название в панели" help="Пусто — из нод входа и выхода">
          <input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder={auto || 'Нидерланды → Германия'} />
        </Field>
        <Field label="Название сервера в приложениях" help="Пусто — «флаг вход → флаг выход»">
          <input className="input" value={remark} onChange={(e) => setRemark(e.target.value)} placeholder="🇳🇱 Нидерланды → 🇩🇪 Германия" />
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
      <div className="card card-pad small text-2">
        Чужая подписка по ссылке — например, подписка друга или другой панели. Её серверы добавляются в подписку тем, кому она выдана
        (в «Группах» или в карточке пользователя — со своим сроком). Панель скачивает её сама раз в 30 минут; если источник недоступен,
        остаётся последняя удачная копия. Ссылка на подписку этой же панели читается напрямую. Один и тот же сервер попадает в подписку один раз.
      </div>
      <div className="card">
        <div className="card-head">
          <h3>Внешние подписки</h3>
          <button className="btn sm" onClick={() => setEdit('new')}><Icon name="plus" /> Подписка</button>
        </div>
        {!subs.data ? <Loading error={subs.error} /> : subs.data.length === 0 ? <Empty>Пока нет</Empty> : (
          <div className="table-wrap">
            <table className="table">
              <tbody>
                {subs.data.map((x) => (
                  <tr key={x.id}>
                    <td style={{ width: 50 }}>
                      <Switch checked={x.enabled} onChange={(v) => act(() => patch(`externals/${x.id}`, { ...x, enabled: v }), v ? 'Включена' : 'Выключена').then(() => subs.reload())} />
                    </td>
                    <td>
                      <div>{x.name} <Badge color={x.error ? 'pink' : 'green'}>{x.servers.length} серв.</Badge></div>
                      <div className="muted small ellipsis mono" style={{ maxWidth: 420 }}>{x.url}</div>
                      {x.error && <div className="small pink-text">{x.error}</div>}
                      {x.servers.length > 0 && <div className="small text-2 ellipsis" style={{ maxWidth: 520 }}>{x.servers.join(' · ')}</div>}
                    </td>
                    <td className="small muted nowrap">{x.fetchedAt ? `обновлена ${ago(x.fetchedAt)}` : 'ещё не скачана'}</td>
                    <td className="nowrap" style={{ width: 120 }}>
                      <button className="btn ghost icon sm" title="Скачать сейчас" onClick={() => act(() => post(`externals/${x.id}/refresh`), 'Обновлено').then(() => subs.reload())}>↻</button>
                      <button className="btn ghost icon sm" title="Изменить" onClick={() => setEdit(x)}><Icon name="edit" /></button>
                      <button className="btn ghost icon sm" title="Удалить" onClick={async () => {
                        if (await confirm(<>Удалить подписку <b>{x.name}</b>? Её серверы пропадут у всех, кому она выдана.</>, 'Удалить'))
                          act(() => del(`externals/${x.id}`), 'Удалено').then(() => subs.reload())
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
    const r = await act(() => (x ? patch(`externals/${x.id}`, body) : post('externals', body)), 'Сохранено')
    if (r !== undefined) onSaved()
  }
  return (
    <Modal title={x ? 'Внешняя подписка' : 'Новая внешняя подписка'} onClose={onClose}
      footer={<><button className="btn ghost" onClick={onClose}>Отмена</button><button className="btn primary" form="external-form" disabled={!name.trim() || !url.trim()}>Сохранить</button></>}>
      <form id="external-form" onSubmit={submit}>
        <Field label="Название"><input className="input" autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="Подписка Пети" /></Field>
        <Field label="Ссылка подписки" help="https://… — как её добавляют в приложение. Понимаются ссылки vless://, hysteria2:// и другие (в base64 или текстом)">
          <input className="input mono" value={url} onChange={(e) => setURL(e.target.value)} placeholder="https://sub.example.com/s/…" />
        </Field>
      </form>
    </Modal>
  )
}

// ================================================================ кому выдано

function useExtras() {
  const cascades = useLoad(() => get<Cascade[]>('cascades'))
  const externals = useLoad(() => get<ExternalSub[]>('externals'))
  const name = (kind: string, id: number) =>
    kind === 'cascade' ? cascades.data?.find((c) => c.id === id)?.title || `каскад #${id}` : externals.data?.find((x) => x.id === id)?.name || `подписка #${id}`
  const options = [
    ...(cascades.data || []).map((c) => ({ kind: 'cascade' as const, refId: c.id, label: `Каскад: ${c.title}` })),
    ...(externals.data || []).map((x) => ({ kind: 'external' as const, refId: x.id, label: `Подписка: ${x.name}` })),
  ]
  return { name, options }
}

const kindText = { cascade: 'каскад', external: 'подписка' }

// GroupExtras — каскады и внешние подписки группы (без срока: срок ставится пользователю).
export function GroupExtras({ group, grants, onChanged }: { group: Group; grants: GrantRow[]; onChanged: () => void }) {
  const { name, options } = useExtras()
  const act = useAction()
  const mine = grants.filter((g) => g.groupId === group.id)
  if (options.length === 0 && mine.length === 0) return null
  return (
    <>
      <div className="label" style={{ margin: '14px 0 8px' }}>Каскады и подписки</div>
      <div className="row" style={{ gap: 6 }}>
        {mine.map((g) => (
          <span key={g.id} className="chip">
            <span className="muted">{kindText[g.kind]}</span> {name(g.kind, g.refId)}
            <button title="Убрать" onClick={() => act(() => del(`grants/${g.id}`), 'Убрано').then(onChanged)}>×</button>
          </span>
        ))}
      </div>
      <select className="input" style={{ marginTop: 10 }} value="" onChange={(e) => {
        const o = options[Number(e.target.value)]
        if (o) act(() => post('grants', { groupId: group.id, kind: o.kind, refId: o.refId }), 'Добавлено').then(onChanged)
      }}>
        <option value="">+ каскад или подписка…</option>
        {options.map((o, i) => mine.some((g) => g.kind === o.kind && g.refId === o.refId) ? null : <option key={i} value={i}>{o.label}</option>)}
      </select>
    </>
  )
}

// UserExtras — каскады и внешние подписки пользователя: свои (со своим сроком) и через группы.
export function UserExtras({ userId, grants, groups, onChanged }: { userId: number; grants: UserGrant[]; groups: Group[]; onChanged: () => void }) {
  const { name, options } = useExtras()
  const [adding, setAdding] = useState<number | ''>('')
  const [until, setUntil] = useState('')
  const act = useAction()
  const add = async () => {
    const o = options[Number(adding)]
    if (!o) return
    const r = await act(() => post('grants', { userId, kind: o.kind, refId: o.refId, expireAt: fromDateInput(until) }), 'Выдано')
    if (r !== undefined) { setAdding(''); setUntil(''); onChanged() }
  }
  return (
    <div className="card">
      <div className="card-head">
        <div><h3>Каскады и подписки</h3><div className="card-sub">свои — со своим сроком; через группу — пока пользователь в ней</div></div>
      </div>
      <div className="card-pad">
        {grants.length === 0 ? <div className="muted small">Нет</div> : grants.map((g) => (
          <div key={g.kind + g.refId} className="row between" style={{ marginBottom: 8 }}>
            <div>
              <span className="muted small">{kindText[g.kind]}</span> {name(g.kind, g.refId)}{' '}
              {g.problem && <div className="pink-text small">⚠ {g.problem}</div>}
              {!g.active ? <Badge color="pink">закончился {date(g.expireAt)}</Badge> : g.expireAt ? <Badge>до {date(g.expireAt)}</Badge> : null}
              {g.groups.length > 0 && <span className="muted small"> · через {g.groups.map((id) => groups.find((x) => x.id === id)?.name || id).join(', ')}</span>}
            </div>
            {g.grantId > 0 && (
              <button className="btn ghost icon sm" title="Забрать свою выдачу" onClick={() => act(() => del(`grants/${g.grantId}`), 'Убрано').then(onChanged)}><Icon name="trash" /></button>
            )}
          </div>
        ))}
        {options.length > 0 && (
          <div className="row" style={{ marginTop: 10 }}>
            <select className="input" style={{ flex: 2, minWidth: 180 }} value={adding} onChange={(e) => setAdding(e.target.value === '' ? '' : Number(e.target.value))}>
              <option value="">+ выдать каскад или подписку…</option>
              {options.map((o, i) => <option key={i} value={i}>{o.label}</option>)}
            </select>
            <input className="input" type="date" style={{ flex: 1, minWidth: 140 }} value={until} onChange={(e) => setUntil(e.target.value)} title="До какого числа (пусто — пока действует подписка)" />
            <button className="btn sm primary" disabled={adding === ''} onClick={add}>Выдать</button>
          </div>
        )}
      </div>
    </div>
  )
}
