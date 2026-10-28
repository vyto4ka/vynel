import { useState } from 'react'
import { get, Integrations } from '../api'
import { dateTime } from '../format'
import { Badge, Empty, Info, Loading, useLoad } from '../ui'
import { Key, t } from '../i18n'

interface EventDoc {
  id: string
  type: string
  created: number
  actor: { kind: string; id: string }
  entity: { type: string; id: number; username?: string; code?: string; name?: string }
  data: Record<string, unknown>
}

const eventText: Record<string, Key> = {
  'user.created': 'integrations.ev_user_created',
  'user.deleted': 'integrations.ev_user_deleted',
  'user.extended': 'integrations.ev_user_extended',
  'user.expiry_changed': 'integrations.ev_user_expiry_changed',
  'user.limit_changed': 'integrations.ev_user_limit_changed',
  'user.traffic_reset': 'integrations.ev_user_traffic_reset',
  'user.groups_changed': 'integrations.ev_user_groups_changed',
  'user.updated': 'integrations.ev_user_updated',
  'user.credentials_changed': 'integrations.ev_user_credentials_changed',
  'user.status_changed': 'integrations.ev_user_status_changed',
  'user.enabled': 'integrations.ev_user_enabled',
  'user.disabled': 'integrations.ev_user_disabled',
  'node.created': 'integrations.ev_node_created',
  'node.updated': 'integrations.ev_node_updated',
  'node.deleted': 'integrations.ev_node_deleted',
  'node.online': 'integrations.ev_node_online',
  'node.offline': 'integrations.ev_node_offline',
  'group.created': 'integrations.ev_group_created',
  'group.updated': 'integrations.ev_group_updated',
  'group.deleted': 'integrations.ev_group_deleted',
}

const example = {
  id: 'evt_1042', type: 'user.extended', created: 1790000000,
  actor: { kind: 'api', id: 'billing' },
  entity: { type: 'user', id: 17, username: 'alice' },
  data: { months: 1, days: 0 },
}

export function Events({ d }: { d: Integrations }) {
  const feed = useLoad(() => get<EventDoc[]>('integrations/events'), [], 10000)
  const [open, setOpen] = useState<string | null>(null)
  return (
    <div className="cards" style={{ maxWidth: 980 }}>
      <div className="card">
        <div className="card-head"><h3>{t('integrations.feed_title')}{' '}<Info>{t('integrations.feed_info')}</Info></h3></div>
        {!feed.data ? <Loading error={feed.error} /> : feed.data.length === 0 ? <div className="card-pad"><Empty>{t('integrations.feed_empty')}</Empty></div> : (
          <div className="table-wrap">
            <table className="table">
              <tbody>
                {feed.data.map((e) => (
                  <tr key={e.id} className="click" onClick={() => setOpen(open === e.id ? null : e.id)}>
                    <td className="small nowrap">{dateTime(e.created)}</td>
                    <td><Badge>{e.type}</Badge></td>
                    <td className="small">
                      <b>{e.entity.username ?? e.entity.code ?? e.entity.name ?? `${e.entity.type} ${e.entity.id}`}</b>
                      {open === e.id && <pre className="snippet">{JSON.stringify(e, null, 2)}</pre>}
                    </td>
                    <td className="small muted nowrap">{t('integrations.by_actor', { actor: e.actor.id ? `${e.actor.kind}:${e.actor.id}` : e.actor.kind })}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
      <div className="card">
        <div className="card-head"><h3>{t('integrations.types_title')}</h3></div>
        <div className="card-pad">
          <table className="table">
            <tbody>
              {d.events.map((e) => (
                <tr key={e}><td className="mono small nowrap">{e}</td><td className="small">{eventText[e] ? t(eventText[e]) : ''}</td></tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
      <div className="card">
        <div className="card-head"><h3>{t('integrations.payload_title')}{' '}<Info>{t('integrations.payload_info')}</Info></h3></div>
        <div className="card-pad"><pre className="snippet" style={{ marginTop: 0 }}>{JSON.stringify(example, null, 2)}</pre></div>
      </div>
    </div>
  )
}
