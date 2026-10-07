// Charts of the panel: plain SVG, measured to the container (no stretched text), with a hover
// layer on every plot. Colors are role tokens from styles.css (--s1…--s6, validated for the dark
// surface: adjacent pairs pass the CVD and normal-vision checks); text always uses text tokens.
import { ReactNode, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { bytes } from './format'

export const SERIES = ['var(--s1)', 'var(--s2)', 'var(--s3)', 'var(--s4)', 'var(--s5)', 'var(--s6)']

/** Width of an element, following resizes. */
function useWidth<T extends HTMLElement>(): [React.RefObject<T>, number] {
  const ref = useRef<T>(null)
  const [w, setW] = useState(0)
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    setW(el.clientWidth)
    const ro = new ResizeObserver(() => setW(el.clientWidth))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])
  return [ref as React.RefObject<T>, w]
}

/** Clean axis maximum and ticks: 0, step, 2·step… covering max. */
function niceTicks(max: number, count = 4, unit: 'bytes' | 'plain' = 'plain'): number[] {
  if (max <= 0) return [0, 1]
  if (unit === 'bytes') {
    // Steps in 1/2/5 × 1024^k so labels read 0 / 512 MB / 1 GB.
    const k = Math.max(0, Math.floor(Math.log(max) / Math.log(1024)))
    const base = 1024 ** k
    const raw = max / count / base
    const step = [0.1, 0.2, 0.25, 0.5, 1, 2, 2.5, 5, 10, 20, 25, 50, 100, 200, 250, 500].find((s) => s >= raw) ?? 1000
    const out: number[] = [0]
    while (out[out.length - 1] < max - 1e-9) out.push(out[out.length - 1] + step * base)
    return out.length < 2 ? [0, step * base] : out
  }
  const raw = max / count
  const mag = 10 ** Math.floor(Math.log10(raw))
  const step = [1, 2, 2.5, 5, 10].map((m) => m * mag).find((s) => s >= raw) ?? 10 * mag
  const out: number[] = [0]
  while (out[out.length - 1] < max - 1e-9) out.push(out[out.length - 1] + step)
  return out.length < 2 ? [0, step] : out
}

export function timeLabel(t: number, bucket: number, long = false, days = false): string {
  const d = new Date(t * 1000)
  if (days && !long) return d.toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit' })
  if (bucket < 86400) {
    const hm = d.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' })
    return long ? `${d.toLocaleDateString('ru-RU', { day: 'numeric', month: 'short' })}, ${hm}` : hm
  }
  return d.toLocaleDateString('ru-RU', long ? { day: 'numeric', month: 'long', weekday: 'short' } : { day: '2-digit', month: '2-digit' })
}

export interface Series {
  key: string
  label: string
  color: string
  values: (number | null)[] // null = nothing recorded (a gap, not a zero)
}

interface TimeChartProps {
  times: number[]
  bucket: number
  series: Series[]
  kind: 'area' | 'line' | 'bars'
  stacked?: boolean
  height?: number
  format?: (v: number) => string
  unit?: 'bytes' | 'plain'
  max?: number // fixed axis maximum, e.g. 100 for percent
  empty?: ReactNode
}

