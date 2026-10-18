import { useState } from 'react'
import { get } from '../api'
import { BarList, Delta, fmtBytes, Heatmap, Legend, Segmented, SERIES, ShareBar, Sparkline, TimeChart } from '../charts'
import { bytes, plural } from '../format'
import { Empty, Loading, useLoad } from '../ui'

type Pt = [number, number | null]

interface StatsT {
  range: string
  bucket: number
  from: number
  to: number
  timeZone: string
  traffic: [number, number, number][] // t, down, up
  total: number
  prevTotal: number
  activeUsers: number
  prevActive: number
  peakOnline: number
  onlineNow: number
  online: Pt[]
  nodes: { id: number; code: string; name: string; flag: string; total: number; traffic: Pt[]; cpu: Pt[]; mem: Pt[] }[]
  topUsers: { id: number; username: string; bytes: number }[]
  heatmap: number[][]
  platforms: { label: string; n: number }[]
  apps: { label: string; n: number }[]
  metricsSince: number
  metricsBucket: number
  users: { active: number; limited: number; expired: number; disabled: number }
}

type Range = '24h' | '7d' | '30d' | '90d'
const ranges: { value: Range; label: string }[] = [
  { value: '24h', label: '24 часа' }, { value: '7d', label: '7 дней' }, { value: '30d', label: '30 дней' }, { value: '90d', label: '90 дней' },
]
const rangeWord: Record<Range, string> = { '24h': 'за сутки', '7d': 'за 7 дней', '30d': 'за 30 дней', '90d': 'за 90 дней' }

function loadRange(): Range {
  try {
    const v = localStorage.getItem('vynel.stats.range')
    if (v && ranges.some((r) => r.value === v)) return v as Range
  } catch { /* storage may be unavailable */ }
  return '7d'
}

const pct = (v: number) => `${Math.round(v)}%`
const count = (v: number) => String(Math.round(v))

export function Stats() {
  const [range, setRangeState] = useState<Range>(loadRange)
  const setRange = (r: Range) => {
    setRangeState(r)
    try { localStorage.setItem('vynel.stats.range', r) } catch { /* ignore */ }
  }
  const { data, error } = useLoad(() => get<StatsT>(`stats?range=${range}`), [range], 30000)

  return (
    <>
      <div className="stats-head">
        <div className="muted small">{data ? <>Время — {data.timeZone}. Нагрузка и онлайн хранятся 7 дней, трафик — 90.</> : ' '}</div>
        <Segmented value={range} options={ranges} onChange={setRange} />
      </div>
      {!data || data.range !== range ? <Loading error={error} /> : <StatsBody d={data} range={range} />}
    </>
  )
}

