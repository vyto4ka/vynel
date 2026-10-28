import { useState } from 'react'
import { get } from '../api'
import { Legend, Segmented, SERIES, TimeChart } from '../charts'
import { GiB } from '../format'
import { Empty, Info, Loading, useLoad } from '../ui'
import { t, Trans } from '../i18n'

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
  { value: '24h', label: t('common.n24_hours') }, { value: '7d', label: t('common.n7_days') }, { value: '30d', label: t('common.n30_days') }, { value: '90d', label: t('common.n90_days') },
]
const bucketWord: Record<number, string> = { 300: t('serverload.n5_minutes'), 1800: t('serverload.n30_minutes'), 10800: t('serverload.n3_hours'), 43200: t('serverload.n12_hours') }

function savedRange(): Range {
  try {
    const v = localStorage.getItem('vynel.load.range')
    if (v && ranges.some((r) => r.value === v)) return v as Range
  } catch { /* хранилище недоступно */ }
  return '24h'
}

const pct = (v: number) => `${Math.round(v)}%`
const pct1 = (v: number) => `${v < 10 ? v.toFixed(1) : Math.round(v)}%`
const gb = (v: number) => t('serverload.gb', { toFixed: (v / GiB).toFixed(v < 10 * GiB ? 1 : 0) })
// Канал — в битах в секунду, как его продают хостеры.
export function bits(bytesPerSec: number): string {
  const b = bytesPerSec * 8
  if (b >= 1e9) return t('serverload.gbit_s', { toFixed: (b / 1e9).toFixed(1) })
  if (b >= 1e6) return t('serverload.mbit_s', { toFixed: (b / 1e6).toFixed(b >= 1e8 ? 0 : 1) })
  if (b >= 1e3) return t('serverload.kbit_s', { round: Math.round(b / 1e3) })
  return t('serverload.bit_s', { round: Math.round(b) })
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
        <div className="muted small">{data ? <>{t('serverload.averages_per')}{' '}{bucketWord[data.bucket] || t('serverload.min', { v: data.bucket / 60 })} <Info>{t('serverload.a_node_sends_measurements_every')}</Info></> : ' '}</div>
        <Segmented value={range} options={ranges} onChange={setRange} />
      </div>
      {!data || data.range !== range ? <Loading error={error} /> : data.nodes.length === 0 ? <div className="card"><Empty>{t('common.no_nodes_yet')}</Empty></div> : (
        <div className="cards">
          {data.nodes.length > 1 && <AllNodesCard d={data} />}
          {data.nodes.map((n) => <NodeLoadCard key={n.id} n={n} d={data} />)}
        </div>
      )}
    </>
  )
}

// Ровная ось времени периода: где нода молчала, на графике пропуск, а не линия через провал.
function axis(d: LoadT): number[] {
  const times: number[] = []
  for (let t = d.from; t < d.to; t += d.bucket) times.push(t)
  return times
}

// AllNodesCard — все ноды на одних графиках, каждая своим цветом: видно, какая нагружена сильнее.
function AllNodesCard({ d }: { d: LoadT }) {
  const times = axis(d)
  const nodes = [...d.nodes].sort((a, b) => a.id - b.id)
  const color = (i: number) => SERIES[i % SERIES.length]
  const series = (f: (p: Point) => number) => nodes.map((n, i) => {
    const at = new Map(n.points.map((p) => [p[0], p]))
    return { key: String(n.id), label: `${n.flag} ${n.name}`, color: color(i), values: times.map((t) => { const p = at.get(t); return p ? f(p) : null }) }
  })
  return (
    <div className="card">
      <div className="card-head">
        <div><h3>{t('serverload.all_nodes')}</h3><div className="card-sub">{t('serverload.overlaid')}</div></div>
        <Legend items={nodes.map((n, i) => ({ label: `${n.flag} ${n.code}`, color: color(i) }))} />
      </div>
      <div className="card-pad small-multiples load-grid">
        <div>
          <div className="sm-title">{t('serverload.processor_average')}</div>
          <TimeChart times={times} bucket={d.bucket} kind="line" max={100} format={pct} height={170} series={series((p) => p[1])} />
        </div>
        <div>
          <div className="sm-title">Steal<Info>{t('serverload.cpu_time_the_hosting_provider')}</Info></div>
          <TimeChart times={times} bucket={d.bucket} kind="line" format={pct1} height={170} series={series((p) => p[3])} />
        </div>
        <div>
          <div className="sm-title">{t('serverload.memory')}</div>
          <TimeChart times={times} bucket={d.bucket} kind="line" unit="bytes" format={gb} height={170} series={series((p) => p[4])} />
        </div>
        <div>
          <div className="sm-title">{t('serverload.bandwidth_incoming_outgoing')}</div>
          <TimeChart times={times} bucket={d.bucket} kind="line" format={bits} height={170} series={series((p) => p[5] + p[6])} />
        </div>
      </div>
    </div>
  )
}

