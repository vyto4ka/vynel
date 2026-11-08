import { useState } from 'react'
import { get, post, Provider, SelfHost, UpdateInfo, UpdateNode } from '../api'
import { dateTime, uptime } from '../format'
import { t, Trans } from '../i18n'
import { Badge, Icon, Loading, Modal, useAction, useConfirm, useLoad } from '../ui'
import { UpdateChip, UpdateLog } from '../updates'
import { PanelBillingForm, PanelRentalDetails } from './Billing'

export function PanelHostCard({ host, update, latest, checkError, waiting, onUpdate, onChanged, onChecked }: {
  host: SelfHost; update?: UpdateNode; latest: string; checkError?: string; waiting: boolean;
  onUpdate: () => Promise<void>; onChanged: () => void; onChecked: (info: UpdateInfo) => void
}) {
  const [edit, setEdit] = useState(false)
  const [log, setLog] = useState(false)
  const [checking, setChecking] = useState(false)
  const providers = useLoad(() => get<Provider[]>('providers'), [edit])
  const act = useAction()
  const confirm = useConfirm()
  const busy = checking || waiting || !!update?.updating
  return <>
    <div className="card">
      <div className="card-head">
        <div className="node-head"><span className="flag">🎛</span><div><h3>{t('nodes.panel_this_server')}</h3><div className="card-sub">{host.domains.join(' · ') || t('nodes.no_domain')}</div></div></div>
        <div className="row"><Badge color={host.withNode ? 'green' : ''} dot>{t(host.withNode ? 'nodes.panel_with_node' : 'nodes.panel_without_node')}</Badge>{update && <UpdateChip n={update} latest={latest} onLog={() => setLog(true)} />}</div>
      </div>
      <div className="card-pad">
        <div className="grid2">
          <div><div className="kv small" style={{ gridTemplateColumns: '130px 1fr' }}>
            <div className="k">{t('nodes.panel_version')}</div><div>{host.version}</div>
            <div className="k">{t('nodes.uptime')}</div><div>{uptime(host.uptime)}</div>
            {host.domains.map((d) => { const c = host.certs.find((c) => c.domain === d); return <div key={d} style={{ display: 'contents' }}><div className="k">{t('nodes.panel_certificate')}</div><div>{!c?.ok || c.placeholder ? <span className="muted">{d} · {t('nodes.panel_cert_placeholder')}</span> : <>{d} · <Trans k="nodes.panel_cert_expires" v={{ v: dateTime(c.notAfter) }} />{c.issuer && ` · ${c.issuer}`}</>}</div></div> })}
          </div><p className="muted small">{t('nodes.panel_hosts_the_web')}</p></div>
          <div><PanelRentalDetails value={host.billing} /><button className="btn sm" onClick={() => setEdit(true)}><Icon name="wallet" /> {t('billing.rental_edit')}</button></div>
        </div>
        {checkError && <div className="alert amber" style={{ marginTop: 12 }}>{t('updates.panel_check_error')} {checkError}</div>}
        <div className="row" style={{ gap: 8, marginTop: 16 }}>
          <button className="btn" disabled={busy} onClick={async () => {
            setChecking(true)
            const info = await act(() => post<UpdateInfo>('updates/check'))
            setChecking(false)
            if (info) onChecked(info)
          }}><Icon name="refresh" /> {t('updates.panel_check')}</button>
          <button className="btn primary" disabled={busy || !update?.available} onClick={async () => {
            if (await confirm(t(host.withNode ? 'updates.panel_confirm_with_node' : 'updates.panel_confirm'), t('updates.panel_update'), false)) onUpdate()
          }}>{t('updates.panel_update')}</button>
          <button className="btn ghost" onClick={() => setLog(true)}>{t('nodes.update_log')}</button>
          {update && !update.available && !checkError && latest && !busy && <span className="muted small">{t('updates.panel_current')}</span>}
        </div>
      </div>
    </div>
    {edit && (providers.data ? <PanelBillingForm value={host.billing} providers={providers.data} remindDays={5} onClose={() => setEdit(false)} onSaved={() => { setEdit(false); onChanged() }} /> : <Modal title={t('billing.rental_edit')} onClose={() => setEdit(false)}><Loading error={providers.error} />{providers.error && <button className="btn" onClick={providers.reload}>{t('common.retry')}</button>}</Modal>)}
    {log && <UpdateLog id={0} name={t('billing.panel_server')} onClose={() => setLog(false)} />}
  </>
}