/** A time series chart with a crosshair tooltip. Stacked areas/bars sum series bottom-up. */
export function TimeChart({ times, bucket, series, kind, stacked, height = 220, format = String, unit = 'plain', max: fixedMax, empty }: TimeChartProps) {
  const [ref, width] = useWidth<HTMLDivElement>()
  const [hover, setHover] = useState<number | null>(null)
  const n = times.length
  const any = series.some((s) => s.values.some((v) => v != null && v > 0))
  const pad = { l: 58, r: 12, t: 12, b: 26 }
  const W = Math.max(width, 200)
  const H = height
  const iw = W - pad.l - pad.r
  const ih = H - pad.t - pad.b

  // Stack tops per index.
  const tops: number[][] = series.map(() => new Array(n).fill(0))
  const bases: number[][] = series.map(() => new Array(n).fill(0))
  for (let i = 0; i < n; i++) {
    let acc = 0
    series.forEach((s, k) => {
      const v = s.values[i] ?? 0
      bases[k][i] = stacked ? acc : 0
      acc = stacked ? acc + v : v
      tops[k][i] = stacked ? acc : v
    })
  }
  const dataMax = Math.max(0, ...tops.flat())
  const ticks = niceTicks(fixedMax ?? dataMax, 4, unit)
  const yMax = fixedMax ?? ticks[ticks.length - 1]
  const x = (i: number) => pad.l + (n <= 1 ? iw / 2 : kind === 'bars' ? (iw / n) * (i + 0.5) : (iw * i) / (n - 1))
  const y = (v: number) => pad.t + ih * (1 - v / (yMax || 1))
  // Hourly data over several days is labelled by date at local midnights; otherwise evenly.
  const multiDay = bucket < 86400 && n > 1 && times[n - 1] - times[0] > 2 * 86400
  const labelEvery = Math.max(1, Math.ceil(n / Math.max(2, Math.floor(iw / 64))))
  let labelAt: number[] = []
  if (multiDay) {
    const midnights = times.map((t, i) => (new Date(t * 1000).getHours() === 0 ? i : -1)).filter((i) => i >= 0)
    const every = Math.max(1, Math.ceil(midnights.length / Math.max(2, Math.floor(iw / 64))))
    labelAt = midnights.filter((_, k) => k % every === 0)
  } else {
    labelAt = times.map((_, i) => i).filter((i) => i % labelEvery === 0 && (kind === 'bars' || i < n - labelEvery / 2))
  }

  const onMove = (e: React.MouseEvent<SVGRectElement>) => {
    const r = (e.currentTarget as SVGRectElement).getBoundingClientRect()
    const px = e.clientX - r.left
    const i = kind === 'bars' ? Math.floor((px / r.width) * n) : Math.round((px / r.width) * (n - 1))
    setHover(Math.max(0, Math.min(n - 1, i)))
  }

  const linePath = (vals: number[], has: (i: number) => boolean) => {
    let d = ''
    let pen = false
    vals.forEach((v, i) => {
      if (!has(i)) {
        pen = false
        return
      }
      d += `${pen ? 'L' : 'M'}${x(i).toFixed(1)},${y(v).toFixed(1)}`
      pen = true
    })
    return d
  }

  return (
    <div ref={ref} className="tchart" style={{ height: H }}>
      {width > 0 && (
        <svg width={W} height={H} role="img">
          {ticks.map((t) => (
            <g key={t}>
              <line className="grid" x1={pad.l} x2={W - pad.r} y1={y(t)} y2={y(t)} />
              <text className="lbl" x={pad.l - 8} y={y(t) + 3.5} textAnchor="end">{format(t)}</text>
            </g>
          ))}
          {labelAt.map((i) => (
            <text key={times[i]} className="lbl" x={x(i)} y={H - 8} textAnchor="middle">{timeLabel(times[i], bucket, false, multiDay)}</text>
          ))}

          {kind === 'bars' && series.map((s, k) => {
            const bw = Math.min(24, Math.max(2, (iw / n) * 0.7))
            return times.map((_, i) => {
              const top = tops[k][i]
              const base = bases[k][i]
              if (top - base <= 0) return null
              const h = Math.max(1, y(base) - y(top) - (stacked && k > 0 ? 2 : 0))
              const isTop = k === series.length - 1 || !stacked || series.slice(k + 1).every((q) => !(q.values[i] ?? 0))
              return <path key={`${s.key}${i}`} d={barPath(x(i) - bw / 2, y(top), bw, h, isTop ? Math.min(4, bw / 2, h) : 0)}
                fill={s.color} opacity={hover == null || hover === i ? 1 : 0.55} />
            })
          })}

          {kind !== 'bars' && series.map((s, k) => {
            const has = (i: number) => s.values[i] != null
            const top = linePath(tops[k], has)
            if (!top) return null
            let area = ''
            if (kind === 'area') {
              // Each contiguous run becomes its own closed band between base and top.
              let run: number[] = []
              const flush = () => {
                if (run.length) {
                  area += 'M' + run.map((i) => `${x(i).toFixed(1)},${y(tops[k][i]).toFixed(1)}`).join('L') +
                    'L' + run.slice().reverse().map((i) => `${x(i).toFixed(1)},${y(bases[k][i]).toFixed(1)}`).join('L') + 'Z'
                }
                run = []
              }
              for (let i = 0; i < n; i++) has(i) ? run.push(i) : flush()
              flush()
            }
            return (
              <g key={s.key}>
                {area && <path d={area} fill={s.color} opacity={0.16} />}
                <path d={top} fill="none" stroke={s.color} strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />
              </g>
            )
          })}

          {hover != null && kind !== 'bars' && (
            <g>
              <line className="cross" x1={x(hover)} x2={x(hover)} y1={pad.t} y2={pad.t + ih} />
              {series.map((s, k) => s.values[hover] == null ? null : (
                <circle key={s.key} cx={x(hover)} cy={y(tops[k][hover])} r={4.5} fill={s.color} stroke="var(--surface)" strokeWidth={2} />
              ))}
            </g>
          )}
          <rect x={pad.l} y={pad.t} width={iw} height={ih} fill="transparent" onMouseMove={onMove} onMouseLeave={() => setHover(null)} />
        </svg>
      )}
      {!any && width > 0 && <div className="tchart-empty">{empty ?? 'Данных за этот период пока нет'}</div>}
      {hover != null && any && (
        <div className="tip" style={{ left: Math.min(Math.max(x(hover) + 12, 0), W - 190), top: 8 }}>
          <div className="tip-h">{timeLabel(times[hover], bucket, true)}</div>
          {series.slice().reverse().map((s) => (
            <div key={s.key} className="tip-r"><i style={{ background: s.color }} />{s.label}<b>{s.values[hover] == null ? '—' : format(s.values[hover]!)}</b></div>
          ))}
          {stacked && series.length > 1 && <div className="tip-r tip-sum">всего<b>{format(series.reduce((a, s) => a + (s.values[hover] ?? 0), 0))}</b></div>}
        </div>
      )}
    </div>
  )
}

