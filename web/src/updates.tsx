import { useEffect, useRef, useState } from 'react'
import { ApiError, get, post, UpdateInfo, UpdateNode } from './api'
import { Badge, copyText, Icon, Modal, Tabs, useAction, useConfirm, useLoad, useToast } from './ui'
import { t, Trans } from './i18n'

type RunResult = { started: string[]; startedIds?: number[]; panelStarted?: boolean; failed: Record<string, string> }
export type LogTarget = { id: number; name: string; initial?: UpdateNode }

// Обновление идёт: опрашивать чаще.
export const updatesActive = (info: UpdateInfo | null) => !!info && [info.panel, ...info.nodes].some((n) => n.updating)

// Сведения об обновлениях: раз в 20 с, а пока что-то обновляется — раз в 2 с.
export function useUpdates(slowMs = 20000) {
  const [fast, setFast] = useState(false)
  const res = useLoad(() => get<UpdateInfo>('updates'), [fast], fast ? 2000 : slowMs)
  useEffect(() => { setFast(updatesActive(res.data)) }, [res.data])
  return res
}

// Запуск обновления. После успешного старта вызывает onStarted со списком запущенных серверов,
// чтобы сразу открыть окно с живым логом.
export function useRunUpdate(info: UpdateInfo | null, reload: () => void, onStarted?: (targets: LogTarget[]) => void) {
  const act = useAction()
  const toast = useToast()
  const [waiting, setWaiting] = useState(false)
  const [panelUpdated, setPanelUpdated] = useState(false)
  const fromVersion = useRef('')

  useEffect(() => {
    if (!waiting) return
    if (info?.panel && ((fromVersion.current && info.panel.version !== fromVersion.current) || info.panel.state === 'done' || info.panel.state === 'failed')) {
      setWaiting(false)
      if (info.panel.state !== 'failed') setPanelUpdated(true)
      return
    }
    const timer = setInterval(() => {
      get<{ version: string }>('session').then((s) => {
        if (fromVersion.current && s.version && s.version !== fromVersion.current) {
          setWaiting(false)
          setPanelUpdated(true)
          reload()
        }
      }, () => {})
    }, 2500)
    return () => clearInterval(timer)
  }, [waiting, info?.panel.version, info?.panel.state, reload])

  return {
    waiting,
    panelUpdated,
    run: async (target: 'all' | 'panel' | 'node', id?: number) => {
      const prePanelVer = info?.panel.version || ''
      const r = await act(() => post<RunResult>('updates/run', { target, id }))
      if (!r) return
      if (r.started.length) toast(t('updates.command_sent_to', { v: r.started.join(', ') }))
      for (const [name, err] of Object.entries(r.failed)) toast(`${name}: ${err}`, true)
      if (r.started.length === 0 && Object.keys(r.failed).length === 0) toast(t('updates.nothing_to_update_everything'))
      if (r.panelStarted) {
        fromVersion.current = prePanelVer
        setWaiting(true)
      }
      if (onStarted && r.startedIds && r.startedIds.length > 0) {
        const nowSec = Math.floor(Date.now() / 1000)
        const targets: LogTarget[] = r.startedIds.map((sid, i) => {
          const prev = sid === 0 ? info?.panel : info?.nodes.find((n) => n.id === sid)
          const label = sid === 0
            ? (r.started[i] || info?.panel.name || t('common.panel'))
            : (r.started[i] || prev?.name || `#${sid}`)
          return {
            id: sid,
            name: label,
            initial: {
              id: sid,
              code: prev?.code || '',
              name: label,
              flag: prev?.flag || '',
              local: sid === 0,
              online: true,
              version: prev?.version || '',
              available: false,
              updating: true,
              state: 'sent',
              startedAt: nowSec,
            },
          }
        })
        onStarted(targets)
      }
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
    const timer = setInterval(() => set((x) => x + 1), 1000)
    return () => clearInterval(timer)
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
      <button type="button" className="badge-btn" onClick={onLog} title={t('updates.show_update_log')}>
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

// Панель временно не ответила (обрыв сети или прокси 502/503/504/52x во время перезапуска службы).
function isOfflineErr(e: unknown): boolean {
  if (!e) return false
  if (e instanceof ApiError) {
    return (e.status >= 502 && e.status <= 504) || (e.status >= 520 && e.status <= 524)
  }
  return true
}

export function UpdateLog({ id, name, targets, onClose }: { id: number; name: string; targets?: LogTarget[]; onClose: () => void }) {
  const list: LogTarget[] = targets && targets.length > 0 ? targets : [{ id, name }]
  const [curId, setCurId] = useState(id)
  const curTarget = list.find((x) => x.id === curId) || list[0]
  const [byId, setById] = useState<Record<number, UpdateNode>>(() => {
    const init: Record<number, UpdateNode> = {}
    for (const item of list) {
      if (item.initial) init[item.id] = item.initial
    }
    return init
  })
  const [isOffline, setIsOffline] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const byIdRef = useRef(byId)
  byIdRef.current = byId
  const listKey = list.map((x) => x.id).join(',')

  useEffect(() => {
    setById((prev) => {
      let changed = false
      const next = { ...prev }
      for (const item of list) {
        if (item.initial && !next[item.id]) {
          next[item.id] = item.initial
          changed = true
        }
      }
      return changed ? next : prev
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [listKey])

  useEffect(() => {
    let stopped = false
    let timer: ReturnType<typeof setTimeout> | undefined
    let ac: AbortController | null = null
    const multi = list.length > 1
    const targetId = curTarget.id

    const poll = async () => {
      if (stopped) return
      let nextDelay = 1500
      if (!document.hidden) {
        ac = new AbortController()
        try {
          if (multi) {
            const info = await get<UpdateInfo>('updates?logs=1', ac.signal)
            if (stopped) return
            const next: Record<number, UpdateNode> = { ...byIdRef.current, 0: info.panel }
            for (const n of info.nodes) next[n.id] = n
            byIdRef.current = next
            setById(next)
            setIsOffline(false)
            setError(null)
            const anyActive = list.some((t) => !next[t.id] || next[t.id].updating)
            nextDelay = anyActive ? 1500 : 4000
          } else {
            const node = await get<UpdateNode>(`updates/log?id=${targetId}`, ac.signal)
            if (stopped) return
            const next = { ...byIdRef.current, [node.id]: node }
            byIdRef.current = next
            setById(next)
            setIsOffline(false)
            setError(null)
            nextDelay = node.updating ? 1500 : 4000
          }
        } catch (e: any) {
          if (stopped || ac.signal.aborted) return
          if (isOfflineErr(e)) {
            setIsOffline(true)
            setError(null)
            nextDelay = 1500
          } else {
            setIsOffline(false)
            setError(e?.message || String(e))
            nextDelay = 4000
          }
        }
      }
      if (!stopped) {
        timer = setTimeout(poll, nextDelay)
      }
    }

    poll()
    return () => {
      stopped = true
      if (timer !== undefined) clearTimeout(timer)
      ac?.abort()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [listKey, list.length > 1 ? 0 : curTarget.id])

  const shown = byId[curTarget.id] || null
  const act = useAction()
  const toast = useToast()
  const finished = shown?.state === 'done' || shown?.state === 'failed'
  const offlineNote = isOffline && <div className="alert amber" style={{ marginBottom: 14 }}>{t('updates.the_panel_is_not_responding')}</div>
  useTick(!!shown?.updating || list.some((x) => byId[x.id]?.updating))
  const pre = useRef<HTMLPreElement>(null)
  useEffect(() => {
    if (pre.current) pre.current.scrollTop = pre.current.scrollHeight
  }, [shown?.log, curTarget.id])

  const steps: [string, string][] = [['sent', t('updates.command_sent_2')], ['running', t('updates.downloading_and_installing')], ['restarting', t('updates.restarting_services')], ['done', t('common.done')]]
  const at = shown ? Math.max(0, steps.findIndex(([k]) => k === shown.state)) : 0
  return (
    <Modal title={t('updates.update', { name: curTarget.name })} onClose={onClose} wide footer={<>
      {shown?.log && (
        <button className="btn ghost" onClick={() => copyText(shown.log!).then(() => toast(t('ui.copied')))}>
          <Icon name="copy" /> {t('ui.copy')}
        </button>
      )}
      {finished && <button className="btn ghost" onClick={async () => { await act(() => post('updates/dismiss', { id: curTarget.id })); onClose() }}>{t('updates.hide_status')}</button>}
      {curTarget.id === 0 && shown?.state === 'done' && (
        <button className="btn" onClick={() => location.reload()}><Icon name="refresh" /> {t('updates.reload_page')}</button>
      )}
      <button className="btn primary" onClick={onClose}>{t('common.close')}</button>
    </>}>
      {list.length > 1 && (
        <Tabs
          value={String(curTarget.id)}
          onChange={(v) => setCurId(Number(v))}
          items={list.map((x) => {
            const st = byId[x.id]
            return {
              id: String(x.id),
              label: (
                <span className="row" style={{ gap: 6, display: 'inline-flex', alignItems: 'center' }}>
                  {st?.updating ? <span className="spinner" style={{ width: 11, height: 11 }} />
                    : st?.state === 'done' ? <span style={{ color: 'var(--teal)' }}>✓</span>
                      : st?.state === 'failed' ? <span style={{ color: 'var(--pink)' }}>!</span> : null}
                  <span>{x.name}</span>
                </span>
              ),
            }
          })}
        />
      )}
      {!shown ? (
        isOffline ? (
          <>
            {offlineNote}
            <pre ref={pre} className="code update-log">{t('updates.waiting_for_the_first_log')}</pre>
          </>
        ) : error ? <div className="alert pink">{error}</div> : <div className="muted">{t('common.loading')}</div>
      ) : (
        <>
          {offlineNote}
          {shown.state === 'failed' ? <div className="alert pink" style={{ marginBottom: 14 }}>{t('updates.the_update_failed_if_the')}</div>
            : shown.state === '' ? <div className="muted small" style={{ marginBottom: 14 }}>{t('updates.no_update_is_running_now')}</div>
              : (
                <div className="wizard-steps">
                  {steps.map(([k, label], i) => (
                    <div key={k} className={i < at || shown.state === 'done' ? 'done' : i === at ? 'on' : ''}>
                      <span>{i < at || shown.state === 'done' ? '✓' : i + 1}</span>{label}
                    </div>
                  ))}
                  {shown.updating && shown.startedAt ? <div className="muted">{elapsed(shown.startedAt)}</div> : null}
                </div>
              )}
          <pre ref={pre} className="code update-log">{shown.log || (shown.updating ? t('updates.waiting_for_the_first_log') : t('updates.no_log_this_server_has'))}</pre>
          <div className="muted small" style={{ marginTop: 8 }}><Trans k="updates.version_full_log_on_the" v={{ v: shown.version || '—' }} /></div>
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
  const [logTargets, setLogTargets] = useState<LogTarget[] | null>(null)
  const { waiting, panelUpdated, run } = useRunUpdate(data, reload, (targets) => setLogTargets(targets))
  const [closed, setClosed] = useState(dismissedKey)
  const log = logTargets && logTargets.length > 0 && (
    <UpdateLog id={logTargets[0].id} name={logTargets[0].name} targets={logTargets} onClose={() => { setLogTargets(null); reload() }} />
  )
  if (!data) return log || null
  const all = [data.panel, ...data.nodes]
  const busy = all.filter((n) => n.updating)
  const recent = all.filter((n) => n.state === 'done' || n.state === 'failed')
  // Что именно показано: итог этих обновлений или эта новая версия.
  const key = recent.length > 0 ? 'done:' + recent.map((n) => `${n.id}.${n.state}.${n.finishedAt || 0}`).join(',') : 'new:' + data.latest
  const close = <button className="btn ghost icon sm" title={t('updates.hide')} onClick={() => { dismiss(key); setClosed(key) }}><Icon name="close" /></button>
  if (closed === key && !waiting && !panelUpdated && busy.length === 0) return log || null

  if (waiting || panelUpdated || busy.length > 0 || recent.length > 0) {
    const failed = recent.filter((n) => n.state === 'failed')
    return (
      <>
        <div className={'banner' + (busy.length === 0 && failed.length > 0 ? ' error' : '')}>
          {busy.length > 0 || waiting ? <span className="spinner" /> : <Icon name="refresh" />}
          <div className="grow">
            <b>{busy.length > 0 || waiting ? t('updates.update_in_progress') : failed.length > 0 ? t('updates.the_update_finished_with_errors') : t('updates.update_finished')}</b>
            <div className="update-list">
              {all.filter((n) => n.state).map((n) => (
                <span key={n.id} className="update-item">{n.flag ? flagOf(n.flag) + ' ' : ''}{n.name}<UpdateChip n={n} latest="" onLog={() => setLogTargets([{ id: n.id, name: n.name, initial: n }])} /></span>
              ))}
            </div>
            {(waiting || data.panel.updating) && <div className="small text-2">{t('updates.the_panel_will_restart_and')}</div>}
          </div>
          {panelUpdated && <button className="btn primary sm" onClick={() => location.reload()}><Icon name="refresh" /> {t('updates.reload_page')}</button>}
          {busy.length === 0 && data.count > 0 && <button className="btn sm" onClick={() => run('all')}>{t('updates.update_the_rest')}</button>}
          {busy.length === 0 && !waiting && close}
        </div>
        {log}
      </>
    )
  }
  if (data.count === 0) return log || null

  const nodes = data.nodes.filter((n) => n.available)
  const what = [data.panel.available && t('common.panel'), nodes.length > 0 && t('common.nodes_count', { n: nodes.length })].filter(Boolean).join(t('updates.and'))
  return (
    <>
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
      {log}
    </>
  )
}
