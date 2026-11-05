import { FormEvent, useEffect, useState } from 'react'
import { del, get, post, put } from '../api'
import { dateTime } from '../format'
import { Badge, CopyField, Field, Icon, Info, Loading, Switch, useAction, useConfirm, useLoad } from '../ui'
import { t, Trans } from '../i18n'

interface RouteNode { id: number; name: string; country: string; enabled: boolean; online: boolean; supported: boolean }

interface BotInfo {
  nodeId: number
  routeNodes: RouteNode[]
  hasToken: boolean
  status: { running: boolean; username?: string; error?: string }
  admins: { id: number; name: string; username?: string; boundAt: number }[]
  backupTime: string
  summaryTime: string
  timezone: string
  alerts: boolean
  alertDelay: string
  backupEncrypted: boolean
}

const zones = ['Europe/Moscow', 'Europe/Kaliningrad', 'Europe/Samara', 'Asia/Yekaterinburg', 'Asia/Omsk', 'Asia/Novosibirsk',
  'Asia/Krasnoyarsk', 'Asia/Irkutsk', 'Asia/Vladivostok', 'Europe/Minsk', 'Europe/Kyiv', 'Asia/Almaty', 'Asia/Tbilisi', 'Europe/Berlin', 'UTC']

export function Telegram() {
  const info = useLoad(() => get<BotInfo>('bot'), [], 10000)
  const [token, setToken] = useState('')
  const [backupPw, setBackupPw] = useState('')
  const [code, setCode] = useState<{ code: string; minutes: number; link?: string; username?: string } | null>(null)
  const act = useAction()
  const confirm = useConfirm()

  if (!info.data) return <Loading error={info.error} />
  const d = info.data
  const save = async (body: Record<string, unknown>, ok = t('common.saved')) => {
    const r = await act(() => put<BotInfo>('bot', body), ok)
    if (r) info.setData(r)
    return r
  }
  const st = d.status

  return (
    <div className="cards" style={{ maxWidth: 860 }}>
      <div className="card">
        <div className="card-head">
          <div>
            <h3>{t('telegram.bot')}{' '}<Info>{t('telegram.user_management_passwordless')}</Info></h3>
          </div>
          {!d.hasToken ? <Badge>{t('telegram.not_set_up')}</Badge> : st.running ? <Badge color="green" dot>@{st.username}</Badge> : st.error ? <Badge color="pink" dot>{t('common.error')}</Badge> : <Badge color="amber" dot>{t('telegram.starting')}</Badge>}
        </div>
        <div className="card-pad">
          {st.error && <div className="alert pink" style={{ marginBottom: 14 }}>{st.error}</div>}
          {!d.hasToken && (
            <ol className="list-plain small text-2" style={{ marginTop: 0 }}>
              <li><Trans k="telegram.in_telegram_open_newbot_the" v={{ v: <a href="https://t.me/BotFather" target="_blank" rel="noreferrer">@BotFather</a> }} /></li>
              <li><Trans k="telegram.copy_the_token_that_looks" /></li>
              <li>{t('telegram.then_link_your_account_with')}</li>
            </ol>
          )}
          <form className="row" style={{ flexWrap: 'nowrap' }} onSubmit={async (e: FormEvent) => {
            e.preventDefault()
            if (await save({ token }, t('telegram.token_saved_the_bot_will'))) setToken('')
          }}>
            <input className="input mono" type="password" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)}
              placeholder={d.hasToken ? t('telegram.token_is_set_paste_a') : '123456789:AA…'} />
            <button className="btn primary" disabled={!token.trim()}>{t('common.save')}</button>
            {d.hasToken && <button type="button" className="btn danger" onClick={async () => {
              if (await confirm(t('telegram.turn_the_bot_off_notifications'), t('telegram.turn_off'))) save({ token: '' }, t('telegram.bot_turned_off'))
            }}>{t('telegram.turn_off')}</button>}
          </form>
        </div>
      </div>

      <BotConnection d={d} onSaved={info.setData} />

      <div className="card">
        <div className="card-head">
          <div>
            <h3>{t('telegram.administrators')}</h3>
            <div className="card-sub">{t('telegram.all_have_full_rights_the')}</div>
          </div>
          <button className="btn sm" disabled={!d.hasToken} onClick={async () => {
            const r = await act(() => post<{ code: string; minutes: number; link?: string; username?: string }>('bot/code'))
            if (r) setCode(r)
          }}><Icon name="plus" />{' '}{t('telegram.link_an_account')}</button>
        </div>
        <div className="card-pad">
          {code && (
            <div className="alert green" style={{ marginBottom: 14 }}>
              {code.link ? <>{t('telegram.press_open_bot_in_the')}</> : <>{t('telegram.send_the_bot_a_message')}</>}
              <div className="row" style={{ marginTop: 10 }}>
                {code.link && <a className="btn primary" href={code.link} target="_blank" rel="noreferrer"><Icon name="telegram" />{' '}{t('telegram.open_bot')}</a>}
                <div className="grow" style={{ minWidth: 200 }}><CopyField value={`/start ${code.code}`} /></div>
              </div>
              <div className="small" style={{ marginTop: 8 }}><Trans k="telegram.the_code_is_single_use" v={{ minutes: code.minutes }} /></div>
            </div>
          )}
          {d.admins.length === 0 ? <div className="muted small">{t('telegram.nobody_yet_a_linked_account')}</div> : (
            <table className="table">
              <tbody>
                {d.admins.map((a) => (
                  <tr key={a.id}>
                    <td><b>{a.name || a.id}</b> {a.username && <span className="muted">@{a.username}</span>}<div className="small muted mono">id {a.id}</div></td>
                    <td className="small muted nowrap"><Trans k="telegram.since" v={{ v: dateTime(a.boundAt) }} /></td>
                    <td style={{ width: 40 }}>
                      <button className="btn ghost icon sm" title={t('common.unlink')} onClick={async () => {
                        if (await confirm(<><Trans k="telegram.unlink_the_bot_will_stop" v={{ v: a.name || a.id }} /></>, t('common.unlink')))
                          act(() => del(`bot/admins/${a.id}`), t('telegram.unlinked')).then(() => info.reload())
                      }}><Icon name="trash" /></button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>

      <div className="card">
        <div className="card-head"><div><h3>{t('telegram.backups')}{' '}<Info><Trans k="telegram.an_archive_the_panel_database" /></Info></h3><div className="card-sub"><Trans k="telegram.the_last_7_nightly_ones" /></div></div></div>
        <div className="card-pad">
          <div className="grid2">
            <Field label={t('telegram.every_day_at')} help={t('telegram.empty_do_not_send')}>
              <input className="input" type="time" defaultValue={d.backupTime === 'off' ? '' : d.backupTime}
                onBlur={(e) => { if ((e.target.value || 'off') !== d.backupTime) save({ backupTime: e.target.value }) }} />
            </Field>
            <Field label={t('telegram.time_zone')} info={t('telegram.for_the_schedule_and_the')}>
              <select className="input" value={d.timezone} onChange={(e) => save({ timezone: e.target.value })}>
                {(zones.includes(d.timezone) ? zones : [d.timezone, ...zones]).map((z) => <option key={z} value={z}>{z}</option>)}
              </select>
            </Field>
          </div>
          <Field label={t('telegram.encrypt_backups_with_a_password')} help={d.backupEncrypted ? t('telegram.on_a_backup_cannot_be') : t('common.optional')}
            info={t('telegram.with_a_password_backups_telegram')}>
            <div className="row">
              <input className="input" type="password" autoComplete="new-password" placeholder={d.backupEncrypted ? t('telegram.new_password') : t('telegram.at_least_8_characters')}
                value={backupPw} onChange={(e) => setBackupPw(e.target.value)} />
              <button className="btn" disabled={backupPw.length < 8}
                onClick={async () => { if (await save({ backupPassword: backupPw }, t('telegram.backups_are_encrypted'))) setBackupPw('') }}>
                {d.backupEncrypted ? t('common.change') : t('telegram.turn_on')}
              </button>
              {d.backupEncrypted && <button className="btn ghost" onClick={() => save({ backupPassword: '' }, t('telegram.backup_encryption_is_off'))}>{t('telegram.turn_off')}</button>}
            </div>
          </Field>
          <div className="row">
            <button className="btn" disabled={!st.running || d.admins.length === 0} onClick={() => act(() => post('bot/backup'), t('telegram.backup_sent_to_telegram'))}>
              <Icon name="telegram" />{' '}{t('telegram.send_a_backup_now')}</button>
            <a className="btn" href="api/backup" download><Icon name="refresh" />{' '}{t('telegram.download_a_backup')}</a>
          </div>
          <div className="small pink-text" style={{ marginTop: 10 }}>{t('telegram.a_backup_holds_the_keys')}</div>
        </div>
      </div>

      <div className="card">
        <div className="card-head"><h3>{t('telegram.summary_and_sign_in')}</h3></div>
        <div className="card-pad">
          <Field label={t('telegram.daily_summary_at')} help={t('telegram.empty_do_not_send_right')} info={t('telegram.traffic_active_users_whose')}>
            <input className="input" style={{ maxWidth: 160 }} type="time" defaultValue={d.summaryTime === 'off' ? '' : d.summaryTime}
              onBlur={(e) => { if ((e.target.value || 'off') !== d.summaryTime) save({ summaryTime: e.target.value }) }} />
          </Field>
          <div className="muted small">{t('telegram.sign_in_messages_and_the')}{' '}<Info>
              <p>{t('telegram.the_bot_writes_about_every')}</p>
              <p>{t('telegram.the_panel_button_next_to')}</p>
            </Info>
          </div>
        </div>
      </div>

      <div className="card">
        <div className="card-head"><h3>{t('telegram.node_notifications')}</h3></div>
        <div className="card-pad">
          <div className="field">
            <div className="row between" style={{ flexWrap: 'nowrap' }}>
              <div>
                <div>{t('telegram.report_when_a_node_is')}{' '}<Info>{t('telegram.one_message_per_outage_it')}</Info></div>
              </div>
              <Switch checked={d.alerts} onChange={(v) => save({ alerts: v })} />
            </div>
          </div>
          <Field label={t('telegram.after_how_many_seconds_without')} info={t('telegram.short_node_restarts_will_not')}>
            <input className="input" style={{ maxWidth: 160 }} inputMode="numeric" defaultValue={d.alertDelay}
              onBlur={(e) => { if (e.target.value !== d.alertDelay) save({ alertDelay: Number(e.target.value) || 0 }) }} />
          </Field>
        </div>
      </div>
    </div>
  )
}


function BotConnection({ d, onSaved }: { d: BotInfo; onSaved: (info: BotInfo) => void }) {
  const [nodeId, setNodeId] = useState(d.nodeId)
  const [dirty, setDirty] = useState(false)
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<{ ok: boolean; username?: string; error?: string } | null>(null)
  const act = useAction()
  useEffect(() => { if (!dirty) setNodeId(d.nodeId) }, [d.nodeId, dirty])
  const selected = d.routeNodes.find((n) => n.id === nodeId)
  const valid = nodeId === 0 || !!selected?.enabled && !!selected?.supported
  const apply = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    try {
      const saved = await act(() => put<BotInfo>('bot', { nodeId }), t('telegram.route_saved'))
      if (saved) { onSaved(saved); setDirty(false); setResult(null) }
    } finally { setBusy(false) }
  }
  const test = async () => {
    setBusy(true)
    setResult(null)
    try {
      const checked = await act(() => post<{ ok: boolean; username?: string; error?: string }>('bot/test', { nodeId }))
      if (checked) setResult(checked)
    } finally { setBusy(false) }
  }
  return <div className="card">
    <div className="card-head"><h3>{t('telegram.connection')}</h3></div>
    <form className="card-pad" onSubmit={apply}>
      <p className="small text-2" style={{ marginTop: 0 }}>{t('telegram.route_current', { name: d.nodeId === 0 ? t('telegram.route_direct') : d.routeNodes.find((n) => n.id === d.nodeId)?.name || t('telegram.route_missing') })}</p>
      <Field label={t('telegram.route_label')} help={t('telegram.route_help')}>
        <select className="input" value={nodeId} disabled={busy} onChange={(e) => { setNodeId(Number(e.target.value)); setDirty(true); setResult(null) }}>
          <option value={0}>{t('telegram.route_direct')}</option>
          {nodeId !== 0 && !selected && <option value={nodeId} disabled>{t('telegram.route_missing')}</option>}
          {d.routeNodes.map((n) => <option key={n.id} value={n.id} disabled={!n.enabled || !n.supported}>
            {n.name} {n.country && `(${n.country})`} · {!n.enabled ? t('telegram.route_disabled') : !n.supported ? t('telegram.route_upgrade') : n.online ? t('common.online') : t('telegram.route_offline')}
          </option>)}
        </select>
      </Field>
      {nodeId !== 0 && <p className="muted small">{t('telegram.route_node_details')}</p>}
      {!valid && <div className="alert amber" style={{ marginBottom: 12 }}>{t('telegram.route_invalid')}</div>}
      {d.routeNodes.some((n) => !n.supported) && <p className="muted small">{t('telegram.route_upgrade_help')}</p>}
      <div className="row">
        <button className="btn primary" disabled={busy || !dirty || !valid}>{t('common.save')}</button>
        <button type="button" className="btn" disabled={busy || !d.hasToken || !valid} onClick={test}>
          <Icon name="plug" /> {busy ? t('common.loading') : t('telegram.test_connection')}
        </button>
      </div>
      <p className="muted small">{d.hasToken ? t('telegram.route_test_unsaved') : t('telegram.test_needs_token')}</p>
      {result && <div className={'alert ' + (result.ok ? 'green' : 'pink')} style={{ marginTop: 14 }} role="status">
        {result.ok ? t('telegram.connection_ok', { username: result.username || '—' }) : result.error}
      </div>}
    </form>
  </div>
}
