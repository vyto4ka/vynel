import { useState } from 'react'
import { get } from '../api'
import { BarList, Delta, fmtBytes, Heatmap, Legend, Segmented, SERIES, ShareBar, Sparkline, TimeChart } from '../charts'
import { bytes } from '../format'
import { Empty, Info, Loading, useLoad } from '../ui'
import { t, Trans } from '../i18n'

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
  { value: '24h', label: t('common.n24_hours') }, { value: '7d', label: t('common.n7_days') }, { value: '30d', label: t('common.n30_days') }, { value: '90d', label: t('common.n90_days') },
]
const rangeWord: Record<Range, string> = { '24h': t('stats.in_a_day'), '7d': t('common.in_7_days'), '30d': t('common.in_30_days'), '90d': t('common.in_90_days') }

function loadRange(): Range {
  try {
    const v = localStorage.getItem('vynel.stats.range')
    if (v && ranges.some((r) => r.value === v)) return v as Range
  } catch { /* storage may be unavailable */ }
  return '7d'
}

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
        <div className="muted small">{data ? <><Trans k="stats.time" v={{ timeZone: data.timeZone }} />{' '}<Info>{t('stats.online_is_kept_for_7')}</Info></> : ' '}</div>
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
  const lastWeek = range === '30d' || range === '90d' // load and online are kept 7 days
  const mb = d.metricsBucket

  return (
    <>
      <div className="stats">
        <div className="card stat kpi">
          <div className="k"><Trans k="stats.traffic" v={{ v: rangeWord[range] }} /></div>
          <div className="v">{bytes(d.total)}</div>
          <div className="s"><Delta now={d.total} prev={d.prevTotal} /></div>
          <Sparkline values={totalSeries} />
        </div>
        <div className="card stat kpi pink">
          <div className="k">{t('stats.active_users')}</div>
          <div className="v">{d.activeUsers}<span className="muted" style={{ fontSize: 15, fontWeight: 500 }}>{' '}<Trans k="stats.of" v={{ usersTotal: usersTotal }} /></span></div>
          <div className="s"><Delta now={d.activeUsers} prev={d.prevActive} /></div>
        </div>
        <div className="card stat kpi">
          <div className="k">{t('common.online_now')}</div>
          <div className="v">{d.onlineNow}</div>
          <div className="s"><Trans k="stats.peak" v={{ v: rangeWord[lastWeek ? '7d' : range], peakOnline: d.peakOnline }} /></div>
          <Sparkline values={d.online.map((p) => p[1] ?? 0)} color="var(--s2)" />
        </div>
        <div className="card stat kpi pink">
          <div className="k">{t('stats.average_per_active_user')}</div>
          <div className="v">{bytes(d.activeUsers ? d.total / d.activeUsers : 0)}</div>
          <div className="s">{rangeWord[range]}</div>
        </div>
      </div>

      <div className="card section-gap">
        <div className="card-head">
          <div><h3>{t('common.traffic')}</h3><div className="card-sub">{d.bucket < 86400 ? t('stats.by_hour') : t('stats.by_day')}{t('stats.all_users')}</div></div>
          <Legend items={[{ label: t('common.downloaded'), color: 'var(--s1)' }, { label: t('common.uploaded'), color: 'var(--s2)' }]} />
        </div>
        <div className="card-pad">
          <TimeChart times={times} bucket={d.bucket} kind={d.bucket < 86400 ? 'area' : 'bars'} stacked unit="bytes" format={fmtBytes} height={260}
            series={[
              { key: 'down', label: t('common.downloaded'), color: 'var(--s1)', values: d.traffic.map((p) => p[1]) },
              { key: 'up', label: t('common.uploaded'), color: 'var(--s2)', values: d.traffic.map((p) => p[2]) },
            ]} />
        </div>
      </div>

      <div className="cols-2 section-gap">
        <div className="card">
          <div className="card-head"><div><h3>{t('common.online')}</h3><div className="card-sub">{t('stats.users_at_the_same_time')}{lastWeek ? t('stats.last_7_days') : ''}</div></div></div>
          <div className="card-pad">
            <TimeChart times={d.online.map((p) => p[0])} bucket={mb} kind="line" format={count} height={200}
              series={[{ key: 'online', label: t('common.online_2'), color: 'var(--s2)', values: d.online.map((p) => p[1]) }]} />
          </div>
        </div>
        <div className="card">
          <div className="card-head"><div><h3>{t('stats.traffic_by_node')}</h3><div className="card-sub"><Trans k="stats.everything_that_passed_through" v={{ v: rangeWord[range] }} /></div></div></div>
          <div className="card-pad">
            {d.nodes.length === 0 ? <Empty>{t('common.no_nodes_yet')}</Empty> : (
              <BarList format={fmtBytes} rows={d.nodes.map((n) => ({ key: n.id, label: `${n.flag} ${n.name}`, sub: n.code, value: n.total, color: nodeColor.get(n.id) }))}
                onClick={() => (location.hash = '#/nodes')} />
            )}
          </div>
        </div>
      </div>


      <div className="cols-2 section-gap">
        <div className="card">
          <div className="card-head"><div><h3>{t('stats.when_they_use_it')}</h3><div className="card-sub">{t('stats.traffic_in_the_last_7')}</div></div></div>
          <div className="card-pad"><Heatmap data={d.heatmap} /></div>
        </div>
        <div className="card">
          <div className="card-head"><div><h3>{t('stats.top_traffic')}</h3><div className="card-sub">{rangeWord[range]}</div></div></div>
          <div className="card-pad">
            {d.topUsers.length === 0 ? <Empty>{t('common.no_traffic_yet')}</Empty> : (
              <BarList format={fmtBytes} rows={d.topUsers.map((t) => ({ key: t.id, label: t.username, value: t.bytes }))}
                onClick={(id) => (location.hash = '#/users/' + id)} />
            )}
          </div>
        </div>
      </div>

      <div className="card section-gap">
        <div className="card-head"><div><h3>{t('common.users')}</h3><div className="card-sub">{t('common.users_count', { n: usersTotal })}{' '}{t('stats.by_state')}</div></div></div>
        <div className="card-pad">
          <ShareBar parts={[
            { label: t('stats.active'), value: u.active, color: 'var(--miku)' },
            { label: t('stats.traffic_used_up'), value: u.limited, color: 'var(--amber)' },
            { label: t('stats.expired'), value: u.expired, color: 'var(--red)' },
            { label: t('stats.disabled'), value: u.disabled, color: 'var(--muted)' },
          ]} />
        </div>
      </div>

      <div className="cols-2 section-gap">
        <div className="card">
          <div className="card-head"><div><h3>{t('common.apps')}</h3><div className="card-sub">{t('stats.what_users_last_refreshed_their')}</div></div></div>
          <div className="card-pad">
            {d.apps.length === 0 ? <Empty>{t('stats.no_subscription_refreshed_yet')}</Empty> : (
              <BarList format={(v) => t('stats.users_short', { n: v })} color="var(--s5)" rows={d.apps.slice(0, 10).map((a) => ({ key: a.label, label: a.label, value: a.n }))} />
            )}
          </div>
        </div>
        <div className="card">
          <div className="card-head"><div><h3>{t('common.devices')}</h3><div className="card-sub">{t('stats.registered_by_hwid_by_platform')}</div></div></div>
          <div className="card-pad">
            {d.platforms.length === 0 ? <Empty>{t('common.no_devices_yet')}</Empty> : (
              <BarList format={(v) => t('common.devices_count', { n: v })} color="var(--s3)" rows={d.platforms.map((p) => ({ key: p.label, label: p.label, value: p.n }))} />
            )}
          </div>
        </div>
      </div>
    </>
  )
}
