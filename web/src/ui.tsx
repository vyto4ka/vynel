import { createContext, ReactNode, useCallback, useContext, useEffect, useRef, useState } from 'react'
import { Day } from './api'
import { bytes } from './format'

// ---------- иконки ----------

const paths: Record<string, ReactNode> = {
  dashboard: <><rect x="3" y="3" width="7" height="9" rx="1.5" /><rect x="14" y="3" width="7" height="5" rx="1.5" /><rect x="14" y="12" width="7" height="9" rx="1.5" /><rect x="3" y="16" width="7" height="5" rx="1.5" /></>,
  users: <><circle cx="9" cy="8" r="3.5" /><path d="M2.5 20c.6-3.6 3.3-5.5 6.5-5.5s5.9 1.9 6.5 5.5" /><path d="M16 4.5a3.5 3.5 0 0 1 0 7M18 14.8c1.9.6 3.2 2.3 3.5 5.2" /></>,
  groups: <><rect x="3" y="4" width="18" height="6" rx="2" /><rect x="3" y="14" width="18" height="6" rx="2" /><circle cx="7" cy="7" r=".8" /><circle cx="7" cy="17" r=".8" /></>,
  templates: <><path d="M6 3h9l4 4v14H6z" /><path d="M15 3v4h4M9 12h7M9 16h5" /></>,
  nodes: <><rect x="3" y="4" width="18" height="7" rx="2" /><rect x="3" y="13" width="18" height="7" rx="2" /><path d="M7 7.5h.01M7 16.5h.01M11 7.5h6M11 16.5h6" /></>,
  profiles: <><path d="M12 3 3 8l9 5 9-5z" /><path d="m3 13 9 5 9-5" /></>,
  settings: <><circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z" /></>,
  audit: <><path d="M12 7v5l3 2" /><circle cx="12" cy="12" r="9" /></>,
  account: <><circle cx="12" cy="8" r="4" /><path d="M4 21c1-4 4-6 8-6s7 2 8 6" /></>,
  logout: <><path d="M15 4h3a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-3" /><path d="M10 17l-5-5 5-5M5 12h11" /></>,
  plus: <path d="M12 5v14M5 12h14" />,
  close: <path d="M6 6l12 12M18 6 6 18" />,
  menu: <path d="M4 7h16M4 12h16M4 17h16" />,
  collapse: <path d="m15 6-6 6 6 6" />,
  expand: <path d="m9 6 6 6-6 6" />,
  copy: <><rect x="9" y="9" width="11" height="11" rx="2" /><path d="M5 15V5a2 2 0 0 1 2-2h10" /></>,
  refresh: <><path d="M20 11a8 8 0 1 0-2.3 5.7" /><path d="M20 4v7h-7" /></>,
  trash: <><path d="M4 7h16M10 11v6M14 11v6" /><path d="M6 7l1 13h10l1-13M9 7V4h6v3" /></>,
  edit: <><path d="M4 20h4L19 9l-4-4L4 16z" /><path d="m13.5 6.5 4 4" /></>,
  search: <><circle cx="11" cy="11" r="7" /><path d="m20 20-3.5-3.5" /></>,
  key: <><circle cx="8" cy="15" r="4" /><path d="m11 12 9-9M17 6l3 3" /></>,
  link: <><path d="M10 14a5 5 0 0 0 7 0l3-3a5 5 0 0 0-7-7l-1 1" /><path d="M14 10a5 5 0 0 0-7 0l-3 3a5 5 0 0 0 7 7l1-1" /></>,
  telegram: <path d="M21 4 3 11l6 2.5M21 4l-4 16-8-6.5M21 4 9 13.5V19l3-3.5" />,
  code: <path d="m8 7-5 5 5 5M16 7l5 5-5 5" />,
  sub: <><rect x="6" y="2.5" width="12" height="19" rx="2.5" /><path d="M10 6h4M9.5 11h5M9.5 14.5h5M11 18h2" /></>,
}

export function Icon({ name, size }: { name: string; size?: number }) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      {paths[name]}
    </svg>
  )
}

// ---------- уведомления ----------

type Toast = { id: number; text: string; err?: boolean }
const ToastCtx = createContext<(text: string, err?: boolean) => void>(() => {})
export const useToast = () => useContext(ToastCtx)

