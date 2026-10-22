import { useState } from 'react'
import { get } from '../api'
import { Legend, Segmented, TimeChart } from '../charts'
import { GiB } from '../format'
import { Empty, Loading, useLoad } from '../ui'

// Точка: t, CPU %, CPU max %, steal %, память (байты), rx, tx, rx max, tx max (байт/с).
type Point = [number, number, number, number, number, number, number, number, number]

interface LoadT {
  range: string
  bucket: number
  from: number
  to: number
  nodes: { id: number; code: string; name: string; flag: string; memTotal: number; points: Point[] }[]
}

type Range = '24h' | '7d' | '30d' | '90d'
const ranges: { value: Range; label: string }[] = [
  { value: '24h', label: '24 часа' }, { value: '7d', label: '7 дней' }, { value: '30d', label: '30 дней' }, { value: '90d', label: '90 дней' },
]
const bucketWord: Record<number, string> = { 300: '5 минут', 1800: '30 минут', 10800: '3 часа', 43200: '12 часов' }

function savedRange(): Range {
  try {
    const v = localStorage.getItem('vynel.load.range')
    if (v && ranges.some((r) => r.value === v)) return v as Range
  } catch { /* хранилище недоступно */ }
  return '24h'
}

const pct = (v: number) => `${Math.round(v)}%`
const pct1 = (v: number) => `${v < 10 ? v.toFixed(1) : Math.round(v)}%`
const gb = (v: number) => `${(v / GiB).toFixed(v < 10 * GiB ? 1 : 0)} ГБ`
// Канал — в битах в секунду, как его продают хостеры.
export function bits(bytesPerSec: number): string {
  const b = bytesPerSec * 8
  if (b >= 1e9) return `${(b / 1e9).toFixed(1)} Гбит/с`
  if (b >= 1e6) return `${(b / 1e6).toFixed(b >= 1e8 ? 0 : 1)} Мбит/с`
  if (b >= 1e3) return `${Math.round(b / 1e3)} Кбит/с`
  return `${Math.round(b)} бит/с`
}

// ServerLoad — нагрузка нод во времени: процессор, steal, память в гигабайтах, канал.
// Точки — средние за 5 минут, панель хранит их 90 дней; страница обновляется раз в 5 минут.
export function ServerLoad() {
  const [range, setRangeState] = useState<Range>(savedRange)
  const setRange = (r: Range) => {
    setRangeState(r)
    try { localStorage.setItem('vynel.load.range', r) } catch { /* ignore */ }
  }
  const { data, error } = useLoad(() => get<LoadT>(`stats/load?range=${range}`), [range], 5 * 60 * 1000)
  return (
    <>
      <div className="stats-head">
        <div className="muted small">{data ? <>Средние за {bucketWord[data.bucket] || `${data.bucket / 60} мин`}; обновляется раз в 5 минут, хранится 90 дней</> : ' '}</div>
        <Segmented value={range} options={ranges} onChange={setRange} />
      </div>
      {!data || data.range !== range ? <Loading error={error} /> : data.nodes.length === 0 ? <div className="card"><Empty>Нод пока нет</Empty></div> : (
        <div className="cards">
          {data.nodes.map((n) => <NodeLoadCard key={n.id} n={n} d={data} />)}
        </div>
      )}
    </>
  )
}

function NodeLoadCard({ n, d }: { n: LoadT['nodes'][number]; d: LoadT }) {
  // Ровная ось времени: где нода молчала, на графике пропуск, а не линия через провал.
  const times: number[] = []
  for (let t = d.from; t < d.to; t += d.bucket) times.push(t)
  const at = new Map(n.points.map((p) => [p[0], p]))
  const col = (i: number, k = 1) => times.map((t) => { const p = at.get(t); return p ? p[i] * k : null })
  const last = n.points[n.points.length - 1]
  const empty = n.points.length === 0
  return (
    <div className="card">
      <div className="card-head">
        <div>
          <h3><span style={{ fontSize: 18 }}>{n.flag}</span> {n.name} <span className="muted small">{n.code}</span></h3>
          {last ? (
            <div className="card-sub load-now">
              <span>ЦП {pct(last[1])}</span>
              {last[3] >= 0.5 && <span className={last[3] >= 5 ? 'pink-text' : ''}>steal {last[3].toFixed(1)}%</span>}
              <span>ОЗУ {gb(last[4])}{n.memTotal ? ` из ${gb(n.memTotal)}` : ''}</span>
              <span>↓ {bits(last[5])} · ↑ {bits(last[6])}</span>
            </div>
          ) : <div className="card-sub">данных за период нет</div>}
        </div>
      </div>
      {!empty && (
        <div className="card-pad small-multiples load-grid">
          <div>
            <div className="row between"><div className="sm-title">Процессор</div>
              <Legend items={[{ label: 'в среднем', color: 'var(--s1)' }, { label: 'пик', color: 'var(--s4)' }]} /></div>
            <TimeChart times={times} bucket={d.bucket} kind="line" max={100} format={pct} height={160}
              series={[
                { key: 'max', label: 'пик', color: 'var(--s4)', values: col(2) },
                { key: 'avg', label: 'в среднем', color: 'var(--s1)', values: col(1) },
              ]} />
          </div>
          <div>
            <div className="sm-title">Steal <span className="muted small">— время, которое хостер отдал другим машинам</span></div>
            <TimeChart times={times} bucket={d.bucket} kind="line" format={pct1} height={160}
              series={[{ key: 'steal', label: 'steal', color: 'var(--s2)', values: col(3) }]} />
          </div>
          <div>
            <div className="sm-title">Оперативная память{n.memTotal ? <span className="muted small"> — всего {gb(n.memTotal)}</span> : null}</div>
            <TimeChart times={times} bucket={d.bucket} kind="area" unit="bytes" max={n.memTotal || undefined} format={gb} height={160}
              series={[{ key: 'mem', label: 'занято', color: 'var(--s3)', values: col(4) }]} />
          </div>
          <div>
            <div className="row between"><div className="sm-title">Канал</div>
              <Legend items={[{ label: 'входящий', color: 'var(--s1)' }, { label: 'исходящий', color: 'var(--s2)' }]} /></div>
            <TimeChart times={times} bucket={d.bucket} kind="line" format={bits} height={160}
              series={[
                { key: 'rx', label: 'входящий', color: 'var(--s1)', values: col(5) },
                { key: 'tx', label: 'исходящий', color: 'var(--s2)', values: col(6) },
              ]} />
          </div>
        </div>
      )}
    </div>
  )
}
