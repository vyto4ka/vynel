import { useState } from 'react'
import { get, put, Setting } from '../api'
import { Field, Loading, Switch, useAction, useConfirm, useLoad } from '../ui'

export function Settings() {
  const { data, error, reload } = useLoad(() => get<Setting[]>('settings'))
  if (!data) return <Loading error={error} />
  const sections = [...new Set(data.map((s) => s.section))]
  return (
    <div className="cards" style={{ maxWidth: 820 }}>
      {sections.map((sec) => (
        <div key={sec} className="card">
          <div className="card-head"><h3>{sec}</h3></div>
          <div className="card-pad">
            {data.filter((s) => s.section === sec).map((s) => <SettingRow key={s.key} s={s} onSaved={reload} />)}
          </div>
        </div>
      ))}
    </div>
  )
}

function SettingRow({ s, onSaved }: { s: Setting; onSaved: () => void }) {
  const initial = s.value ?? ''
  const [value, setValue] = useState(initial)
  const act = useAction()
  const confirm = useConfirm()
  const dirty = value !== initial
  const effective = s.set ? initial : s.default ?? ''

  const save = async (v: string) => {
    if (s.key === 'web.path' && !(await confirm('Панель переедет на новый адрес. Сохраните его — старый перестанет работать.', 'Сменить', false))) return
    const r = await act(() => put<{ webUrl: string }>('settings', { key: s.key, value: v }), 'Сохранено')
    if (r === undefined) return
    if (s.key === 'web.path' && r.webUrl) {
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
            <div>{s.title}</div>
            {s.help && <div className="help muted small">{s.help}</div>}
          </div>
          <Switch checked={on} onChange={(v) => save(v ? 'true' : 'false')} />
        </div>
      </div>
    )
  }
  return (
    <Field label={<>{s.title} <span className="muted mono" style={{ fontWeight: 400 }}>{s.key}</span></>} help={s.help}>
      <div className="row" style={{ flexWrap: 'nowrap' }}>
        {s.type === 'select' ? (
          <select className="input" value={value} onChange={(e) => setValue(e.target.value)}>
            <option value="">по умолчанию{s.default ? ` (${s.default})` : ''}</option>
            {s.options?.map((o) => <option key={o} value={o}>{o}</option>)}
          </select>
        ) : (
          <input className="input" value={value} inputMode={s.type === 'int' ? 'numeric' : undefined}
            onChange={(e) => setValue(e.target.value)} placeholder={s.default ? `по умолчанию ${s.default}` : ''}
            onKeyDown={(e) => e.key === 'Enter' && dirty && save(value)} />
        )}
        {dirty && <button className="btn primary" onClick={() => save(value)}>Сохранить</button>}
        {dirty && <button className="btn ghost" onClick={() => setValue(initial)}>Отмена</button>}
      </div>
    </Field>
  )
}
