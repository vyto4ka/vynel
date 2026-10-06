import { get, Overview as OverviewT } from '../api'
import { bytes, nodeStateInfo } from '../format'
import { Badge, ChartLegend, Empty, Loading, TrafficChart, useLoad } from '../ui'

export function Overview() {
  const { data, error } = useLoad(() => get<OverviewT>('overview'), [], 15000)
  if (!data) return <Loading error={error} />
  const u = data.users
  return (
    <>
      <div className="stats">
        <div className="card stat">
          <div className="k">Пользователи</div>
          <div className="v">{u.total}</div>
          <div className="s"><span className="miku-text">{u.active} активных</span>{u.expired + u.limited > 0 && <> · <span className="pink-text">{u.expired + u.limited} без доступа</span></>}</div>
        </div>
        <div className="card stat pink">
          <div className="k">Онлайн сейчас</div>
          <div className="v">{data.onlineNow}</div>
          <div className="s">за последние 3 минуты</div>
        </div>
        <div className="card stat">
          <div className="k">Трафик сегодня</div>
          <div className="v">{bytes(data.todayBytes)}</div>
          <div className="s">все пользователи</div>
        </div>
        <div className="card stat pink">
          <div className="k">За 30 дней</div>
          <div className="v">{bytes(data.monthBytes)}</div>
          <div className="s">{data.nodes.length} {data.nodes.length === 1 ? 'нода' : 'нод(ы)'}</div>
        </div>
      </div>

      <div className="card section-gap">
        <div className="card-head">
          <h3>Трафик по дням</h3>
          <ChartLegend />
        </div>
        <div className="card-pad"><TrafficChart days={data.daily} /></div>
      </div>

      <div className="cards cols section-gap">
        <div className="card">
          <div className="card-head"><h3>Ноды</h3><a href="#/nodes" className="small">все →</a></div>
          {data.nodes.length === 0 ? <Empty>Нод пока нет</Empty> : (
            <table className="table">
              <tbody>
                {data.nodes.map((n) => {
                  const st = nodeStateInfo[n.state] || { label: n.state, color: '' }
                  return (
                    <tr key={n.id} className="click" onClick={() => (location.hash = '#/nodes')}>
                      <td className="nowrap"><span style={{ fontSize: 18 }}>{n.flag}</span> {n.name} <span className="muted small">{n.code}</span></td>
                      <td><Badge color={st.color} dot>{st.label}</Badge></td>
                      <td className="nowrap text-2">{n.metrics ? `${n.metrics.online} онлайн` : ''}</td>
                      <td className="nowrap text-2">{bytes(n.todayBytes)}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          )}
        </div>
        <div className="card">
          <div className="card-head"><h3>Больше всех трафика (30 дней)</h3></div>
          {data.topUsers.length === 0 ? <Empty>Трафика пока нет</Empty> : (
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