function StatsBody({ d, range }: { d: StatsT; range: Range }) {
  const times = d.traffic.map((p) => p[0])
  const totalSeries = d.traffic.map((p) => p[1] + p[2])
  const u = d.users
  const usersTotal = u.active + u.limited + u.expired + u.disabled
  // Node colors follow the node (by id), not its rank in the period.
  const nodeColor = new Map([...d.nodes].sort((a, b) => a.id - b.id).map((n, i) => [n.id, SERIES[i % SERIES.length]]))
  const nodes = d.nodes.slice(0, 6)
  const metricsTimes = nodes[0]?.cpu.map((p) => p[0]) ?? []
  const lastWeek = range === '30d' || range === '90d' // load and online are kept 7 days
  const mb = d.metricsBucket

  return (
    <>
      <div className="stats">
        <div className="card stat kpi">
          <div className="k">Трафик {rangeWord[range]}</div>
          <div className="v">{bytes(d.total)}</div>
          <div className="s"><Delta now={d.total} prev={d.prevTotal} /></div>
          <Sparkline values={totalSeries} />
        </div>
        <div className="card stat kpi pink">
          <div className="k">Активных пользователей</div>
          <div className="v">{d.activeUsers}<span className="muted" style={{ fontSize: 15, fontWeight: 500 }}> из {usersTotal}</span></div>
          <div className="s"><Delta now={d.activeUsers} prev={d.prevActive} /></div>
        </div>
        <div className="card stat kpi">
          <div className="k">Онлайн сейчас</div>
          <div className="v">{d.onlineNow}</div>
          <div className="s">пик {rangeWord[lastWeek ? '7d' : range]}: <b>{d.peakOnline}</b></div>
          <Sparkline values={d.online.map((p) => p[1] ?? 0)} color="var(--s2)" />
        </div>
        <div className="card stat kpi pink">
          <div className="k">В среднем на активного</div>
          <div className="v">{bytes(d.activeUsers ? d.total / d.activeUsers : 0)}</div>
          <div className="s">{rangeWord[range]}</div>
        </div>
      </div>

      <div className="card section-gap">
        <div className="card-head">
          <div><h3>Трафик</h3><div className="card-sub">{d.bucket < 86400 ? 'по часам' : 'по дням'}, все пользователи</div></div>
          <Legend items={[{ label: 'скачано', color: 'var(--s1)' }, { label: 'отдано', color: 'var(--s2)' }]} />
        </div>
        <div className="card-pad">
          <TimeChart times={times} bucket={d.bucket} kind={d.bucket < 86400 ? 'area' : 'bars'} stacked unit="bytes" format={fmtBytes} height={260}
            series={[
              { key: 'down', label: 'скачано', color: 'var(--s1)', values: d.traffic.map((p) => p[1]) },
              { key: 'up', label: 'отдано', color: 'var(--s2)', values: d.traffic.map((p) => p[2]) },
            ]} />
        </div>
      </div>

      <div className="cols-2 section-gap">
        <div className="card">
          <div className="card-head"><div><h3>Онлайн</h3><div className="card-sub">пользователей одновременно, все ноды{lastWeek ? ' · последние 7 дней' : ''}</div></div></div>
          <div className="card-pad">
            <TimeChart times={d.online.map((p) => p[0])} bucket={mb} kind="line" format={count} height={200}
              series={[{ key: 'online', label: 'онлайн', color: 'var(--s2)', values: d.online.map((p) => p[1]) }]} />
          </div>
        </div>
        <div className="card">
          <div className="card-head"><div><h3>Трафик по нодам</h3><div className="card-sub">{rangeWord[range]}, всё, что прошло через ноду</div></div></div>
          <div className="card-pad">
            {d.nodes.length === 0 ? <Empty>Нод пока нет</Empty> : (
              <BarList format={fmtBytes} rows={d.nodes.map((n) => ({ key: n.id, label: `${n.flag} ${n.name}`, sub: n.code, value: n.total, color: nodeColor.get(n.id) }))}
                onClick={() => (location.hash = '#/nodes')} />
            )}
          </div>
        </div>
      </div>

      {nodes.length > 0 && (
        <div className="card section-gap">
          <div className="card-head">
            <div><h3>Нагрузка нод</h3><div className="card-sub">средняя за час{lastWeek ? ' · последние 7 дней' : ''}</div></div>
            {nodes.length > 1 && <Legend items={nodes.map((n) => ({ label: `${n.flag} ${n.code}`, color: nodeColor.get(n.id)! }))} />}
          </div>
          <div className="card-pad small-multiples">
            <div>
              <div className="sm-title">Процессор</div>
              <TimeChart times={metricsTimes} bucket={mb} kind="line" max={100} format={pct} height={180}
                series={nodes.map((n) => ({ key: String(n.id), label: `${n.flag} ${n.name}`, color: nodeColor.get(n.id)!, values: n.cpu.map((p) => p[1]) }))} />
            </div>
            <div>
              <div className="sm-title">Память</div>
              <TimeChart times={metricsTimes} bucket={mb} kind="line" max={100} format={pct} height={180}
                series={nodes.map((n) => ({ key: String(n.id), label: `${n.flag} ${n.name}`, color: nodeColor.get(n.id)!, values: n.mem.map((p) => p[1]) }))} />
            </div>
          </div>
        </div>
      )}

      <div className="cols-2 section-gap">
        <div className="card">
          <div className="card-head"><div><h3>Когда пользуются</h3><div className="card-sub">трафик за последние 7 дней по дням недели и часам</div></div></div>
          <div className="card-pad"><Heatmap data={d.heatmap} /></div>
        </div>
        <div className="card">
          <div className="card-head"><div><h3>Больше всех трафика</h3><div className="card-sub">{rangeWord[range]}</div></div></div>
          <div className="card-pad">
            {d.topUsers.length === 0 ? <Empty>Трафика пока нет</Empty> : (
              <BarList format={fmtBytes} rows={d.topUsers.map((t) => ({ key: t.id, label: t.username, value: t.bytes }))}
                onClick={(id) => (location.hash = '#/users/' + id)} />
            )}
          </div>
        </div>
      </div>

      <div className="card section-gap">
        <div className="card-head"><div><h3>Пользователи</h3><div className="card-sub">{usersTotal} {plural(usersTotal, 'пользователь', 'пользователя', 'пользователей')} по состоянию</div></div></div>
        <div className="card-pad">
          <ShareBar parts={[
            { label: 'активны', value: u.active, color: 'var(--miku)' },
            { label: 'трафик исчерпан', value: u.limited, color: 'var(--amber)' },
            { label: 'срок истёк', value: u.expired, color: 'var(--red)' },
            { label: 'отключены', value: u.disabled, color: 'var(--muted)' },
          ]} />
        </div>
      </div>

      <div className="cols-2 section-gap">
        <div className="card">
          <div className="card-head"><div><h3>Приложения</h3><div className="card-sub">чем пользователи последний раз обновляли подписку</div></div></div>
          <div className="card-pad">
            {d.apps.length === 0 ? <Empty>Подписки ещё не обновлялись</Empty> : (
              <BarList format={(v) => `${v} ${plural(v, 'польз.', 'польз.', 'польз.')}`} color="var(--s5)" rows={d.apps.slice(0, 10).map((a) => ({ key: a.label, label: a.label, value: a.n }))} />
            )}
          </div>
        </div>
        <div className="card">
          <div className="card-head"><div><h3>Устройства</h3><div className="card-sub">зарегистрированные по HWID, по платформам</div></div></div>
          <div className="card-pad">
            {d.platforms.length === 0 ? <Empty>Устройств пока нет</Empty> : (
              <BarList format={(v) => `${v} ${plural(v, 'устройство', 'устройства', 'устройств')}`} color="var(--s3)" rows={d.platforms.map((p) => ({ key: p.label, label: p.label, value: p.n }))} />
            )}
          </div>
        </div>
      </div>
    </>
  )
}
