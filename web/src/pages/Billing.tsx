import { FormEvent, useState } from 'react'
import { Billing as BillingT, BillingNode, del, get, patch, post, Provider, put } from '../api'
import { bytes, date, fromDateInput, GiB, toDateInput } from '../format'
import { Bar, Empty, Field, Icon, Info, Loading, Menu, Modal, PageInfo, useAction, useConfirm, useLoad } from '../ui'
import { t, Trans } from '../i18n'

// Биллинг — где арендованы серверы, сколько стоят, до когда оплачены и сколько трафика им можно.
// За 5 дней до оплаты бот присылает напоминание; ноду, которая вот-вот выйдет за лимит трафика
// провайдера, панель выключает до нового периода.

const countLabels: Record<BillingNode['trafficCount'], string> = { both: t('billing.incoming_outgoing'), out: t('billing.outgoing_only'), in: t('billing.incoming_only') }

const money = (v: number) => (Number.isInteger(v) ? String(v) : v.toFixed(2)).replace(/\B(?=(\d{3})+(?!\d))/g, ' ')

// Сколько дней до даты (отрицательное — уже прошла).
const daysTo = (ts: number) => Math.ceil((ts * 1000 - Date.now()) / 86400000)

export function ProviderIcon({ p, size = 18 }: { p: Provider | null | undefined; size?: number }) {
  if (!p) return null
  return p.icon
    ? <img className="provider-icon" src={p.icon} width={size} height={size} alt="" />
    : <span className="provider-icon letter" style={{ width: size, height: size, fontSize: size * 0.6 }}>{p.name.slice(0, 1).toUpperCase()}</span>
}