function NodeLoadCard({ n, d }: { n: LoadT['nodes'][number]; d: LoadT }) {
  const times = axis(d)
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
              <span><Trans k="serverload.cpu" v={{ v: pct(last[1]) }} /></span>
              {last[3] >= 0.5 && <span className={last[3] >= 5 ? 'pink-text' : ''}>steal {last[3].toFixed(1)}%</span>}
              <span><Trans k="serverload.ram" v={{ v: gb(last[4]) }} />{n.memTotal ? t('serverload.of', { v: gb(n.memTotal) }) : ''}</span>
              <span>↓ {bits(last[5])} · ↑ {bits(last[6])}</span>
            </div>
          ) : <div className="card-sub">{t('serverload.no_data_for_the_period')}</div>}
        </div>
      </div>
      {!empty && (
        <div className="card-pad small-multiples load-grid">
          <div>
            <div className="row between"><div className="sm-title">{t('serverload.processor')}</div>
              <Legend items={[{ label: t('serverload.average'), color: 'var(--s1)' }, { label: t('serverload.peak'), color: 'var(--s4)' }]} /></div>
            <TimeChart times={times} bucket={d.bucket} kind="line" max={100} format={pct} height={160}
              series={[
                { key: 'max', label: t('serverload.peak'), color: 'var(--s4)', values: col(2) },
                { key: 'avg', label: t('serverload.average'), color: 'var(--s1)', values: col(1) },
              ]} />
          </div>
          <div>
            <div className="sm-title">Steal<Info>{t('serverload.cpu_time_the_hosting_provider_2')}</Info></div>
            <TimeChart times={times} bucket={d.bucket} kind="line" format={pct1} height={160}
              series={[{ key: 'steal', label: 'steal', color: 'var(--s2)', values: col(3) }]} />
          </div>
          <div>
            <div className="sm-title">{t('serverload.memory')}{n.memTotal ? <span className="muted small">{' '}<Trans k="serverload.total" v={{ v: gb(n.memTotal) }} /></span> : null}</div>
            <TimeChart times={times} bucket={d.bucket} kind="area" unit="bytes" max={n.memTotal || undefined} format={gb} height={160}
              series={[{ key: 'mem', label: t('serverload.used'), color: 'var(--s3)', values: col(4) }]} />
          </div>
          <div>
            <div className="row between"><div className="sm-title">{t('serverload.bandwidth')}</div>
              <Legend items={[{ label: t('serverload.incoming'), color: 'var(--s1)' }, { label: t('serverload.outgoing'), color: 'var(--s2)' }]} /></div>
            <TimeChart times={times} bucket={d.bucket} kind="line" format={bits} height={160}
              series={[
                { key: 'rx', label: t('serverload.incoming'), color: 'var(--s1)', values: col(5) },
                { key: 'tx', label: t('serverload.outgoing'), color: 'var(--s2)', values: col(6) },
              ]} />
          </div>
        </div>
      )}
    </div>
  )
}
