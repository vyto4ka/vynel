import { FormEvent, useState } from 'react'
import { del, get, Group, post, put, Template, TemplateGrant } from '../api'
import { TemplateExtras } from '../extras'
import { clientTypes, GiB, resetLabels } from '../format'
import { Badge, Empty, Field, Icon, Loading, Modal, PageInfo, useAction, useConfirm, useLoad } from '../ui'
import { templateSummary } from './Users'
import { t as tr, Trans } from '../i18n'

export function Templates() {
  const templates = useLoad(() => get<Template[]>('templates'))
  const groups = useLoad(() => get<Group[]>('groups'))
  const [edit, setEdit] = useState<Template | 'new' | null>(null)
  const act = useAction()
  const confirm = useConfirm()

  return (
    <>
      <div className="toolbar">
        <PageInfo>{tr('templates.a_template_is_a_set')}</PageInfo>
        <button className="btn primary" onClick={() => setEdit('new')}><Icon name="plus" />{' '}{tr('common.template')}</button>
      </div>
      <div className="card">
        {!templates.data ? <Loading error={templates.error} /> : templates.data.length === 0 ? <Empty>{tr('templates.no_templates')}</Empty> : (
          <div className="table-wrap">
            <table className="table">
              <thead><tr><th>{tr('common.name')}</th><th>{tr('templates.what_it_gives')}</th><th></th></tr></thead>
              <tbody>
                {templates.data.map((t) => (
                  <tr key={t.id} className="click" onClick={() => setEdit(t)}>
                    <td className="nowrap"><b>{t.name}</b> {t.isDefault && <Badge color="green">{tr('common.default')}</Badge>}</td>
                    <td className="text-2 small">{templateSummary(t, groups.data || [])}</td>
                    <td style={{ width: 40 }}>
                      <button className="btn ghost icon sm" title={tr('common.delete')} onClick={async (e) => {
                        e.stopPropagation()
                        if (await confirm(<><Trans k="templates.delete_the_template_users_already" v={{ name: t.name }} /></>, tr('common.delete')))
                          act(() => del(`templates/${t.id}`), tr('common.template_deleted')).then((r) => { if (r !== undefined) templates.reload() })
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
  const [grants, setGrants] = useState<TemplateGrant[]>(t?.grants || [])
  const act = useAction()

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const body = {
      name, isDefault, expireMonths: Number(months) || 0, expireDays: Number(days) || 0,
      trafficLimit: limitGB ? Math.round(Number(limitGB) * GiB) : null, resetStrategy: reset,
      hwidLimit: hwid === '' ? null : Number(hwid), clientType, groupIds, note, grants,
    }
    const r = await act(() => (t ? put(`templates/${t.id}`, body) : post('templates', body)), tr('templates.template_saved'))
    if (r !== undefined) onSaved()
  }

  return (
    <Modal title={t ? tr('templates.template', { name: t.name }) : tr('common.new_template')} onClose={onClose}
      footer={<><button className="btn ghost" onClick={onClose}>{tr('common.cancel')}</button><button className="btn primary" form="tpl-form" disabled={!name.trim()}>{tr('common.save')}</button></>}>
      <form id="tpl-form" onSubmit={submit}>
        <Field label={tr('common.name')}><input className="input" autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder={tr('templates.e_g_one_month')} /></Field>
        <div className="grid2 keep2">
          <Field label={tr('templates.term_months')}><input className="input" inputMode="numeric" value={months} onChange={(e) => setMonths(e.target.value.replace(/\D/g, ''))} /></Field>
          <Field label={tr('templates.and_days')} help={tr('templates.n0_and_0_no_expiry')}><input className="input" inputMode="numeric" value={days} onChange={(e) => setDays(e.target.value.replace(/\D/g, ''))} /></Field>
          <Field label={tr('common.traffic_limit_gb')} help={tr('common.empty_no_limit')}><input className="input" inputMode="decimal" value={limitGB} onChange={(e) => setLimitGB(e.target.value.replace(',', '.'))} placeholder="∞" /></Field>
          <Field label={tr('common.traffic_reset')}>
            <select className="input" value={reset} onChange={(e) => setReset(e.target.value)}>
              {Object.entries(resetLabels).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
            </select>
          </Field>
          <Field label={tr('common.device_limit')} help={tr('templates.empty_the_shared_one_from')}><input className="input" inputMode="numeric" value={hwid} onChange={(e) => setHwid(e.target.value.replace(/\D/g, ''))} /></Field>
          <Field label={tr('common.subscription_format')}>
            <select className="input" value={clientType} onChange={(e) => setClientType(e.target.value)}>
              {Object.entries(clientTypes).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
            </select>
          </Field>
        </div>
        <Field label={tr('common.groups')}>
          <div className="row">
            {groups.map((g) => (
              <label key={g.id} className="check">
                <input type="checkbox" checked={groupIds.includes(g.id)} onChange={(e) => setGroupIds(e.target.checked ? [...groupIds, g.id] : groupIds.filter((x) => x !== g.id))} />
                {g.name}
              </label>
            ))}
          </div>
        </Field>
        <Field label={tr('common.cascades_and_subscriptions')} help={tr('templates.no_expiry')} info={tr('templates.every_new_user_gets_them')}>
          <TemplateExtras value={grants} onChange={setGrants} />
        </Field>
        <Field label={tr('templates.note_for_new_users')}><input className="input" value={note} onChange={(e) => setNote(e.target.value)} /></Field>
        <label className="check"><input type="checkbox" checked={isDefault} disabled={t?.isDefault} onChange={(e) => setIsDefault(e.target.checked)} />{' '}{tr('templates.default_template')}</label>
      </form>
    </Modal>
  )
}
