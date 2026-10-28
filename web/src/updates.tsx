import { useEffect, useRef, useState } from 'react'
import { get, post, UpdateInfo, UpdateNode } from './api'
import { Badge, Icon, Modal, useAction, useConfirm, useLoad, useToast } from './ui'
import { t, Trans } from './i18n'

type RunResult = { started: string[]; failed: Record<string, string> }

// Обновление идёт: опрашивать чаще.
export const updatesActive = (info: UpdateInfo | null) => !!info && [info.panel, ...info.nodes].some((n) => n.updating)

// Сведения об обновлениях: раз в 20 с, а пока что-то обновляется — раз в 2 с.
export function useUpdates(slowMs = 20000) {
  const [fast, setFast] = useState(false)
  const res = useLoad(() => get<UpdateInfo>('updates'), [fast], fast ? 2000 : slowMs)
  useEffect(() => { setFast(updatesActive(res.data)) }, [res.data])
  return res
}

// Запуск обновления. Если среди запущенных есть панель, страница ждёт, пока панель вернётся
// с новой версией, и перезагружается сама.
export function useRunUpdate(info: UpdateInfo | null, reload: () => void) {
  const act = useAction()
  const toast = useToast()
  const [waiting, setWaiting] = useState(false)

  useEffect(() => {
    if (!waiting || !info) return
    const from = info.panel.version
    const t = setInterval(() => {
      get<{ version: string }>('session').then((s) => { if (s.version !== from) location.reload() }, () => {})
    }, 3000)
    return () => clearInterval(t)
  }, [waiting, info])

  return {
    waiting,
    run: async (target: 'all' | 'panel' | 'node', id?: number) => {
      const r = await act(() => post<RunResult>('updates/run', { target, id }))
      if (!r) return
      if (r.started.length) toast(t('updates.command_sent_to', { v: r.started.join(', ') }))
      for (const [name, err] of Object.entries(r.failed)) toast(`${name}: ${err}`, true)
      if (r.started.length === 0 && Object.keys(r.failed).length === 0) toast(t('updates.nothing_to_update_everything'))
      if (r.started.includes(t('updates.panel'))) setWaiting(true)
      reload()
    },
  }
}

// Флаг по коду страны (как в остальной панели).
const flagOf = (cc: string) => cc.length === 2 ? String.fromCodePoint(...[...cc.toUpperCase()].map((c) => 0x1f1e6 + c.charCodeAt(0) - 65)) : ''

function elapsed(from?: number) {
  if (!from) return ''
  const s = Math.max(0, Math.floor(Date.now() / 1000 - from))
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
}

// Перерисовка раз в секунду, пока идёт обновление (часики на плашке).
function useTick(on: boolean) {
  const [, set] = useState(0)
  useEffect(() => {
    if (!on) return
    const t = setInterval(() => set((x) => x + 1), 1000)
    return () => clearInterval(t)
  }, [on])
}

const stateText: Record<string, string> = {
  sent: t('updates.command_sent'), running: t('updates.updating'), restarting: t('updates.restarting'), done: t('updates.updated'), failed: t('updates.update_failed'),
}

// Плашка статуса обновления; по клику — лог.
export function UpdateChip({ n, latest, onLog }: { n: UpdateNode; latest: string; onLog: () => void }) {
  useTick(n.updating)
  if (n.state) {
    const color = n.state === 'done' ? 'green' : n.state === 'failed' ? 'red' : 'amber'
    return (
      <button className="chip-btn" onClick={onLog} title={t('updates.show_update_log')}>
        <Badge color={color}>
          {n.updating ? <span className="spinner" /> : <span className="dot" />}
          {stateText[n.state]}{n.updating && n.startedAt ? ' · ' + elapsed(n.startedAt) : ''}
        </Badge>
      </button>
    )
  }
  if (n.available) return <Badge color="amber" dot><Trans k="updates.update_available" v={{ v: latest ? ' ' + latest : '' }} /></Badge>
  return null
}

// Окно с ходом обновления и последними строками лога сервера.
// Панель не ответила совсем (не ошибка API): обычно она перезапускается на своё же обновление.
const offline = (e: string | null) => !!e && /NetworkError|Failed to fetch|Load failed|network/i.test(e)