/** A bar rounded at the data end (top), square at the baseline. */
function barPath(x: number, y: number, w: number, h: number, r: number) {
  if (r <= 0) return `M${x},${y}h${w}v${h}h${-w}Z`
  return `M${x},${y + h}V${y + r}Q${x},${y} ${x + r},${y}H${x + w - r}Q${x + w},${y} ${x + w},${y + r}V${y + h}Z`
}

export function Legend({ items }: { items: { label: string; color: string }[] }) {
  return (
    <div className="legend">
      {items.map((i) => <span key={i.label}><i style={{ background: i.color }} />{i.label}</span>)}
    </div>
  )
}

/** Horizontal bars: a ranked list with values at the tip. */
export function BarList({ rows, format = String, color = 'var(--s1)', onClick }: {
  rows: { key: string | number; label: ReactNode; value: number; sub?: ReactNode; color?: string }[]
  format?: (v: number) => string
  color?: string
  onClick?: (key: string | number) => void
}) {
  const max = Math.max(1, ...rows.map((r) => r.value))
  const total = rows.reduce((a, r) => a + r.value, 0)
  return (
    <div className="barlist">
      {rows.map((r) => (
        <div key={r.key} className={'barlist-row' + (onClick ? ' click' : '')} onClick={onClick ? () => onClick(r.key) : undefined}
          title={`${typeof r.label === 'string' ? r.label : ''} ${format(r.value)} (${total ? Math.round((100 * r.value) / total) : 0}%)`}>
          <div className="barlist-top">
            <span className="barlist-label">{r.label}{r.sub && <span className="muted small"> {r.sub}</span>}</span>
            <span className="barlist-val">{format(r.value)} <span className="muted">{total ? Math.round((100 * r.value) / total) : 0}%</span></span>
          </div>
          <div className="barlist-track"><div className="barlist-fill" style={{ width: `${(100 * r.value) / max}%`, background: r.color ?? color }} /></div>
        </div>
      ))}
    </div>
  )
}

/** Sequential teal ramp for magnitude: near-surface for zero, bright teal for the maximum. */
function ramp(t: number): string {
  const a = [0x1f, 0x26, 0x2c] // --surface-2
  const b = [0x5f, 0xd8, 0xcf] // --miku-2
  const k = Math.pow(Math.max(0, Math.min(1, t)), 0.75)
  return `rgb(${a.map((v, i) => Math.round(v + (b[i] - v) * k)).join(',')})`
}

const DAYS = ['пн', 'вт', 'ср', 'чт', 'пт', 'сб', 'вс']

