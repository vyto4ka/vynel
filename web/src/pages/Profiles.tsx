import { FormEvent, useState } from 'react'
import { del, get, Group, patch, post, Profile, ProfileTemplate, Variable } from '../api'
import { Empty, Field, Icon, Loading, Modal, useAction, useConfirm, useLoad } from '../ui'

// Переменные профиля, которые человек может задать руками (остальные генерируются или выводятся).
const editable = (v: Variable) => v.scope === 'profile' && (v.source === 'input' || v.source === 'const' || v.source === 'select')

export function Profiles() {
  const profiles = useLoad(() => get<Profile[]>('profiles'))
  const tpls = useLoad(() => get<ProfileTemplate[]>('profile-templates'))
  const [creating, setCreating] = useState(false)
  const [edit, setEdit] = useState<Profile | null>(null)
  const act = useAction()
  const confirm = useConfirm()

  return (
    <>
      <div className="toolbar">
        <div className="text-2 grow small">
          Профиль — общие настройки VLESS-инбаунда по шаблону (Reality self-steal или XHTTP через VK CDN). На каждой ноде из профиля получается свой инбаунд со своими ключами.
        </div>
        <button className="btn primary" onClick={() => setCreating(true)}><Icon name="plus" /> Профиль</button>
      </div>
      {!profiles.data || !tpls.data ? <Loading error={profiles.error || tpls.error} /> : profiles.data.length === 0 ? <div className="card"><Empty>Профилей нет</Empty></div> : (
        <div className="cards cols">
          {profiles.data.map((p) => {
            const t = tpls.data!.find((x) => x.id === p.templateId)
            return (
              <div key={p.id} className="card">
                <div className="card-head">
                  <div>
                    <h3>{p.name}</h3>
                    <div className="card-sub">{p.templateTitle || p.templateId}</div>
                  </div>
                  <div className="row" style={{ gap: 4 }}>
                    <button className="btn ghost icon sm" title="Изменить" onClick={() => setEdit(p)}><Icon name="edit" /></button>
                    <button className="btn ghost icon sm" title="Удалить" onClick={async () => {
                      if (await confirm(<>Удалить профиль <b>{p.name}</b>? {p.inbounds.length > 0 && `Он используется на нодах: ${p.inbounds.join(', ')}.`}</>, 'Удалить'))
                        act(() => del(`profiles/${p.id}`), 'Профиль удалён').then((r) => { if (r !== undefined) profiles.reload() })
                    }}><Icon name="trash" /></button>
                  </div>
                </div>
                <div className="card-pad">
                  <div className="kv small" style={{ gridTemplateColumns: '150px 1fr' }}>
                    {(t?.variables || []).filter((v) => v.scope === 'profile').map((v) => (
                      <Frag key={v.name} k={v.name} v={p.values[v.name]} def={v.default} />
                    ))}
                    <div className="k">Тег</div><div className="mono">{p.tagPattern}</div>
                  </div>
                  <div className="row small" style={{ marginTop: 12, gap: 6 }}>
                    <span className="muted">На нодах:</span>
                    {p.inbounds.length === 0 ? <span className="muted">нигде — добавьте в разделе «Ноды»</span> : p.inbounds.map((tag) => <span key={tag} className="chip static mono">{tag}</span>)}
                  </div>
                  {Object.keys(p.override).length > 0 && <div className="small muted" style={{ marginTop: 8 }}>Есть ручные правки JSON поверх шаблона</div>}
                </div>
              </div>
            )
          })}
        </div>
      )}
      {creating && tpls.data && <ProfileForm tpls={tpls.data} onClose={() => setCreating(false)} onSaved={() => { setCreating(false); profiles.reload() }} />}
      {edit && tpls.data && <ProfileForm tpls={tpls.data} p={edit} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); profiles.reload() }} />}
    </>
  )
}

function Frag({ k, v, def }: { k: string; v: any; def?: any }) {
  const show = (x: any) => (typeof x === 'object' ? JSON.stringify(x) : String(x))
  return (
    <>
      <div className="k">{k}</div>
      <div className="mono ellipsis" title={v == null ? '' : String(v)}>
        {v == null || v === '' ? (def != null ? <span className="muted">{show(def)} (по умолчанию)</span> : '—') : show(v)}
      </div>
    </>
  )
}