export function ToastProvider({ children }: { children: ReactNode }) {
  const [list, setList] = useState<Toast[]>([])
  const push = useCallback((text: string, err?: boolean) => {
    const id = Date.now() + Math.random()
    setList((l) => [...l, { id, text, err }])
    setTimeout(() => setList((l) => l.filter((t) => t.id !== id)), err ? 6000 : 3000)
  }, [])
  return (
    <ToastCtx.Provider value={push}>
      {children}
      <div className="toasts">
        {list.map((t) => (
          <div key={t.id} className={'toast' + (t.err ? ' err' : '')}>{t.text}</div>
        ))}
      </div>
    </ToastCtx.Provider>
  )
}

// Выполнить действие с уведомлением об ошибке/успехе.
export function useAction() {
  const toast = useToast()
  return useCallback(
    async <T,>(fn: () => Promise<T>, ok?: string): Promise<T | undefined> => {
      try {
        const r = await fn()
        if (ok) toast(ok)
        return r
      } catch (e: any) {
        toast(e?.message || String(e), true)
        return undefined
      }
    },
    [toast],
  )
}

// ---------- загрузка данных ----------

export function useLoad<T>(fn: () => Promise<T>, deps: unknown[] = [], refreshMs = 0) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<string | null>(null)
  const fnRef = useRef(fn)
  fnRef.current = fn
  const reload = useCallback(async () => {
    try {
      setData(await fnRef.current())
      setError(null)
    } catch (e: any) {
      setError(e?.message || String(e))
    }
  }, [])
  useEffect(() => {
    reload()
    if (!refreshMs) return
    const t = setInterval(() => {
      if (!document.hidden) reload()
    }, refreshMs)
    return () => clearInterval(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)
  return { data, error, reload, setData }
}

export function Loading({ error }: { error?: string | null }) {
  if (error) return <div className="alert pink">{error}</div>
  return <div className="empty">Загрузка…</div>
}

// ---------- оверлеи ----------

export function Modal({ title, children, footer, onClose, wide, xl }: { title: ReactNode; children: ReactNode; footer?: ReactNode; onClose: () => void; wide?: boolean; xl?: boolean }) {
  useEffect(() => {
    const h = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [onClose])
  return (
    <div className="modal-back" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className={'modal' + (wide ? ' wide' : '') + (xl ? ' xl' : '')} role="dialog">
        <div className="modal-head">
          <h2>{title}</h2>
          <button className="btn ghost icon sm" onClick={onClose} aria-label="Закрыть"><Icon name="close" /></button>
        </div>
        <div className="modal-body">{children}</div>
        {footer && <div className="modal-foot">{footer}</div>}
      </div>
    </div>
  )
}

export function Drawer({ title, children, onClose, actions }: { title: ReactNode; children: ReactNode; onClose: () => void; actions?: ReactNode }) {
  useEffect(() => {
    const h = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [onClose])
  return (
    <>
      <div className="drawer-back" onClick={onClose} />
      <aside className="drawer">
        <div className="drawer-head">
          <h2 className="grow ellipsis">{title}</h2>
          {actions}
          <button className="btn ghost icon sm" onClick={onClose} aria-label="Закрыть"><Icon name="close" /></button>
        </div>
        <div className="drawer-body">{children}</div>
      </aside>
    </>
  )
}

type ConfirmState = { text: ReactNode; ok: string; danger?: boolean; resolve: (v: boolean) => void } | null
const ConfirmCtx = createContext<(text: ReactNode, ok?: string, danger?: boolean) => Promise<boolean>>(async () => false)
export const useConfirm = () => useContext(ConfirmCtx)

export function ConfirmProvider({ children }: { children: ReactNode }) {
  const [st, setSt] = useState<ConfirmState>(null)
  const ask = useCallback((text: ReactNode, ok = 'Да', danger = true) => new Promise<boolean>((resolve) => setSt({ text, ok, danger, resolve })), [])
  const close = (v: boolean) => {
    st?.resolve(v)
    setSt(null)
  }
  return (
    <ConfirmCtx.Provider value={ask}>
      {children}
      {st && (
        <Modal title="Подтверждение" onClose={() => close(false)}
          footer={<><button className="btn ghost" onClick={() => close(false)}>Отмена</button>
            <button className={'btn ' + (st.danger ? 'pink' : 'primary')} autoFocus onClick={() => close(true)}>{st.ok}</button></>}>
          <div className="text-2">{st.text}</div>
        </Modal>
      )}
    </ConfirmCtx.Provider>
  )
}

// ---------- мелочи ----------

export function Badge({ color, children, dot }: { color?: string; children: ReactNode; dot?: boolean }) {
  return <span className={'badge ' + (color || '')}>{dot && <span className="dot" />}{children}</span>
}

export function Field({ label, help, children }: { label: ReactNode; help?: ReactNode; children: ReactNode }) {
  return (
    <div className="field">
      <label>{label}</label>
      {children}
      {help && <div className="help">{help}</div>}
    </div>
  )
}

export function Switch({ checked, onChange, disabled }: { checked: boolean; onChange: (v: boolean) => void; disabled?: boolean }) {
  return (
    <label className="switch">
      <input type="checkbox" checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />
      <span />
    </label>
  )
}

export function CopyField({ value, wrap }: { value: string; wrap?: boolean }) {
  const toast = useToast()
  return (
    <div className={'copy' + (wrap ? ' wrap' : '')}>
      <code title={value}>{value}</code>
      <button className="btn sm" onClick={() => copyText(value).then(() => toast('Скопировано'))}>
        <Icon name="copy" /> Копировать
      </button>
    </div>
  )
}

export async function copyText(text: string) {
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    // http без TLS (SSH-туннель): запасной способ
    const ta = document.createElement('textarea')
    ta.value = text
    document.body.appendChild(ta)
    ta.select()
    document.execCommand('copy')
    ta.remove()
  }
}

export function Bar({ value, max }: { value: number; max: number | null }) {
  if (!max) return <div className="bar"><i style={{ width: '0%' }} /></div>
  const p = Math.min(100, (100 * value) / max)
  return <div className={'bar' + (p >= 90 ? ' warn' : '')}><i style={{ width: p + '%' }} /></div>
}

export function Empty({ icon, children }: { icon?: string; children: ReactNode }) {
  return <div className="empty">{icon && <div className="big">{icon}</div>}{children}</div>
}

export function TrafficChart({ days }: { days: Day[] }) {
  const W = 720
  const H = 170
  const pad = { l: 46, r: 6, t: 10, b: 20 }
  const max = Math.max(1, ...days.map((d) => d.up + d.down))
  const bw = (W - pad.l - pad.r) / Math.max(1, days.length)
  const y = (v: number) => pad.t + (H - pad.t - pad.b) * (1 - v / max)
  const ticks = [0, 0.5, 1]
  return (
    <svg className="chart" viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none">
      {ticks.map((t) => (
        <g key={t}>
          <line className="grid" x1={pad.l} x2={W - pad.r} y1={y(max * t)} y2={y(max * t)} />
          <text className="lbl" x={pad.l - 6} y={y(max * t) + 3} textAnchor="end">{bytes(max * t)}</text>
        </g>
      ))}
      {days.map((d, i) => {
        const x = pad.l + i * bw + bw * 0.18
        const w = bw * 0.64
        const yd = y(d.down)
        const yt = y(d.up + d.down)
        const label = new Date(d.day * 1000).toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit' })
        return (
          <g key={d.day}>
            <title>{`${label}: ↓ ${bytes(d.down)}, ↑ ${bytes(d.up)}`}</title>
            <rect x={x} y={yd} width={w} height={Math.max(0, H - pad.b - yd)} rx={2} fill="var(--miku)" opacity={0.85} />
            <rect x={x} y={yt} width={w} height={Math.max(0, yd - yt)} rx={2} fill="var(--pink)" opacity={0.85} />
            {(i % 5 === 0 || i === days.length - 1) && <text className="lbl" x={x + w / 2} y={H - 5} textAnchor="middle">{label}</text>}
          </g>
        )
      })}
    </svg>
  )
}

export function ChartLegend() {
  return (
    <div className="legend">
      <span><i style={{ background: 'var(--miku)' }} />скачано</span>
      <span><i style={{ background: 'var(--pink)' }} />отдано</span>
    </div>
  )
}