/** Weekday × hour heatmap of traffic. */
export function Heatmap({ data }: { data: number[][] }) {
  const [hover, setHover] = useState<[number, number] | null>(null)
  const max = Math.max(1, ...data.flat())
  return (
    <div className="heatmap">
      <div className="heatmap-grid">
        <span />
        {Array.from({ length: 24 }, (_, h) => <span key={h} className="hm-h">{h % 3 === 0 ? h : ''}</span>)}
        {data.map((row, d) => (
          <FragmentRow key={d} label={DAYS[d]}>
            {row.map((v, h) => (
              <span key={h} className={'hm-c' + (hover && hover[0] === d && hover[1] === h ? ' on' : '')} style={{ background: v ? ramp(v / max) : 'var(--surface-2)' }}
                onMouseEnter={() => setHover([d, h])} onMouseLeave={() => setHover(null)} />
            ))}
          </FragmentRow>
        ))}
      </div>
      <div className="heatmap-foot">
        <span className="small text-2">{hover ? <>{DAYS[hover[0]]}, {String(hover[1]).padStart(2, '0')}:00–{String(hover[1] + 1).padStart(2, '0')}:00 — <b>{bytes(data[hover[0]][hover[1]])}</b></> : 'Наведите на клетку'}</span>
        <span className="hm-scale"><span className="small muted">меньше</span>{[0.05, 0.25, 0.5, 0.75, 1].map((t) => <i key={t} style={{ background: ramp(t) }} />)}<span className="small muted">больше</span></span>
      </div>
    </div>
  )
}

function FragmentRow({ label, children }: { label: string; children: ReactNode }) {
  return <><span className="hm-d">{label}</span>{children}</>
}

/** Tiny trend line for a stat tile. */
export function Sparkline({ values, color = 'var(--s1)' }: { values: number[]; color?: string }) {
  const [ref, w] = useWidth<HTMLDivElement>()
  const H = 34
  const max = Math.max(1, ...values)
  const n = values.length
  const pts = values.map((v, i) => `${n <= 1 ? 0 : ((w - 4) * i) / (n - 1) + 2},${H - 3 - (H - 6) * (v / max)}`)
  return (
    <div ref={ref} className="spark">
      {w > 0 && n > 1 && (
        <svg width={w} height={H}>
          <path d={`M${pts.join('L')}L${w - 2},${H}L2,${H}Z`} fill={color} opacity={0.14} />
          <path d={`M${pts.join('L')}`} fill="none" stroke={color} strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />
          <circle cx={pts[n - 1].split(',')[0]} cy={pts[n - 1].split(',')[1]} r={3.5} fill={color} stroke="var(--surface)" strokeWidth={2} />
        </svg>
      )}
    </div>
  )
}

/** Change vs the previous period: ▲ 12% / ▼ 3% / no data. */
export function Delta({ now, prev, upIsGood = true }: { now: number; prev: number; upIsGood?: boolean }) {
  if (!prev) return <span className="delta muted">нет данных за прошлый период</span>
  // A barely recorded previous period (the panel is new) gives absurd percentages.
  if (prev * 20 < now) return <span className="delta muted">мало данных за прошлый период</span>
  const pct = ((now - prev) / prev) * 100
  const up = pct >= 0
  const good = up === upIsGood
  return (
    <span className={'delta ' + (Math.abs(pct) < 0.5 ? 'muted' : good ? 'good' : 'bad')}>
      {up ? '▲' : '▼'} {Math.abs(pct) < 10 ? Math.abs(pct).toFixed(1) : Math.round(Math.abs(pct))}% <span className="muted">к прошлому периоду</span>
    </span>
  )
}

/** Segmented control for a small set of options. */
export function Segmented<T extends string>({ value, options, onChange }: { value: T; options: { value: T; label: string }[]; onChange: (v: T) => void }) {
  return (
    <div className="segmented" role="tablist">
      {options.map((o) => (
        <button key={o.value} role="tab" aria-selected={o.value === value} className={o.value === value ? 'on' : ''} onClick={() => onChange(o.value)}>{o.label}</button>
      ))}
    </div>
  )
}

/** A stacked share bar (e.g. users by status) with a labelled legend underneath. */
export function ShareBar({ parts }: { parts: { label: string; value: number; color: string }[] }) {
  const total = parts.reduce((a, p) => a + p.value, 0)
  const [hover, setHover] = useState<number | null>(null)
  useEffect(() => setHover(null), [total])
  return (
    <div>
      <div className="sharebar">
        {parts.map((p, i) => p.value > 0 && (
          <div key={p.label} style={{ flexGrow: p.value, background: p.color, opacity: hover == null || hover === i ? 1 : 0.5 }}
            onMouseEnter={() => setHover(i)} onMouseLeave={() => setHover(null)} title={`${p.label}: ${p.value}`} />
        ))}
      </div>
      <div className="sharebar-legend">
        {parts.map((p) => (
          <div key={p.label}><i style={{ background: p.color }} /><span className="text-2">{p.label}</span><b>{p.value}</b><span className="muted small">{total ? Math.round((100 * p.value) / total) : 0}%</span></div>
        ))}
      </div>
    </div>
  )
}

export const fmtBytes = (v: number) => bytes(v)