function ProfileForm({ tpls, p, onClose, onSaved }: { tpls: ProfileTemplate[]; p?: Profile; onClose: () => void; onSaved: () => void }) {
  const groups = useLoad(() => get<Group[]>('groups'))
  const [name, setName] = useState(p?.name || '')
  const [templateId, setTemplateId] = useState(p?.templateId || tpls[0]?.id || '')
  const [values, setValues] = useState<Record<string, string>>(() =>
    Object.fromEntries(Object.entries(p?.values || {}).map(([k, v]) => [k, v == null ? '' : String(v)])))
  const [override, setOverride] = useState(p && Object.keys(p.override).length ? JSON.stringify(p.override, null, 2) : '')
  const [groupIds, setGroupIds] = useState<number[] | null>(null)
  const act = useAction()
  const t = tpls.find((x) => x.id === templateId)
  const vars = (t?.variables || []).filter(editable)
  const gids = groupIds ?? (groups.data || []).filter((g) => g.name === 'Основная').map((g) => g.id)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    let over: any = undefined
    if (override.trim()) {
      try {
        over = JSON.parse(override)
      } catch {
        act(() => Promise.reject(new Error('Правка JSON: это не JSON-объект')))
        return
      }
    } else if (p) over = {}
    const vals: Record<string, string> = {}
    for (const v of vars) if (values[v.name] !== undefined) vals[v.name] = values[v.name]
    const r = await act(() => (p
      ? patch(`profiles/${p.id}`, { values: vals, override: over })
      : post('profiles', { name, templateId, values: vals, override: over, groupIds: gids })), p ? 'Профиль сохранён — ноды получат изменения' : 'Профиль создан')
    if (r !== undefined) onSaved()
  }

  return (
    <Modal title={p ? `Профиль «${p.name}»` : 'Новый профиль'} wide onClose={onClose}
      footer={<><button className="btn ghost" onClick={onClose}>Отмена</button><button className="btn primary" form="profile-form" disabled={!name.trim()}>Сохранить</button></>}>
      <form id="profile-form" onSubmit={submit}>
        <div className="grid2">
          <Field label="Название"><input className="input" autoFocus={!p} disabled={!!p} value={name} onChange={(e) => setName(e.target.value)} placeholder="Reality" /></Field>
          <Field label="Шаблон" help={t?.summary}>
            <select className="input" disabled={!!p} value={templateId} onChange={(e) => { setTemplateId(e.target.value); setValues({}) }}>
              {tpls.map((x) => <option key={x.id} value={x.id}>{x.title}</option>)}
            </select>
          </Field>
        </div>
        {vars.length > 0 && <div className="label" style={{ margin: '4px 0 10px' }}>Параметры (пусто — значение по умолчанию)</div>}
        <div className="grid2">
          {vars.map((v) => (
            <Field key={v.name} label={<span className="mono">{v.name}</span>} help={v.description}>
              {v.options && v.options.length > 0 ? (
                <select className="input" value={values[v.name] ?? ''} onChange={(e) => setValues({ ...values, [v.name]: e.target.value })}>
                  <option value="">по умолчанию{v.default != null ? ` (${v.default})` : ''}</option>
                  {v.options.map((o) => <option key={o} value={o}>{o}</option>)}
                </select>
              ) : (
                <input className="input" value={values[v.name] ?? ''} onChange={(e) => setValues({ ...values, [v.name]: e.target.value })}
                  placeholder={v.default != null ? String(v.default) : ''} />
              )}
            </Field>
          ))}
        </div>
        {!p && (
          <Field label="Дать доступ группам" help="Пользователи этих групп получат профиль на всех нодах, где он стоит">
            <div className="row">
              {(groups.data || []).map((g) => (
                <label key={g.id} className="check">
                  <input type="checkbox" checked={gids.includes(g.id)} onChange={(e) => setGroupIds(e.target.checked ? [...gids, g.id] : gids.filter((x) => x !== g.id))} />
                  {g.name}
                </label>
              ))}
            </div>
          </Field>
        )}
        <details>
          <summary className="label" style={{ cursor: 'pointer', marginBottom: 8 }}>Ручная правка JSON поверх шаблона (для опытных)</summary>
          <Field label="JSON merge patch" help='Например {"sniffing":{"enabled":false}}. null удаляет поле. Пусто — без правок.'>
            <textarea className="input mono" rows={6} value={override} onChange={(e) => setOverride(e.target.value)} placeholder="{}" />
          </Field>
        </details>
      </form>
    </Modal>
  )
}
