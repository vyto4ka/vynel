import { createContext, ReactNode, useCallback, useContext, useEffect, useId, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Day } from './api'
import { fmtBytes, TimeChart } from './charts'
import { bytes } from './format'
import { Link, useLinkHandlers } from './jsonlink'
import { t } from './i18n'

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
  xray: <><path d="M12 2.5 20 7v10l-8 4.5L4 17V7z" /><path d="m9 9.5 6 5M15 9.5l-6 5" /></>,
  check: <path d="m5 12.5 4.5 4.5L19 7.5" />,
  chart: <><path d="M4 4v16h16" /><path d="m7 14 4-4 3 3 5-6" /></>,
  wallet: <><rect x="3" y="6" width="18" height="14" rx="2.5" /><path d="M3 10h18M16 15h2M6 6l9-3 2 3" /></>,
  external: <><path d="M14 4h6v6M20 4l-9 9" /><path d="M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5" /></>,
  more: <><circle cx="12" cy="5" r="1.3" /><circle cx="12" cy="12" r="1.3" /><circle cx="12" cy="19" r="1.3" /></>,
  power: <><path d="M12 3v8" /><path d="M6.3 7.3a8 8 0 1 0 11.4 0" /></>,
  plug: <><path d="M9 3v5M15 3v5" /><path d="M6 8h12v3a6 6 0 0 1-12 0z" /><path d="M12 17v4" /></>,
  sub: <><rect x="6" y="2.5" width="12" height="19" rx="2.5" /><path d="M10 6h4M9.5 11h5M9.5 14.5h5M11 18h2" /></>,
  move: <><path d="M5 12h14M13 6l6 6-6 6" /><path d="M11 4H6a1 1 0 0 0-1 1v14a1 1 0 0 0 1 1h5" /></>,
  download: <><path d="M12 4v11M7 11l5 5 5-5" /><path d="M5 20h14" /></>,
  upload: <><path d="M12 16V5M7 9l5-5 5 5" /><path d="M5 20h14" /></>,
  help: <><circle cx="12" cy="12" r="9" /><path d="M9.2 9a2.9 2.9 0 0 1 5.7.9c0 1.9-2.9 2.4-2.9 4.1" /><path d="M12 17.5h.01" /></>,
  chevron: <path d="m6 9 6 6 6-6" />,
  grip: <><circle cx="9" cy="6" r="1.2" /><circle cx="15" cy="6" r="1.2" /><circle cx="9" cy="12" r="1.2" /><circle cx="15" cy="12" r="1.2" /><circle cx="9" cy="18" r="1.2" /><circle cx="15" cy="18" r="1.2" /></>,
  qr: <><rect x="3" y="3" width="7" height="7" rx="1" /><rect x="14" y="3" width="7" height="7" rx="1" /><rect x="3" y="14" width="7" height="7" rx="1" /><path d="M14 14h3v3h-3zM18 18h3v3h-3zM14 20h2" /></>,
  fullscreen: <path d="M8 3H5a2 2 0 0 0-2 2v3m18 0V5a2 2 0 0 0-2-2h-3m0 18h3a2 2 0 0 0 2-2v-3M3 16v3a2 2 0 0 0 2 2h3" />,
  shield: <path d="M12 3 5 6v6c0 4.5 3 7.7 7 9 4-1.3 7-4.5 7-9V6l-7-3z" />,
}

export function Icon({ name, size }: { name: string; size?: number }) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} fill="none" stroke="currentColor" strokeWidth={1.75} strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      {paths[name]}
    </svg>
  )
}

