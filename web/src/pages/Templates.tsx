import { FormEvent, useState } from 'react'
import { del, get, Group, post, put, Template } from '../api'
import { clientTypes, GiB, resetLabels } from '../format'
import { Badge, Empty, Field, Icon, Loading, Modal, useAction, useConfirm, useLoad } from '../ui'
import { templateSummary } from './Users'

export function Templates() {
  const templates = useLoad(() => get<Template[]>('templates'))
  const groups = useLoad(() => get<Group[]>('groups'))
  const [edit, setEdit] = useState<Template | 'new' | null>(null)
  const act = useAction()
  const confirm = useConfirm()

  return (
    <>
      <div className="toolbar">
        <div className="text-2 grow small">
          Шаблон — набор настроек для новых пользователей: срок, лимиты, группы. При создании достаточно ввести имя, остальное берётся из шаблона по умолчанию.
        </div>
        <button className="btn primary" onClick={() => setEdit('new')}><Icon name="plus" /> Шаблон</button>
      </div>
      <div className="card">
        {!templates.data ? <Loading error={templates.error} /> : templates.data.length === 0 ? <Empty>Шаблонов нет</Empty> : (
          <div className="table-wrap">
            <table className="table">
              <thead><tr><th>Название</th><th>Что даёт</th><th></th></tr></thead>
              <tbody>
                {templates.data.map((t) => (
                  <tr key={t.id} className="click" onClick={() => setEdit(t)}>
                    <td className="nowrap"><b>{t.name}</b> {t.isDefault && <Badge color="green">по умолчанию</Badge>}</td>
                    <td className="text-2 small">{templateSummary(t, groups.data || [])}</td>
                    <td style={{ width: 40 }}>
                      <button className="btn ghost icon sm" title="Удалить" onClick={async (e) => {
                        e.stopPropagation()
                        if (await confirm(<>Удалить шаблон <b>{t.name}</b>? Созданные по нему пользователи не изменятся.</>, 'Удалить'))
                          act(() => del(`templates/${t.id}`), 'Шаблон удалён').then((r) => { if (r !== undefined) templates.reload() })
                      }}><Icon name="trash" /></button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
      {edit && <TemplateForm t={edit === 'new' ? null : edit} groups={groups.data || []} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); templates.reload() }} />}
    </>
  )
}

function TemplateForm({ t, groups, onClose, onSaved }: { t: Template | null; groups: Group[]; onClose: () => void; onSaved: () => void }) {
  const [name, setName] = useState(t?.name || '')
  const [months, setMonths] = useState(String(t?.expireMonths ?? 1))
  const [days, setDays] = useState(String(t?.expireDays ?? 0))
  const [limitGB, setLimitGB] = useState(t?.trafficLimit ? String(Math.round((t.trafficLimit / GiB) * 100) / 100) : '')
  const [reset, setReset] = useState(t?.resetStrategy || 'month')
  const [hwid, setHwid] = useState(t?.hwidLimit == null ? '' : String(t.hwidLimit))
  const [clientType, setClientType] = useState(t?.clientType || 'auto')
  const [groupIds, setGroupIds] = useState<number[]>(t?.groupIds || groups.slice(0, 1).map((g) => g.id))
  const [isDefault, setIsDefault] = useState(t?.isDefault || false)
  const [note, setNote] = useState(t?.note || '')
  const act = useAction()

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const body = {
      name, isDefault, expireMonths: Number(months) || 0, expireDays: Number(days) || 0,
      trafficLimit: limitGB ? Math.round(Number(limitGB) * GiB) : null, resetStrategy: reset,
      hwidLimit: hwid === '' ? null : Number(hwid), clientType, groupIds, note,
    }
    const r = await act(() => (t ? put(`templates/${t.id}`, body) : post('templates', body)), 'Шаблон сохранён')
    if (r !== undefined) onSaved()
  }

  return (
    <Modal title={t ? `Шаблон «${t.name}»` : 'Новый шаблон'} onClose={onClose}
      footer={<><button className="btn ghost" onClick={onClose}>Отмена</button><button className="btn primary" form="tpl-form" disabled={!name.trim()}>Сохранить</button></>}>
      <form id="tpl-form" onSubmit={submit}>
        <Field label="Название"><input className="input" autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="Например, Месяц" /></Field>
        <div className="grid2">
          <Field label="Срок, месяцев"><input className="input" inputMode="numeric" value={months} onChange={(e) => setMonths(e.target.value.replace(/\D/g, ''))} /></Field>
          <Field label="и дней" help="0 и 0 — бессрочно"><input className="input" inputMode="numeric" value={days} onChange={(e) => setDays(e.target.value.replace(/\D/g, ''))} /></Field>
          <Field label="Лимит трафика, ГБ" help="Пусто — без лимита"><input className="input" inputMode="decimal" value={limitGB} onChange={(e) => setLimitGB(e.target.value.replace(',', '.'))} placeholder="∞" /></Field>
          <Field label="Сброс трафика">
            <select className="input" value={reset} onChange={(e) => setReset(e.target.value)}>
              {Object.entries(resetLabels).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
            </select>
          </Field>
          <Field label="Лимит устройств" help="Пусто — общий из настроек, 0 — без лимита"><input className="input" inputMode="numeric" value={hwid} onChange={(e) => setHwid(e.target.value.replace(/\D/g, ''))} /></Field>
          <Field label="Формат подписки">
            <select className="input" value={clientType} onChange={(e) => setClientType(e.target.value)}>
              {Object.entries(clientTypes).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
            </select>
          </Field>
        </div>
        <Field label="Группы">
          <div className="row">
            {groups.map((g) => (
              <label key={g.id} className="check">
                <input type="checkbox" checked={groupIds.includes(g.id)} onChange={(e) => setGroupIds(e.target.checked ? [...groupIds, g.id] : groupIds.filter((x) => x !== g.id))} />
                {g.name}
              </label>
            ))}
          </div>
        </Field>
        <Field label="Заметка для новых пользователей"><input className="input" value={note} onChange={(e) => setNote(e.target.value)} /></Field>
        <label className="check"><input type="checkbox" checked={isDefault} disabled={t?.isDefault} onChange={(e) => setIsDefault(e.target.checked)} /> Шаблон по умолчанию</label>
      </form>
    </Modal>
  )
}