export function Billing() {
  const { data, error, reload } = useLoad(() => get<BillingT>('billing'), [], 60000)
  const [editProvider, setEditProvider] = useState<Provider | 'new' | null>(null)
  const [editNode, setEditNode] = useState<BillingNode | null>(null)
  const act = useAction()
  const confirm = useConfirm()
  if (!data) return <Loading error={error} />
  const byId = new Map(data.providers.map((p) => [p.id, p]))

  // Итоги: в месяц по валютам, ближайшая оплата.
  const monthly = new Map<string, number>()
  for (const n of data.nodes) if (n.monthlyCost > 0) monthly.set(n.currency, (monthly.get(n.currency) || 0) + n.monthlyCost)
  const due = data.nodes.filter((n) => n.paidUntil).sort((a, b) => a.paidUntil! - b.paidUntil!)[0]
  const limited = data.nodes.filter((n) => n.trafficLimit > 0)
  const fullest = [...limited].sort((a, b) => b.used / b.trafficLimit - a.used / a.trafficLimit)[0]

  return (
    <>
      <div className="toolbar">
        <PageInfo>
          <p>{t('billing.providers_are_where_the_servers')}</p>
          <p><Trans k="billing.each_node_has_a_provider" v={{ remindDays: data.remindDays, v: bytes(data.margin) }} /></p>
        </PageInfo>
        <button className="btn primary" onClick={() => setEditProvider('new')}><Icon name="plus" />{' '}{t('common.provider')}</button>
      </div>

      <div className="stats">
        <div className="card stat kpi">
          <div className="k">{t('billing.per_month')}</div>
          <div className="v">{monthly.size === 0 ? '—' : [...monthly].map(([c, v]) => `${money(Math.round(v * 100) / 100)} ${c}`).join(' + ')}</div>
          <div className="s"><Trans k="billing.of_nodes_have_a_price" v={{ length: data.nodes.filter((n) => n.cost > 0).length, length2: data.nodes.length }} /></div>
        </div>
        <div className="card stat kpi pink">
          <div className="k">{t('billing.next_payment')}</div>
          <div className="v">{due ? date(due.paidUntil) : '—'}</div>
          <div className="s">{due ? <>{due.flag} {due.name} · <PaidLeft ts={due.paidUntil!} /></> : t('billing.no_payment_dates_set')}</div>
        </div>
        <div className="card stat kpi">
          <div className="k">{t('billing.provider_traffic')}</div>
          <div className="v">{fullest ? `${Math.min(100, Math.round((100 * fullest.used) / fullest.trafficLimit))}%` : '—'}</div>
          <div className="s">{fullest ? t('billing.most', { flag: fullest.flag, name: fullest.name }) : t('billing.no_limits_set')}</div>
        </div>
      </div>

      <div className="card section-gap">
        <div className="card-head"><div><h3>{t('billing.providers')}</h3><div className="card-sub">{t('billing.icon_and_name_come_from')}</div></div></div>
        {data.providers.length === 0 ? (
          <Empty icon="🏢">{t('billing.no_providers_yet_press_provider')}</Empty>
        ) : (
          <div className="provider-grid card-pad">
            {data.providers.map((p) => {
              const nodes = data.nodes.filter((n) => n.providerId === p.id)
              return (
                <div key={p.id} className="provider-tile">
                  <div className="row" style={{ gap: 10, flexWrap: 'nowrap' }}>
                    <ProviderIcon p={p} size={28} />
                    <div className="grow" style={{ minWidth: 0 }}>
                      <div className="ellipsis"><b>{p.name}</b></div>
                      <div className="muted small ellipsis">{p.url ? new URL(p.url).hostname.replace(/^www\./, '') : t('billing.no_site')} · {t('common.nodes_count', { n: nodes.length })}</div>
                    </div>
                    <Menu title={t('common.actions')} items={[
                      { label: t('common.edit'), icon: 'edit', onClick: () => setEditProvider(p) },
                      { label: t('billing.refresh_the_icon_and_name'), icon: 'refresh', disabled: !p.url && !p.loginUrl,
                        onClick: () => act(() => put(`providers/${p.id}`, { name: '', url: p.url, loginUrl: p.loginUrl, refresh: true }), t('common.updated')).then(reload) },
                      'divider',
                      { label: t('common.delete'), icon: 'trash', danger: true, onClick: async () => {
                        if (await confirm(<><Trans k="billing.delete_the_provider_nodes_keep" v={{ name: p.name }} /></>, t('common.delete')))
                          act(() => del(`providers/${p.id}`), t('billing.provider_deleted')).then(reload)
                      } },
                    ]} />
                  </div>
                  <div className="row" style={{ gap: 6, marginTop: 10 }}>
                    {p.url && <a className="btn sm" href={p.url} target="_blank" rel="noreferrer"><Icon name="external" size={16} />{' '}{t('billing.site')}</a>}
                    {p.loginUrl && <a className="btn sm primary" href={p.loginUrl} target="_blank" rel="noreferrer"><Icon name="key" size={16} />{' '}{t('common.sign_in')}</a>}
                  </div>
                </div>
              )
            })}
          </div>
        )}
      </div>

      <div className="card section-gap">
        <div className="card-head"><div><h3>{t('common.nodes')}</h3><div className="card-sub">{t('billing.click_a_row_to_set')}</div></div></div>
        {data.nodes.length === 0 ? <Empty>{t('common.no_nodes_yet')}</Empty> : (
          <div className="table-wrap">
            <table className="table">
              <thead><tr><th>{t('common.node')}</th><th className="hide-sm">{t('common.provider')}</th><th>{t('billing.price')}</th><th>{t('billing.paid_until')}</th><th className="hide-sm">{t('billing.provider_traffic')}</th></tr></thead>
              <tbody>
                {data.nodes.map((n) => {
                  const p = n.providerId ? byId.get(n.providerId) : null
                  return (
                    <tr key={n.id} className="click" onClick={() => setEditNode(n)}>
                      <td className="nowrap"><span style={{ fontSize: 16 }}>{n.flag}</span> <b>{n.name}</b> <span className="muted small">{n.code}</span>
                        {n.offByLimit && <div className="small pink-text">{t('billing.switched_off_by_the_limit')}</div>}</td>
                      <td className="nowrap hide-sm">{p ? <span className="row" style={{ gap: 6, flexWrap: 'nowrap' }}><ProviderIcon p={p} />{p.name}</span> : <span className="muted">—</span>}</td>
                      <td className="nowrap">{n.cost > 0 ? <>{money(n.cost)} {n.currency}<span className="muted small"> / {n.costPeriod === 'year' ? t('billing.year') : t('billing.mo')}</span></> : <span className="muted">—</span>}</td>
                      <td className="nowrap">{n.paidUntil ? <><div>{date(n.paidUntil)}</div><div className="small"><PaidLeft ts={n.paidUntil} /></div></> : <span className="muted">—</span>}</td>
                      <td className="hide-sm" style={{ minWidth: 190 }}>
                        {n.trafficLimit > 0 ? <>
                          <div className="small text-2"><Trans k="billing.of" v={{ v: bytes(n.used), v2: bytes(n.trafficLimit) }} /></div>
                          <Bar value={n.used} max={n.trafficLimit} />
                          <div className="muted small"><Trans k="billing.reset" v={{ v: date(n.periodTo) }} /></div>
                        </> : <span className="muted small"><Trans k="billing.since_no_limit" v={{ v: bytes(n.used), v2: date(n.periodFrom) }} /></span>}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {editProvider && <ProviderForm p={editProvider === 'new' ? null : editProvider} onClose={() => setEditProvider(null)} onSaved={() => { setEditProvider(null); reload() }} />}
      {editNode && <NodeBillingForm n={editNode} providers={data.providers} margin={data.margin} remindDays={data.remindDays}
        onClose={() => setEditNode(null)} onSaved={() => { setEditNode(null); reload() }} onNewProvider={() => { setEditNode(null); setEditProvider('new') }} />}
    </>
  )
}

function PaidLeft({ ts }: { ts: number }) {
  const d = daysTo(ts)
  if (d < 0) return <span className="pink-text"><Trans k="billing.overdue_by_d" v={{ v: -d }} /></span>
  if (d === 0) return <span className="pink-text">{t('billing.today')}</span>
  return <span className={d <= 5 ? 'pink-text' : 'muted'}><Trans k="billing.d_left" v={{ d: d }} /></span>
}

function ProviderForm({ p, onClose, onSaved }: { p: Provider | null; onClose: () => void; onSaved: () => void }) {
  const [url, setUrl] = useState(p?.url || '')
  const [loginUrl, setLoginUrl] = useState(p?.loginUrl || '')
  const [name, setName] = useState(p?.name || '')
  const [busy, setBusy] = useState(false)
  const act = useAction()
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    const body = { name, url, loginUrl }
    const r = await act(() => (p ? put(`providers/${p.id}`, body) : post('providers', body)), p ? t('common.saved') : t('billing.provider_added'))
    setBusy(false)
    if (r !== undefined) onSaved()
  }
  return (
    <Modal title={p ? p.name : t('billing.new_provider')} onClose={onClose}
      footer={<><button className="btn ghost" onClick={onClose}>{t('common.cancel')}</button>
        <button className="btn primary" form="provider-form" disabled={busy || (!url.trim() && !loginUrl.trim() && !name.trim())}>{busy ? t('billing.loading_the_site') : t('common.save')}</button></>}>
      <form id="provider-form" onSubmit={submit}>
        <Field label={t('billing.site')} info={t('billing.a_link_to_the_provider')}>
          <input className="input" autoFocus value={url} onChange={(e) => setUrl(e.target.value)} inputMode="url" />
        </Field>
        <Field label={t('billing.account_login')} help={t('common.optional')}>
          <input className="input" value={loginUrl} onChange={(e) => setLoginUrl(e.target.value)} inputMode="url" />
        </Field>
        <Field label={t('common.name')} help={t('billing.empty_taken_from_the_site')}>
          <input className="input" value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
      </form>
    </Modal>
  )
}

function NodeBillingForm({ n, providers, margin, remindDays, onClose, onSaved, onNewProvider }: {
  n: BillingNode; providers: Provider[]; margin: number; remindDays: number; onClose: () => void; onSaved: () => void; onNewProvider: () => void
}) {
  const [providerId, setProviderId] = useState(n.providerId || 0)
  const [cost, setCost] = useState(n.cost ? String(n.cost) : '')
  const [currency, setCurrency] = useState(n.currency)
  const [period, setPeriod] = useState(n.costPeriod)
  const [paid, setPaid] = useState(toDateInput(n.paidUntil))
  const [limit, setLimit] = useState(n.trafficLimit ? String(Math.round((n.trafficLimit / GiB) * 100) / 100) : '')
  const [resetDay, setResetDay] = useState(String(n.resetDay || 1))
  const [count, setCount] = useState(n.trafficCount)
  const act = useAction()
  // «+1 месяц» / «+1 год» от текущей даты оплаты (или от сегодня).
  const extend = (months: number) => {
    const base = paid ? new Date(paid + 'T12:00:00') : new Date()
    base.setMonth(base.getMonth() + months)
    setPaid(toDateInput(Math.floor(base.getTime() / 1000)))
  }
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const body = {
      providerId, cost: Number(cost.replace(',', '.')) || 0, currency: currency.trim() || '₽', costPeriod: period,
      paidUntil: fromDateInput(paid) || 0, trafficLimit: Math.round((Number(limit.replace(',', '.')) || 0) * GiB),
      resetDay: Math.min(31, Math.max(1, Number(resetDay) || 1)), trafficCount: count,
    }
    const r = await act(() => patch(`nodes/${n.id}/billing`, body), t('common.saved'))
    if (r !== undefined) onSaved()
  }
  return (
    <Modal title={<>{n.flag} {n.name} <span className="muted small">{n.code}</span></>} onClose={onClose}
      footer={<><button className="btn ghost" onClick={onClose}>{t('common.cancel')}</button><button className="btn primary" form="node-billing">{t('common.save')}</button></>}>
      <form id="node-billing" onSubmit={submit}>
        <Field label={t('common.provider')}>
          <div className="row" style={{ flexWrap: 'nowrap' }}>
            <select className="input" value={providerId} onChange={(e) => setProviderId(Number(e.target.value))}>
              <option value={0}>{t('common.not_set')}</option>
              {providers.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
            </select>
            <button type="button" className="btn ghost" onClick={onNewProvider}><Icon name="plus" /> <span className="hide-sm">{t('billing.new')}</span></button>
          </div>
        </Field>
        <div className="grid3">
          <Field label={t('billing.price')}><input className="input" inputMode="decimal" value={cost} onChange={(e) => setCost(e.target.value)} placeholder="0" /></Field>
          <Field label={t('billing.currency')}><input className="input" value={currency} maxLength={8} onChange={(e) => setCurrency(e.target.value)} list="currencies" /></Field>
          <Field label={t('billing.per')}>
            <select className="input" value={period} onChange={(e) => setPeriod(e.target.value as BillingNode['costPeriod'])}>
              <option value="month">{t('billing.month')}</option><option value="year">{t('billing.year')}</option>
            </select>
          </Field>
        </div>
        <datalist id="currencies"><option value="₽" /><option value="$" /><option value="€" /></datalist>
        <Field label={t('billing.paid_until')} help={t('billing.the_bot_reminds_you_days', { remindDays })}>
          <div className="row" style={{ flexWrap: 'nowrap' }}>
            <input className="input" type="date" value={paid} onChange={(e) => setPaid(e.target.value)} />
            <button type="button" className="btn ghost" onClick={() => extend(period === 'year' ? 12 : 1)}>+1 {period === 'year' ? t('billing.year') : t('billing.mo')}</button>
          </div>
        </Field>
        <div className="label" style={{ margin: '6px 0 8px' }}>{t('billing.provider_traffic')}<Info><Trans k="billing.counted_on_the_server_s" v={{ v: bytes(margin) }} /></Info>
        </div>
        <div className="grid3">
          <Field label={t('billing.limit_gb')} help={t('common.empty_no_limit')}><input className="input" inputMode="decimal" value={limit} onChange={(e) => setLimit(e.target.value)} placeholder="∞" /></Field>
          <Field label={t('billing.reset_day')} help={t('billing.every_month')}><input className="input" inputMode="numeric" value={resetDay} onChange={(e) => setResetDay(e.target.value.replace(/\D/g, ''))} /></Field>
          <Field label={t('billing.count')}>
            <select className="input" value={count} onChange={(e) => setCount(e.target.value as BillingNode['trafficCount'])}>
              {Object.entries(countLabels).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
            </select>
          </Field>
        </div>
        <div className="small text-2"><Trans k="billing.now_since" v={{ v: bytes(n.used), v2: date(n.periodFrom) }} />{n.trafficLimit > 0 && <>{' '}<Trans k="billing.of_2" v={{ v: bytes(n.trafficLimit) }} /></>}
          {n.offByLimit && <span className="pink-text">{' '}{t('billing.switched_off_by_the_limit_2')}</span>}
          {n.overridden && <span>{' '}{t('billing.switched_on_manually_until')}</span>}
        </div>
      </form>
    </Modal>
  )
}