// Логотип vynel — тот же, что в README (docs/images/logo.svg).
export function Logo({ size = 36 }: { size?: number }) {
  const id = useId()
  return (
    <svg className="brand-logo" width={size} height={size} viewBox="0 0 96 96" role="img" aria-label="vynel">
      <defs>
        <linearGradient id={id} x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor="#39c5bb" />
          <stop offset="1" stopColor="#ff5fa2" />
        </linearGradient>
      </defs>
      <rect x="4" y="4" width="88" height="88" rx="24" fill={`url(#${id})`} />
      <path d="M30 34 L48 66 L66 34" fill="none" stroke="var(--inverse-on-surface)" strokeWidth="9" strokeLinecap="round" strokeLinejoin="round" />
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

export function useLoad<T>(fn: (signal?: AbortSignal) => Promise<T>, deps: unknown[] = [], refreshMs = 0) {
  const [loading, setLoading] = useState(true)
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<string | null>(null)
  const requestId = useRef(0)
  const controller = useRef<AbortController | null>(null)
  const fnRef = useRef(fn)
  fnRef.current = fn
  // A newer load aborts the one in flight: a slow answer to an old filter must not overwrite
  // the data the admin is looking at now.
  const reload = useCallback(async () => {
    const id = ++requestId.current
    controller.current?.abort()
    const ac = new AbortController()
    controller.current = ac
    setLoading(true)
    setError(null)
    try {
      const result = await fnRef.current(ac.signal)
      if (id !== requestId.current) return
      setData(result)
      setError(null)
    } catch (e: any) {
      if (id !== requestId.current || ac.signal.aborted) return
      setError(e?.message || String(e))
    } finally {
      if (id === requestId.current) setLoading(false)
    }
  }, [])
  useEffect(() => {
    reload()
    if (!refreshMs) return () => { requestId.current++; controller.current?.abort() }
    const t = setInterval(() => {
      if (!document.hidden) reload()
    }, refreshMs)
    return () => { requestId.current++; controller.current?.abort(); clearInterval(t) }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)
  return { data, error, loading, reload, setData }
}

export function Loading({ error }: { error?: string | null }) {
  if (error) return <div className="alert pink">{error}</div>
  return <div className="empty">{t('common.loading')}</div>
}

// ---------- оверлеи и горячие клавиши ----------

const escapeStack: Array<() => void> = []
let escapeListenerAttached = false

function handleGlobalEscape(e: KeyboardEvent) {
  if (e.key !== 'Escape' || escapeStack.length === 0) return
  e.preventDefault()
  e.stopPropagation()
  const top = escapeStack[escapeStack.length - 1]
  top()
}

// useEscape регистрирует обработчик Escape в общем стеке: закрывается только верхний оверлей.
export function useEscape(active: boolean, onClose: () => void) {
  const ref = useRef(onClose)
  ref.current = onClose
  useEffect(() => {
    if (!active) return
    if (!escapeListenerAttached) {
      window.addEventListener('keydown', handleGlobalEscape, true)
      escapeListenerAttached = true
    }
    const fn = () => ref.current()
    escapeStack.push(fn)
    return () => {
      const idx = escapeStack.lastIndexOf(fn)
      if (idx >= 0) escapeStack.splice(idx, 1)
    }
  }, [active])
}

// isEditable — фокус в поле ввода / редакторе, где глобальные хоткеи не должны мешать.
export function isEditable(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  if (target.isContentEditable) return true
  const tag = target.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || !!target.closest('.cm-editor')
}

// useRangeSelection — выделение элементов списка с поддержкой Shift+Click (диапазон).
export function useRangeSelection<T>(items: T[], selected: T[], setSelected: (next: T[] | ((prev: T[]) => T[])) => void) {
  const anchorRef = useRef<T | null>(null)
  const toggleItem = useCallback((id: T, shiftKey: boolean) => {
    const idx = items.indexOf(id)
    if (shiftKey && anchorRef.current !== null && idx >= 0) {
      const aIdx = items.indexOf(anchorRef.current)
      if (aIdx >= 0) {
        const [from, to] = aIdx <= idx ? [aIdx, idx] : [idx, aIdx]
        const range = items.slice(from, to + 1)
        const anchorSelected = selected.includes(anchorRef.current)
        setSelected((prev) => {
          const set = new Set(prev)
          if (anchorSelected) range.forEach((x) => set.add(x))
          else range.forEach((x) => set.delete(x))
          return Array.from(set)
        })
        return
      }
    }
    anchorRef.current = id
    setSelected((prev) => (prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]))
  }, [items, selected, setSelected])

  const resetAnchor = useCallback(() => { anchorRef.current = null }, [])
  return { toggleItem, resetAnchor }
}

export function Modal({ title, children, footer, onClose, wide, xl }: { title: ReactNode; children: ReactNode; footer?: ReactNode; onClose: () => void; wide?: boolean; xl?: boolean }) {
  useEscape(true, onClose)
  return (
    <div className="modal-back" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className={'modal' + (wide ? ' wide' : '') + (xl ? ' xl' : '')} role="dialog">
        <div className="modal-head">
          <h2>{title}</h2>
          <button className="btn ghost icon sm" onClick={onClose} aria-label={t('common.close')}><Icon name="close" /></button>
        </div>
        <div className="modal-body">{children}</div>
        {footer && <div className="modal-foot">{footer}</div>}
      </div>
    </div>
  )
}

export function Drawer({ title, children, onClose, actions }: { title: ReactNode; children: ReactNode; onClose: () => void; actions?: ReactNode }) {
  useEscape(true, onClose)
  return (
    <>
      <div className="drawer-back" onClick={onClose} />
      <aside className="drawer">
        <div className="drawer-head">
          <h2 className="grow ellipsis">{title}</h2>
          {actions}
          <button className="btn ghost icon sm" onClick={onClose} aria-label={t('common.close')}><Icon name="close" /></button>
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
  const ask = useCallback((text: ReactNode, ok = t('common.yes'), danger = true) => new Promise<boolean>((resolve) => setSt({ text, ok, danger, resolve })), [])
  const close = (v: boolean) => {
    st?.resolve(v)
    setSt(null)
  }
  return (
    <ConfirmCtx.Provider value={ask}>
      {children}
      {st && (
        <Modal title={t('ui.confirmation')} onClose={() => close(false)}
          footer={<><button className="btn ghost" onClick={() => close(false)}>{t('common.cancel')}</button>
            <button className={'btn ' + (st.danger ? 'pink' : 'primary')} autoFocus onClick={() => close(true)}>{st.ok}</button></>}>
          <div className="text-2">{st.text}</div>
        </Modal>
      )}
    </ConfirmCtx.Provider>
  )
}

// ---------- меню (портал в body, не обрезается таблицами и drawer) ----------

export type MenuItem = { label: ReactNode; icon?: string; danger?: boolean; disabled?: boolean; onClick: () => void } | 'divider'

function useMenuPos(open: boolean, anchor: React.RefObject<HTMLElement | null>, pop: React.RefObject<HTMLDivElement | null>) {
  const [pos, setPos] = useState<{ left: number; top: number } | null>(null)
  useLayoutEffect(() => {
    if (!open || !anchor.current) return setPos(null)
    const place = () => {
      if (!anchor.current) return
      const r = anchor.current.getBoundingClientRect()
      const w = pop.current?.offsetWidth || 220
      const h = pop.current?.offsetHeight || 200
      const left = Math.max(8, Math.min(r.right - w, window.innerWidth - w - 8))
      const up = r.bottom + 6 + h > window.innerHeight - 8 && r.top - 6 - h > 8
      const top = up ? Math.max(8, r.top - 6 - h) : Math.min(r.bottom + 6, window.innerHeight - h - 8)
      setPos({ left, top })
    }
    place()
    window.addEventListener('resize', place)
    window.addEventListener('scroll', place, true)
    return () => {
      window.removeEventListener('resize', place)
      window.removeEventListener('scroll', place, true)
    }
  }, [open, anchor, pop])
  return pos
}

// Меню по кнопке: рендерится в портал, закрывается кликом мимо, Esc и после выбора пункта.
export function Menu({ title, items, icon = 'more' }: { title: string; items: MenuItem[]; icon?: string }) {
  const [open, setOpen] = useState(false)
  const btn = useRef<HTMLButtonElement>(null)
  const pop = useRef<HTMLDivElement>(null)
  const pos = useMenuPos(open, btn, pop)
  useEscape(open, () => setOpen(false))
  useEffect(() => {
    if (!open) return
    const down = (e: MouseEvent) => {
      if (!btn.current?.contains(e.target as Node) && !pop.current?.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', down)
    return () => document.removeEventListener('mousedown', down)
  }, [open])
  return (
    <div className="menu-wrap">
      <button type="button" ref={btn} className={'btn ghost icon sm' + (open ? ' on' : '')} title={title} aria-haspopup="menu" aria-expanded={open}
        onClick={(e) => { e.stopPropagation(); setOpen((o) => !o) }}><Icon name={icon} /></button>
      {open && createPortal(
        <div ref={pop} className="menu portal-menu" role="menu"
          style={pos ? { left: pos.left, top: pos.top } : { left: -9999, top: -9999, visibility: 'hidden' }}
          onClick={(e) => e.stopPropagation()}>
          {items.map((it, i) => it === 'divider' ? <div key={i} className="menu-divider" /> : (
            <button key={i} type="button" role="menuitem" className={'menu-item' + (it.danger ? ' danger' : '')} disabled={it.disabled}
              onClick={() => { setOpen(false); it.onClick() }}>
              {it.icon && <Icon name={it.icon} />}<span>{it.label}</span>
            </button>
          ))}
        </div>,
        document.body,
      )}
    </div>
  )
}

// SideTooltip — мгновенный портальный тултип справа от иконки в свёрнутом сайдбаре.
export function SideTooltip({ label, active, children }: { label: string; active: boolean; children: ReactNode }) {
  const [pos, setPos] = useState<{ left: number; top: number } | null>(null)
  const wrap = useRef<HTMLDivElement>(null)
  const show = () => {
    if (!active || !wrap.current) return
    const r = wrap.current.getBoundingClientRect()
    setPos({ left: r.right + 10, top: r.top + r.height / 2 })
  }
  const hide = () => setPos(null)
  useEffect(() => { if (!active) setPos(null) }, [active])
  return (
    <div ref={wrap} className="side-tip-wrap" onMouseEnter={show} onMouseLeave={hide} onFocus={show} onBlur={hide}>
      {children}
      {active && pos && createPortal(
        <div className="side-tooltip" role="tooltip" style={{ left: pos.left, top: pos.top }}>{label}</div>,
        document.body,
      )}
    </div>
  )
}

// ---------- мелочи ----------

export function Badge({ color, children, dot }: { color?: string; children: ReactNode; dot?: boolean }) {
  return <span className={'badge ' + (color || '')}>{dot && <span className="dot" />}{children}</span>
}

// Info — тихая подсказка по наведению/клику без навязчивого кружка.
export function Info({ children, label = t('ui.what_is_this') }: { children: ReactNode; label?: string }) {
  const [open, setOpen] = useState<'hover' | 'click' | null>(null)
  const btn = useRef<HTMLButtonElement>(null)
  const pop = useRef<HTMLDivElement>(null)
  const [pos, setPos] = useState<{ left: number; top: number; up: boolean } | null>(null)
  useEscape(open === 'click', () => setOpen(null))
  useLayoutEffect(() => {
    if (!open || !btn.current) return setPos(null)
    const r = btn.current.getBoundingClientRect()
    const w = Math.min(320, window.innerWidth - 16)
    const h = pop.current?.offsetHeight || 0
    const up = r.bottom + 8 + h > window.innerHeight && r.top - 8 - h > 0
    setPos({ left: Math.max(8, Math.min(r.left + r.width / 2 - w / 2, window.innerWidth - w - 8)), top: up ? r.top - 8 - h : r.bottom + 8, up })
  }, [open])
  useEffect(() => {
    if (open !== 'click') return
    const close = (e: Event) => {
      if (!btn.current?.contains(e.target as Node) && !pop.current?.contains(e.target as Node)) setOpen(null)
    }
    document.addEventListener('pointerdown', close)
    window.addEventListener('scroll', close as any, true)
    return () => {
      document.removeEventListener('pointerdown', close)
      window.removeEventListener('scroll', close as any, true)
    }
  }, [open])
  return (
    <>
      <button type="button" ref={btn} className={'info-btn' + (open ? ' on' : '')} aria-label={label} aria-expanded={!!open}
        onClick={(e) => { e.preventDefault(); e.stopPropagation(); setOpen(open === 'click' ? null : 'click') }}
        onMouseEnter={() => { if (!open && matchMedia('(hover: hover)').matches) setOpen('hover') }}
        onMouseLeave={() => { if (open === 'hover') setOpen(null) }}>i</button>
      {open && createPortal(
        <div ref={pop} className="info-pop" role="tooltip" style={pos ? { left: pos.left, top: pos.top } : { visibility: 'hidden', left: 0, top: 0 }}
          onClick={(e) => e.stopPropagation()}>{children}</div>,
        document.body,
      )}
    </>
  )
}

// PageInfo — спокойная строка контекста без навязчивых кнопок.
export function PageInfo({ children }: { children: ReactNode }) {
  return <div className="grow page-info"><Info label={t('ui.about_this_page')}>{children}</Info></div>
}

// Tabs — чистые линейные вкладки с плавным нижним подчёркиванием.
export type TabItem = { id: string; label: ReactNode; count?: number }
export function Tabs({ items, value, onChange, right }: { items: TabItem[]; value: string; onChange: (id: string) => void; right?: ReactNode }) {
  return (
    <div className="tabs-bar" role="tablist">
      <div className="tabs-list">
        {items.map((it) => (
          <button key={it.id} type="button" role="tab" aria-selected={value === it.id}
            className={'tab-btn' + (value === it.id ? ' on' : '')}
            onClick={() => onChange(it.id)}>
            <span>{it.label}</span>
            {it.count !== undefined && <span className="tab-count">{it.count}</span>}
          </button>
        ))}
      </div>
      {right && <div className="tabs-right">{right}</div>}
    </div>
  )
}

// SplitButton — главное действие + выпадающий список дополнительных вариантов (в портале).
export function SplitButton({ label, onClick, items, primary, disabled }: {
  label: ReactNode
  onClick: () => void
  items: { label: ReactNode; onClick: () => void }[]
  primary?: boolean
  disabled?: boolean
}) {
  const [open, setOpen] = useState(false)
  const box = useRef<HTMLDivElement>(null)
  const pop = useRef<HTMLDivElement>(null)
  const pos = useMenuPos(open, box, pop)
  useEscape(open, () => setOpen(false))
  useEffect(() => {
    if (!open) return
    const down = (e: MouseEvent) => {
      if (!box.current?.contains(e.target as Node) && !pop.current?.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', down)
    return () => document.removeEventListener('mousedown', down)
  }, [open])
  const cls = primary ? 'btn primary sm' : 'btn sm'
  return (
    <div className="split-btn" ref={box}>
      <button type="button" className={cls + ' split-main'} disabled={disabled} onClick={onClick}>{label}</button>
      <button type="button" className={cls + ' split-arrow'} disabled={disabled} aria-haspopup="menu" aria-expanded={open}
        onClick={() => setOpen((o) => !o)}><Icon name="chevron" size={14} /></button>
      {open && createPortal(
        <div ref={pop} className="menu portal-menu" role="menu"
          style={pos ? { left: pos.left, top: pos.top } : { left: -9999, top: -9999, visibility: 'hidden' }}>
          {items.map((it, i) => (
            <button key={i} type="button" role="menuitem" className="menu-item"
              onClick={() => { setOpen(false); it.onClick() }}>{it.label}</button>
          ))}
        </div>,
        document.body,
      )}
    </div>
  )
}

export function Field({ label, help, info, link, children }: { label: ReactNode; help?: ReactNode; info?: ReactNode; link?: Link; children: ReactNode }) {
  const h = useLinkHandlers(link)
  return (
    <div className="field" {...h}>
      <label>{label}{info && <Info>{info}</Info>}</label>
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
      <button className="btn sm" onClick={() => copyText(value).then(() => toast(t('ui.copied')))}>
        <Icon name="copy" />{' '}{t('ui.copy')}</button>
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

export function Empty({ icon, action, children }: { icon?: string; action?: ReactNode; children: ReactNode }) {
  return <div className="empty">{icon && <div className="big">{icon}</div>}{children}{action && <div style={{ marginTop: 14 }}>{action}</div>}</div>
}

export function TrafficChart({ days }: { days: Day[] }) {
  return (
    <TimeChart times={days.map((d) => d.day)} bucket={86400} kind="bars" stacked unit="bytes" format={fmtBytes} height={190}
      series={[
        { key: 'down', label: t('common.downloaded'), color: 'var(--s1)', values: days.map((d) => d.down) },
        { key: 'up', label: t('common.uploaded'), color: 'var(--s2)', values: days.map((d) => d.up) },
      ]} />
  )
}

export function ChartLegend() {
  return (
    <div className="legend">
      <span><i style={{ background: 'var(--s1)' }} />{t('common.downloaded')}</span>
      <span><i style={{ background: 'var(--s2)' }} />{t('common.uploaded')}</span>
    </div>
  )
}