export function UpdateLog({ id, name, onClose }: { id: number; name: string; onClose: () => void }) {
  const [fast, setFast] = useState(true)
  const { data, error } = useLoad(() => get<UpdateNode>(`updates/log?id=${id}`), [fast], fast ? 2000 : 0)
  useEffect(() => { if (data) setFast(data.updating) }, [data])
  // Пока панель недоступна, продолжаем спрашивать: вернётся — покажем.
  useEffect(() => { if (offline(error)) setFast(true) }, [error])
  const act = useAction()
  const finished = data?.state === 'done' || data?.state === 'failed'
  const offlineNote = offline(error) && <div className="alert amber" style={{ marginBottom: 14 }}>{t('updates.the_panel_is_not_responding')}</div>
  useTick(!!data?.updating)
  const pre = useRef<HTMLPreElement>(null)
  useEffect(() => { if (pre.current) pre.current.scrollTop = pre.current.scrollHeight }, [data?.log])
  const steps: [string, string][] = [['sent', t('updates.command_sent_2')], ['running', t('updates.downloading_and_installing')], ['restarting', t('updates.restarting_services')], ['done', t('common.done')]]
  const at = data ? Math.max(0, steps.findIndex(([k]) => k === data.state)) : 0
  return (
    <Modal title={t('updates.update', { name })} onClose={onClose} wide footer={<>
      {finished && <button className="btn ghost" onClick={async () => { await act(() => post('updates/dismiss', { id })); onClose() }}>{t('updates.hide_status')}</button>}
      <button className="btn primary" onClick={onClose}>{t('common.close')}</button>
    </>}>
      {!data ? (offlineNote || (error ? <div className="alert pink">{error}</div> : <div className="muted">{t('common.loading')}</div>)) : (
        <>
          {offlineNote}
          {data.state === 'failed' ? <div className="alert pink" style={{ marginBottom: 14 }}>{t('updates.the_update_failed_if_the')}</div>
            : data.state === '' ? <div className="muted small" style={{ marginBottom: 14 }}>{t('updates.no_update_is_running_now')}</div>
              : (
                <div className="wizard-steps">
                  {steps.map(([k, label], i) => (
                    <div key={k} className={i < at || data.state === 'done' ? 'done' : i === at ? 'on' : ''}>
                      <span>{i < at || data.state === 'done' ? '✓' : i + 1}</span>{label}
                    </div>
                  ))}
                  {data.updating && data.startedAt ? <div className="muted">{elapsed(data.startedAt)}</div> : null}
                </div>
              )}
          <pre ref={pre} className="code update-log">{data.log || (data.updating ? t('updates.waiting_for_the_first_log') : t('updates.no_log_this_server_has'))}</pre>
          <div className="muted small" style={{ marginTop: 8 }}><Trans k="updates.version_full_log_on_the" v={{ v: data.version || '—' }} /></div>
        </>
      )}
    </Modal>
  )
}

// Баннер на дашборде: новая версия, «Обновить всё», ход обновлений.
// Закрытый баннер не возвращается, пока не случится что-то новое: другое обновление или новая версия.
function dismissedKey(): string {
  try { return localStorage.getItem('vynel.update.dismissed') || '' } catch { return '' }
}
function dismiss(key: string) {
  try { localStorage.setItem('vynel.update.dismissed', key) } catch { /* приватный режим */ }
}

export function UpdateBanner() {
  const { data, reload } = useUpdates()
  const confirm = useConfirm()
  const { waiting, run } = useRunUpdate(data, reload)
  const [logOf, setLogOf] = useState<UpdateNode | null>(null)
  const [closed, setClosed] = useState(dismissedKey)
  if (!data) return null
  const all = [data.panel, ...data.nodes]
  const busy = all.filter((n) => n.updating)
  const recent = all.filter((n) => n.state === 'done' || n.state === 'failed')
  const log = logOf && <UpdateLog id={logOf.id} name={logOf.name} onClose={() => setLogOf(null)} />
  // Что именно показано: итог этих обновлений или эта новая версия.
  const key = recent.length > 0 ? 'done:' + recent.map((n) => `${n.id}.${n.state}.${n.finishedAt || 0}`).join(',') : 'new:' + data.latest
  const close = <button className="btn ghost icon sm" title={t('updates.hide')} onClick={() => { dismiss(key); setClosed(key) }}><Icon name="close" /></button>
  if (closed === key && !waiting && busy.length === 0) return null

  if (waiting || busy.length > 0 || recent.length > 0) {
    const failed = recent.filter((n) => n.state === 'failed')
    return (
      <>
        <div className={'banner' + (busy.length === 0 && failed.length > 0 ? ' error' : '')}>
          {busy.length > 0 || waiting ? <span className="spinner" /> : <Icon name="refresh" />}
          <div className="grow">
            <b>{busy.length > 0 || waiting ? t('updates.update_in_progress') : failed.length > 0 ? t('updates.the_update_finished_with_errors') : t('updates.update_finished')}</b>
            <div className="update-list">
              {all.filter((n) => n.state).map((n) => (
                <span key={n.id} className="update-item">{n.flag ? flagOf(n.flag) + ' ' : ''}{n.name}<UpdateChip n={n} latest="" onLog={() => setLogOf(n)} /></span>
              ))}
            </div>
            {(waiting || data.panel.updating) && <div className="small text-2">{t('updates.the_panel_will_restart_and')}</div>}
          </div>
          {busy.length === 0 && data.count > 0 && <button className="btn sm" onClick={() => run('all')}>{t('updates.update_the_rest')}</button>}
          {busy.length === 0 && !waiting && close}
        </div>
        {log}
      </>
    )
  }
  if (data.count === 0) return null

  const nodes = data.nodes.filter((n) => n.available)
  const what = [data.panel.available && t('common.panel'), nodes.length > 0 && t('common.nodes_count', { n: nodes.length })].filter(Boolean).join(t('updates.and'))
  return (
    <div className="banner">
      <Icon name="refresh" />
      <div className="grow">
        <b><Trans k="updates.a_new_vynel_version_is" v={{ latest: data.latest }} /></b>
        <div className="small text-2"><Trans k="updates.outdated_vynel_xray_and_nginx" v={{ what: what }} /></div>
      </div>
      <a className="btn ghost sm" href="#/nodes">{t('updates.details')}</a>
      <button className="btn primary sm" onClick={async () => {
        if (await confirm(<><Trans k="updates.update_now_everything_updates" v={{ what: what }} /></>, t('updates.update_all'), false))
          run('all')
      }}>{t('updates.update_all')}</button>
      {close}
    </div>
  )
}
