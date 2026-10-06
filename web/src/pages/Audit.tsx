import { AuditEntry, get } from '../api'
import { dateTime } from '../format'
import { Empty, Loading, useLoad } from '../ui'

const actors: Record<string, string> = { admin: 'веб', cli: 'терминал', system: 'система', bot: 'бот', api: 'API' }

export function Audit() {
  const { data, error } = useLoad(() => get<AuditEntry[]>('audit?limit=300'), [], 30000)
  if (!data) return <Loading error={error} />
  return (
    <div className="card">
      {data.length === 0 ? <Empty>Изменений пока нет</Empty> : (
        <div className="table-wrap">
          <table className="table">
            <thead><tr><th>Когда</th><th>Кто</th><th>Что</th><th>Объект</th><th>Подробности</th></tr></thead>
            <tbody>
              {data.map((e) => (
                <tr key={e.id}>
                  <td className="nowrap small text-2">{dateTime(e.ts)}</td>
                  <td className="nowrap small">{actors[e.actor] || e.actor}{e.actorId && <span className="muted"> {e.actorId}</span>}</td>
                  <td className="nowrap mono small miku-text">{e.action}</td>
                  <td className="nowrap small text-2">{e.entity}{e.entityId ? ` #${e.entityId}` : ''}</td>
                  <td className="mono small muted ellipsis" style={{ maxWidth: 380 }} title={e.diff}>{e.diff === '{}' || e.diff === 'null' ? '' : e.diff}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
