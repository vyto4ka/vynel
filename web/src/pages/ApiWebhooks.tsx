import { useState } from 'react'
import { del, get, Delivery, Integrations, patch, post, Webhook } from '../api'
import { ago, dateTime } from '../format'
import { Badge, CopyField, Empty, Field, Icon, Info, Menu, Modal, Switch, useAction, useConfirm, useLoad, useToast } from '../ui'
import { t, Trans } from '../i18n'
import { SnippetLang, snippetLangs, verifySnippets } from '../apiSnippets'

// Event groups for the checkboxes: «user.*» takes every user event, also the ones added later.
const groups = ['user.*', 'node.*', 'group.*']

export function Webhooks({ d, reload }: { d: Integrations; reload: () => void }) {
  const [form, setForm] = useState<Webhook | 'new' | null>(null)
  const [secret, setSecret] = useState<Webhook | null>(null)
  const [log, setLog] = useState<Webhook | null>(null)
  const act = useAction()
  const confirm = useConfirm()
  const toast = useToast()

  const test = async (h: Webhook) => {
    const r = await act(() => post<Delivery>(`integrations/webhooks/${h.id}/test`))
    if (r) toast(r.state === 'ok' ? t('integrations.test_ok', { status: r.status, ms: r.tookMs }) : t('integrations.test_fail', { error: r.error }), r.state !== 'ok')
    reload()
  }

  return (
    <div className="cards" style={{ maxWidth: 980 }}>
      <div className="card">
        <div className="card-head">
          <div className="grow" />
          <button className="btn primary" onClick={() => setForm('new')}><Icon name="plus" />{' '}{t('integrations.add_webhook')}</button>
        </div>
        {d.webhooks.length === 0 ? <div className="card-pad"><Empty icon="link">{t('integrations.hooks_empty')}</Empty></div> : (
          <div className="table-wrap">
            <table className="table">
              <tbody>
                {d.webhooks.map((h) => (
                  <tr key={h.id}>
                    <td>
                      <b>{h.name}</b>
                      <div className="small muted mono" style={{ wordBreak: 'break-all' }}>{h.url}</div>
                      <div className="badges" style={{ marginTop: 4 }}>
                        {(h.events.includes('*') ? [t('integrations.hook_events_all')] : h.events).map((e) => <Badge key={e}>{e}</Badge>)}
                      </div>
                    </td>
                    <td className="small nowrap">
                      {h.enabled ? <Badge color="green" dot>{t('integrations.hook_on')}</Badge>
                        : h.offReason ? <Badge color="pink" dot>{t('integrations.hook_failed_off')}</Badge>
                          : <Badge dot>{t('integrations.hook_off')}</Badge>}
                      {!h.enabled && h.offReason && <Info>{t('integrations.hook_failed_off_info', { n: h.fails })}</Info>}
                      <div className="muted">
                        {h.lastAt ? <>{t('integrations.hook_last', { code: h.lastStatus || '—' })}, {ago(h.lastAt)}</> : t('integrations.hook_never')}
                      </div>
                    </td>
                    <td className="nowrap" style={{ textAlign: 'right' }}>
                      <button className="btn sm" onClick={() => test(h)}>{t('integrations.send_test')}</button>{' '}
                      <button className="btn sm" onClick={() => setLog(h)}>{t('integrations.deliveries')}</button>{' '}
                      <Menu title={h.name} items={[
                        { label: t('common.edit'), icon: 'edit', onClick: () => setForm(h) },
                        { label: t('integrations.rotate'), icon: 'key', onClick: async () => {
                          if (!await confirm(t('integrations.rotate_confirm'), t('integrations.rotate'))) return
                          const r = await act(() => post<Webhook>(`integrations/webhooks/${h.id}/rotate`))
                          if (r) { setSecret(r); reload() }
                        } },
                        'divider',
                        { label: t('common.delete'), icon: 'trash', danger: true, onClick: async () => {
                          if (await confirm(t('integrations.delete_hook_confirm', { name: h.name }), t('common.delete')))
                            act(() => del(`integrations/webhooks/${h.id}`), t('integrations.hook_deleted')).then(reload)
                        } },
                      ]} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
      <Verify d={d} />
      {form && <HookForm d={d} hook={form === 'new' ? null : form} onClose={() => setForm(null)} onSaved={(h) => { setForm(null); reload(); if (h?.secret) setSecret(h) }} />}
      {secret && (
        <Modal title={t('integrations.secret_title')} onClose={() => setSecret(null)} footer={<button className="btn primary" onClick={() => setSecret(null)}>{t('integrations.done')}</button>}>
          <div className="alert amber" style={{ marginBottom: 14 }}>{t('integrations.secret_warn')}</div>
          <CopyField value={secret.secret!} wrap />
        </Modal>
      )}
      {log && <Deliveries hook={log} onClose={() => { setLog(null); reload() }} />}
    </div>
  )
}

function HookForm({ d, hook, onClose, onSaved }: { d: Integrations; hook: Webhook | null; onClose: () => void; onSaved: (h?: Webhook) => void }) {
  const [name, setName] = useState(hook?.name ?? '')
  const [url, setUrl] = useState(hook?.url ?? '')
  const [events, setEvents] = useState<string[]>(hook ? hook.events.filter((e) => e !== '*') : [])
  const [enabled, setEnabled] = useState(hook?.enabled ?? true)
  const [priv, setPriv] = useState(hook?.allowPrivate ?? false)
  const act = useAction()
  const toggle = (e: string, on: boolean) => setEvents(on ? [...events, e] : events.filter((x) => x !== e))
  const covered = (e: string) => events.some((g) => g.endsWith('*') && e.startsWith(g.slice(0, -1)))
  const save = async () => {
    const body = { name, url, events, enabled, allowPrivate: priv }
    const r = await act(() => hook ? patch<Webhook>(`integrations/webhooks/${hook.id}`, body) : post<Webhook>('integrations/webhooks', body), hook ? t('integrations.token_saved') : undefined)
    if (r) onSaved(hook ? undefined : r)
  }
  return (
    <Modal title={hook ? t('integrations.edit_webhook', { name: hook.name }) : t('integrations.add_webhook')} onClose={onClose}
      footer={<><button className="btn" onClick={onClose}>{t('integrations.cancel')}</button><button className="btn primary" disabled={!name.trim() || !url.trim()} onClick={save}>{hook ? t('integrations.save') : t('integrations.add_webhook')}</button></>}>
      <Field label={t('integrations.f_name')}>
        <input className="input" value={name} maxLength={64} autoFocus onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field label={t('integrations.f_url')} help={t('integrations.f_url_help')}>
        <input className="input mono" value={url} placeholder="https://billing.example.com/vynel-hook" onChange={(e) => setUrl(e.target.value)} />
      </Field>
      <Field label={t('integrations.f_events')} info={t('integrations.f_events_info')}>
        <div style={{ display: 'grid', gap: 10 }}>
          {groups.map((g) => (
            <div key={g}>
              <label className="check"><input type="checkbox" checked={events.includes(g)} onChange={(e) => toggle(g, e.target.checked)} /><b className="mono">{g}</b></label>
              <div className="row" style={{ gap: 4, marginLeft: 28 }}>
                {d.events.filter((e) => e.startsWith(g.slice(0, -1))).map((e) => (
                  <label key={e} className="check small" style={{ marginRight: 12, opacity: covered(e) ? 0.5 : 1 }}>
                    <input type="checkbox" checked={covered(e) || events.includes(e)} disabled={covered(e)} onChange={(x) => toggle(e, x.target.checked)} />
                    <span className="mono">{e}</span>
                  </label>
                ))}
              </div>
            </div>
          ))}
        </div>
      </Field>
      <Field label={<>{t('integrations.f_allow_private')}</>} info={t('integrations.f_allow_private_info')}>
        <Switch checked={priv} onChange={setPriv} />
      </Field>
      <Field label={t('integrations.f_enabled')}>
        <Switch checked={enabled} onChange={setEnabled} />
      </Field>
      {hook && <div className="small muted">{t('integrations.secret_end', { hint: hook.secretHint.replace('…', '') })}</div>}
    </Modal>
  )
}

function Deliveries({ hook, onClose }: { hook: Webhook; onClose: () => void }) {
  const list = useLoad(() => get<Delivery[]>(`integrations/webhooks/${hook.id}/deliveries`), [hook.id], 5000)
  const [open, setOpen] = useState<Delivery | null>(null)
  const act = useAction()
  return (
    <Modal title={t('integrations.deliveries_title', { name: hook.name })} xl onClose={onClose}>
      {!list.data ? null : list.data.length === 0 ? <Empty>{t('integrations.deliveries_empty')}</Empty> : (
        <div className="table-wrap">
          <table className="table">
            <thead><tr><th>{t('integrations.d_when')}</th><th>{t('integrations.d_event')}</th><th>{t('integrations.d_result')}</th><th /></tr></thead>
            <tbody>
              {list.data.map((x) => (
                <tr key={x.id} className="click" onClick={() => setOpen(open?.id === x.id ? null : x)}>
                  <td className="small nowrap">{dateTime(x.createdAt)}</td>
                  <td className="mono small">{x.type}{x.eventId > 0 && <span className="muted"> · evt_{x.eventId}</span>}</td>
                  <td className="small">
                    {x.state === 'ok' && <Badge color="green" dot>{t('integrations.d_ok')}</Badge>}
                    {x.state === 'failed' && <Badge color="pink" dot>{t('integrations.d_failed')}</Badge>}
                    {x.state === 'pending' && <Badge color="amber" dot>{t('integrations.d_pending', { n: x.attempts + 1, when: dateTime(x.nextAt) })}</Badge>}
                    <span className="muted">{' '}{x.status ? `HTTP ${x.status}` : ''}{x.tookMs ? ` · ${x.tookMs} ms` : ''}</span>
                    {x.error && <div className="pink-text" style={{ wordBreak: 'break-word' }}>{x.error}</div>}
                  </td>
                  <td style={{ textAlign: 'right' }} onClick={(e) => e.stopPropagation()}>
                    <button className="btn sm" title={t('integrations.d_retry_info')} onClick={async () => { await act(() => post(`integrations/webhooks/${hook.id}/deliveries/${x.id}/retry`)); list.reload() }}>{t('integrations.d_retry')}</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {open && (
        <div style={{ marginTop: 14 }}>
          <div className="small muted">{t('integrations.d_body')}</div>
          <pre className="snippet">{prettyBody(open.body)}</pre>
        </div>
      )}
    </Modal>
  )
}

function prettyBody(s: string): string {
  try {
    return JSON.stringify(JSON.parse(s), null, 2)
  } catch {
    return s
  }
}

function Verify({ d }: { d: Integrations }) {
  const [lang, setLang] = useState<Exclude<SnippetLang, 'curl'>>('python')
  return (
    <div className="card">
      <div className="card-head"><h3>{t('integrations.verify_title')}{' '}<Info>{t('integrations.verify_info')}</Info></h3>
        <div className="seg sm">
          {snippetLangs.filter((l) => l.id !== 'curl').map((l) => <button key={l.id} className={lang === l.id ? 'on' : ''} onClick={() => setLang(l.id as Exclude<SnippetLang, 'curl'>)}>{l.label}</button>)}
        </div>
      </div>
      <div className="card-pad">
        <pre className="snippet" style={{ marginTop: 0 }}>{verifySnippets[lang]}</pre>
        <div className="small text-2"><Trans k="integrations.verify_headers" v={{
          sig: <code>{d.headers.signature}</code>, ts: <code>{d.headers.timestamp}</code>, ev: <code>{d.headers.event}</code>, id: <code>{d.headers.delivery}</code> }} /></div>
        <div className="small text-2" style={{ marginTop: 8 }}>{t('integrations.verify_retries')}</div>
      </div>
    </div>
  )
}
