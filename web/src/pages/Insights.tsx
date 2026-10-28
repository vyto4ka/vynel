import { useEffect, useMemo, useState } from 'react'
import { get } from '../api'
import { BarList, fmtBytes, Legend, Segmented, SERIES, TimeChart } from '../charts'
import { ago, bytes, dateTime } from '../format'
import { Empty, Icon, Info, Loading, useLoad } from '../ui'
import { t, Trans } from '../i18n'

// Аналитика — куда уходит трафик (по профилям и каскадам, по каждому пользователю), все устройства
// по HWID и все адреса, с которых подключались. Трафик и адреса хранятся 90 дней.

type Tab = 'traffic' | 'devices' | 'ips'
const tabs: [Tab, string][] = [['traffic', t('insights.by_profile')], ['devices', t('common.devices')], ['ips', t('insights.ip_addresses')]]

function fromHash(): { tab: Tab; user: number } {
  const [, sub, user] = location.hash.replace(/^#\/?/, '').split('/')
  return { tab: tabs.some(([t]) => t === sub) ? (sub as Tab) : 'traffic', user: Number(user) || 0 }
}

export function Insights() {
  const [state, setState] = useState(fromHash)
  useEffect(() => {
    const h = () => setState(fromHash())
    window.addEventListener('hashchange', h)
    return () => window.removeEventListener('hashchange', h)
  }, [])
  const open = (tab: Tab, user = 0) => {
    history.replaceState(null, '', `#/insights/${tab}${user ? '/' + user : ''}`)
    setState({ tab, user })
  }
  return (
    <>
      <div className="toolbar">
        <div className="seg">
          {tabs.map(([t, label]) => <button key={t} className={state.tab === t ? 'on' : ''} onClick={() => open(t)}>{label}</button>)}
        </div>
      </div>
      {state.tab === 'traffic' && <ProfileTraffic user={state.user} setUser={(u) => open('traffic', u)} />}
      {state.tab === 'devices' && <><Newcomers /><Devices /></>}
      {state.tab === 'ips' && <IPs />}
    </>
  )
}

// ---------- трафик по профилям ----------

interface TrafficT {
  range: string
  days: number[]
  profiles: { key: string; label: string; kind: 'profile' | 'cascade' | 'other'; total: number; values: number[] }[]
  users: { id: number; username: string; total: number; by: Record<string, number> }[]
}

type Range = '7d' | '30d' | '90d'
const ranges: { value: Range; label: string }[] = [{ value: '7d', label: t('common.n7_days') }, { value: '30d', label: t('common.n30_days') }, { value: '90d', label: t('common.n90_days') }]
const rangeWord: Record<Range, string> = { '7d': t('common.in_7_days'), '30d': t('common.in_30_days'), '90d': t('common.in_90_days') }

// Цветов шесть: пять самых больших профилей и «остальные».
const shown = 5
const OTHER = '__other'

function savedRange(): Range {
  try {
    const v = localStorage.getItem('vynel.insights.range')
    if (v && ranges.some((r) => r.value === v)) return v as Range
  } catch { /* хранилище недоступно */ }
  return '30d'
}

function ProfileTraffic({ user, setUser }: { user: number; setUser: (id: number) => void }) {
  const [range, setRangeState] = useState<Range>(savedRange)
  const setRange = (r: Range) => {
    setRangeState(r)
    try { localStorage.setItem('vynel.insights.range', r) } catch { /* ignore */ }
  }
  const [search, setSearch] = useState('')
  const [sort, setSort] = useState<string>('total')
  const { data, error } = useLoad(() => get<TrafficT>(`insights/traffic?range=${range}${user ? '&user=' + user : ''}`), [range, user], 5 * 60 * 1000)
  const ok = data && data.range === range
  const groups = useMemo(() => {
    if (!data) return []
    const top = data.profiles.slice(0, data.profiles.length > shown + 1 ? shown : shown + 1)
    const rest = data.profiles.slice(top.length)
    const out = top.map((p, i) => ({ key: p.key, keys: [p.key], label: p.label, kind: p.kind, total: p.total, values: p.values, color: SERIES[i] }))
    if (rest.length) {
      out.push({ key: OTHER, keys: rest.map((p) => p.key), label: t('insights.others', { length: rest.length }), kind: 'other', color: SERIES[shown],
        total: rest.reduce((a, p) => a + p.total, 0), values: data.days.map((_, i) => rest.reduce((a, p) => a + p.values[i], 0)) })
    }
    return out
  }, [data])
  const name = data?.users.find((u) => u.id === user)?.username

  const share = (u: TrafficT['users'][number], g: (typeof groups)[number]) => g.keys.reduce((a, k) => a + (u.by[k] || 0), 0)
  const users = useMemo(() => {
    if (!data) return []
    const q = search.trim().toLowerCase()
    const list = data.users.filter((u) => !q || u.username.toLowerCase().includes(q))
    const g = groups.find((x) => x.key === sort)
    return g ? [...list].sort((a, b) => share(b, g) - share(a, g)) : list
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data, groups, search, sort])

  return (
    <>
      <div className="stats-head">
        <div className="row" style={{ gap: 8 }}>
          {user ? (
            <span className="chip">{name || `#${user}`}<button onClick={() => setUser(0)} aria-label={t('insights.all_users')}><Icon name="close" size={14} /></button></span>
          ) : <span className="muted small">{t('insights.all_users_click_a_row')}</span>}
        </div>
        <Segmented value={range} options={ranges} onChange={setRange} />
      </div>
      {!ok ? <Loading error={error} /> : data.profiles.length === 0 ? (
        <div className="card"><Empty icon="📊"><Trans k="insights.no_traffic" v={{ v: rangeWord[range] }} />{user ? t('insights.for_this_user') : ''}</Empty></div>
      ) : (
        <>
          <div className="card">
            <div className="card-head">
              <div><h3>{t('insights.traffic_by_profile')}</h3><div className="card-sub">{t('insights.by_day')}{' '}{user ? name || t('insights.user') : t('common.all_users')}</div></div>
              <Legend items={groups.map((g) => ({ label: g.label, color: g.color }))} />
            </div>
            <div className="card-pad">
              <TimeChart times={data.days} bucket={86400} kind="bars" stacked unit="bytes" format={fmtBytes} height={260}
                series={groups.map((g) => ({ key: g.key, label: g.label, color: g.color, values: g.values }))} />
            </div>
          </div>

          <div className="cols-2 section-gap">
            <div className="card">
              <div className="card-head"><div><h3>{t('common.profiles')}{' '}<Info>
                  <p>{t('insights.a_profile_s_traffic_is')}</p>
                  <p>{t('insights.before_the_profile_breakdown')}</p>
                </Info></h3><div className="card-sub"><Trans k="insights.all_nodes_of_the_profile" v={{ v: rangeWord[range] }} /></div></div></div>
              <div className="card-pad">
                <BarList format={fmtBytes} rows={data.profiles.map((p, i) => ({
                  key: p.key, label: p.label, value: p.total, color: i < groups.length && groups[i].key === p.key ? groups[i].color : SERIES[shown],
                  sub: p.kind === 'cascade' ? t('common.cascade') : undefined,
                }))} />
              </div>
            </div>
            {!user && (
              <div className="card">
                <div className="card-head"><div><h3>{t('insights.top')}</h3><div className="card-sub">{rangeWord[range]}</div></div></div>
                <div className="card-pad">
                  <BarList format={fmtBytes} rows={data.users.slice(0, 8).map((u) => ({ key: u.id, label: u.username, value: u.total }))}
                    onClick={(id) => setUser(Number(id))} />
                </div>
              </div>
            )}
          </div>

          <div className="card section-gap">
            <div className="card-head">
              <div><h3>{t('common.users')}</h3><div className="card-sub"><Trans k="insights.columns_sort" v={{ v: rangeWord[range] }} /></div></div>
              {!user && (
                <div className="search-box">
                  <input className="input" placeholder={t('insights.search_by_name')} value={search} onChange={(e) => setSearch(e.target.value)} />
                  <span className="muted search-icon"><Icon name="search" size={18} /></span>
                </div>
              )}
            </div>
            {users.length === 0 ? <Empty>{t('insights.nothing_found')}</Empty> : (
              <div className="table-wrap">
                <table className="table">
                  <thead>
                    <tr>
                      <th>{t('common.user')}</th>
                      <th className={'sortable' + (sort === 'total' ? ' sorted' : '')} onClick={() => setSort('total')}><Trans k="insights.total" v={{ v: sort === 'total' ? ' ↓' : '' }} /></th>
                      {groups.map((g) => (
                        <th key={g.key} className={'sortable hide-sm' + (sort === g.key ? ' sorted' : '')} onClick={() => setSort(g.key)}>
                          <span className="legend-dot" style={{ background: g.color }} />{g.label}{sort === g.key ? ' ↓' : ''}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {users.map((u) => (
                      <tr key={u.id} className={'click' + (u.id === user ? ' selected' : '')} onClick={() => setUser(u.id === user ? 0 : u.id)}>
                        <td style={{ minWidth: 140 }}>
                          <b>{u.username}</b>
                          <div className="sharebar thin" style={{ marginTop: 6 }}>
                            {groups.map((g) => { const v = share(u, g); return v > 0 && <div key={g.key} style={{ flexGrow: v, background: g.color }} title={`${g.label}: ${bytes(v)}`} /> })}
                          </div>
                        </td>
                        <td className="nowrap">{bytes(u.total)}</td>
                        {groups.map((g) => { const v = share(u, g); return <td key={g.key} className="small text-2 nowrap hide-sm">{v ? bytes(v) : '—'}</td> })}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        </>
      )}
    </>
  )
}

// ---------- устройства и адреса ----------

interface NewcomersT { days: number[]; devices: number[]; ips: number[]; platforms: { label: string; n: number }[] }

function Newcomers() {
  const { data, error } = useLoad(() => get<NewcomersT>('insights/newcomers'), [], 5 * 60 * 1000)
  if (!data) return <Loading error={error} />
  const count = (v: number) => String(Math.round(v))
  return (
    <div className="cols-2" style={{ marginBottom: 16 }}>
      <div className="card">
        <div className="card-head">
          <div><h3>{t('insights.new_in_30_days')}</h3><div className="card-sub">{t('insights.devices_and_addresses_seen')}</div></div>
          <Legend items={[{ label: t('insights.devices'), color: 'var(--s1)' }, { label: t('insights.ip_addresses'), color: 'var(--s2)' }]} />
        </div>
        <div className="card-pad">
          <TimeChart times={data.days} bucket={86400} kind="bars" format={count} height={200}
            series={[{ key: 'dev', label: t('insights.devices'), color: 'var(--s1)', values: data.devices }, { key: 'ip', label: t('insights.ip_addresses'), color: 'var(--s2)', values: data.ips }]} />
        </div>
      </div>
      <div className="card">
        <div className="card-head"><div><h3>{t('common.platforms')}</h3><div className="card-sub">{t('insights.all_registered_devices')}</div></div></div>
        <div className="card-pad">
          {data.platforms.length === 0 ? <Empty>{t('common.no_devices_yet')}</Empty> : (
            <BarList format={count} rows={data.platforms.map((p, i) => ({ key: p.label || '?', label: p.label || t('common.unknown'), value: p.n, color: SERIES[i % SERIES.length] }))} />
          )}
        </div>
      </div>
    </div>
  )
}

const pageSize = 100

function usePaged<T>(path: string) {
  const [search, setSearch] = useState('')
  const [page, setPage] = useState(0)
  useEffect(() => setPage(0), [search])
  const list = useLoad(() => get<{ items: T[]; total: number }>(`${path}?q=${encodeURIComponent(search)}&limit=${pageSize}&offset=${page * pageSize}`), [search, page], 60000)
  return { search, setSearch, page, setPage, list }
}

function Pager({ page, total, setPage }: { page: number; total: number; setPage: (p: number) => void }) {
  if (total <= pageSize) return null
  return (
    <div className="row between card-pad small text-2">
      <span><Trans k="common.of" v={{ v: page * pageSize + 1, min: Math.min(total, (page + 1) * pageSize), total: total }} /></span>
      <div className="row" style={{ gap: 6 }}>
        <button className="btn sm ghost" disabled={page === 0} onClick={() => setPage(page - 1)}>{t('common.back')}</button>
        <button className="btn sm ghost" disabled={(page + 1) * pageSize >= total} onClick={() => setPage(page + 1)}>{t('common.next')}</button>
      </div>
    </div>
  )
}

interface DeviceRow {
  id: number; userId: number; username: string; hwid: string; platform: string; osVersion: string; model: string
  userAgent: string; firstSeen: number; lastSeen: number; lastIp: string
}

function Devices() {
  const { search, setSearch, page, setPage, list } = usePaged<DeviceRow>('insights/devices')
  return (
    <div className="card">
      <div className="card-head">
        <div><h3>{t('insights.all_devices')}</h3><div className="card-sub">{list.data ? t('insights.by_hwid', { total: list.data.total }) : ' '}</div></div>
        <div className="search-box">
          <input className="input" placeholder={t('insights.user_hwid_model_ip')} value={search} onChange={(e) => setSearch(e.target.value)} />
          <span className="muted search-icon"><Icon name="search" size={18} /></span>
        </div>
      </div>
      {!list.data ? <Loading error={list.error} /> : list.data.items.length === 0 ? <Empty icon="📱">{search ? t('insights.nothing_found_2') : t('common.no_devices_yet')}</Empty> : (
        <div className="table-wrap">
          <table className="table">
            <thead><tr><th>{t('common.user')}</th><th>{t('insights.device')}</th><th className="hide-sm">HWID</th><th className="hide-sm">{t('insights.app')}</th><th className="hide-sm">IP</th><th className="hide-sm">{t('insights.first_seen')}</th><th>{t('insights.last_seen')}</th></tr></thead>
            <tbody>
              {list.data.items.map((d) => (
                <tr key={d.id} className="click" onClick={() => (location.hash = `#/users/${d.userId}`)}>
                  <td><b>{d.username}</b></td>
                  <td><div>{d.model || '—'}</div><div className="muted small">{[d.platform, d.osVersion].filter(Boolean).join(' ')}</div></td>
                  <td className="hide-sm mono small ellipsis" style={{ maxWidth: 180 }} title={d.hwid}>{d.hwid}</td>
                  <td className="hide-sm small text-2 ellipsis" style={{ maxWidth: 200 }} title={d.userAgent}>{d.userAgent || '—'}</td>
                  <td className="hide-sm small mono">{d.lastIp || '—'}</td>
                  <td className="hide-sm small muted nowrap">{dateTime(d.firstSeen)}</td>
                  <td className="small muted nowrap">{ago(d.lastSeen)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <Pager page={page} total={list.data?.total ?? 0} setPage={setPage} />
    </div>
  )
}

interface IPRow { userId: number; username: string; ip: string; node: string; firstAt: number; lastAt: number }

function IPs() {
  const { search, setSearch, page, setPage, list } = usePaged<IPRow>('insights/ips')
  const now = Date.now() / 1000
  return (
    <>
      <Newcomers />
      <div className="card">
        <div className="card-head">
          <div><h3>{t('insights.connection_addresses')}</h3><div className="card-sub">{list.data ? t('insights.in_90_days_a_user', { total: list.data.total }) : ' '}</div></div>
          <div className="search-box">
            <input className="input" placeholder={t('insights.user_or_ip')} value={search} onChange={(e) => setSearch(e.target.value)} />
            <span className="muted search-icon"><Icon name="search" size={18} /></span>
          </div>
        </div>
        {!list.data ? <Loading error={list.error} /> : list.data.items.length === 0 ? <Empty icon="🌐">{search ? t('insights.nothing_found_2') : t('insights.no_connections_yet')}</Empty> : (
          <div className="table-wrap">
            <table className="table">
              <thead><tr><th>IP</th><th>{t('common.user')}</th><th className="hide-sm">{t('common.node')}</th><th className="hide-sm">{t('insights.first_seen')}</th><th>{t('insights.last_seen')}</th></tr></thead>
              <tbody>
                {list.data.items.map((r) => (
                  <tr key={r.userId + r.ip} className="click" onClick={() => (location.hash = `#/users/${r.userId}`)}>
                    <td className="mono small">{r.ip}</td>
                    <td><b>{r.username}</b></td>
                    <td className="hide-sm small text-2">{r.node || '—'}</td>
                    <td className="hide-sm small muted nowrap">{dateTime(r.firstAt)}</td>
                    <td className="small muted nowrap">{now - r.lastAt < 180 ? <span className="miku-text">{t('common.now')}</span> : ago(r.lastAt)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        <Pager page={page} total={list.data?.total ?? 0} setPage={setPage} />
      </div>
    </>
  )
}
