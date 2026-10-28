import { get, Overview as OverviewT } from '../api'
import { bytes, nodeStateInfo } from '../format'
import { Badge, ChartLegend, Empty, Loading, TrafficChart, useLoad } from '../ui'
import { UpdateBanner } from '../updates'
import { t, Trans } from '../i18n'

export function Overview() {
  const { data, error } = useLoad(() => get<OverviewT>('overview'), [], 15000)
  if (!data) return <Loading error={error} />
  const u = data.users
  return (
    <>
      <UpdateBanner />
      <div className="stats">
        <div className="card stat">
          <div className="k">{t('common.users')}</div>
          <div className="v">{u.total}</div>
          <div className="s"><span className="miku-text"><Trans k="overview.active" v={{ active: u.active }} /></span>{u.expired + u.limited > 0 && <> · <span className="pink-text"><Trans k="overview.without_access" v={{ v: u.expired + u.limited }} /></span></>}</div>
        </div>
        <div className="card stat pink">
          <div className="k">{t('common.online_now')}</div>
          <div className="v">{data.onlineNow}</div>
          <div className="s">{t('overview.in_the_last_3_minutes')}</div>
        </div>
        <div className="card stat">
          <div className="k">{t('overview.traffic_today')}</div>
          <div className="v">{bytes(data.todayBytes)}</div>
          <div className="s">{t('common.all_users')}</div>
        </div>
        <div className="card stat pink">
          <div className="k">{t('overview.last_30_days')}</div>
          <div className="v">{bytes(data.monthBytes)}</div>
          <div className="s">{t('common.nodes_count', { n: data.nodes.length })}</div>
        </div>
      </div>

      <div className="card section-gap">
        <div className="card-head">
          <h3>{t('overview.traffic_by_day')}</h3>
          <ChartLegend />
        </div>
        <div className="card-pad"><TrafficChart days={data.daily} /></div>
      </div>

      <div className="cards cols section-gap">
        <div className="card">
          <div className="card-head"><h3>{t('common.nodes')}</h3><a href="#/nodes" className="small">{t('overview.all')}</a></div>
          {data.nodes.length === 0 ? <Empty>{t('common.no_nodes_yet')}</Empty> : (
            <div className="table-wrap"><table className="table">
              <tbody>
                {data.nodes.map((n) => {
                  const st = nodeStateInfo[n.state] || { label: n.state, color: '' }
                  return (
                    <tr key={n.id} className="click" onClick={() => (location.hash = '#/nodes')}>
                      <td className="nowrap"><span style={{ fontSize: 18 }}>{n.flag}</span> {n.name} <span className="muted small">{n.code}</span></td>
                      <td><Badge color={st.color} dot>{st.label}</Badge></td>
                      <td className="nowrap text-2 small hide-sm">{n.metrics ? t('overview.online', { online: n.metrics.online }) : ''}</td>
                      <td className="nowrap text-2">{bytes(n.todayBytes)}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table></div>
          )}
        </div>
        <div className="card">
          <div className="card-head"><h3>{t('overview.top_traffic_30_days')}</h3></div>
          {data.topUsers.length === 0 ? <Empty>{t('common.no_traffic_yet')}</Empty> : (
            <table className="table">
              <tbody>
                {data.topUsers.map((t, i) => (
                  <tr key={t.id} className="click" onClick={() => (location.hash = '#/users/' + t.id)}>
                    <td className="muted" style={{ width: 30 }}>{i + 1}</td>
                    <td>{t.username}</td>
                    <td className="nowrap" style={{ textAlign: 'right' }}>{bytes(t.bytes)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>
    </>
  )
}
