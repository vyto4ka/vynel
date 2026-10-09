import { useState } from 'react'
import { get, Network, NetRole, post, put, Setting } from '../api'
import { Badge, Field, Loading, Switch, useAction, useConfirm, useLoad } from '../ui'

export function Settings() {
  const { data, error, reload } = useLoad(() => get<Setting[]>('settings'))
  if (!data) return <Loading error={error} />
  const sections = [...new Set(data.map((s) => s.section))]
  return (
    <div className="cards" style={{ maxWidth: 900 }}>
      <Stealth settings={data} />
      <NetworkCard />
      {sections.map((sec) => (
        <div key={sec} className="card">
          <div className="card-head"><h3>{sec}</h3></div>
          <div className="card-pad">
            {data.filter((s) => s.section === sec).map((s) => <SettingRow key={s.key + (s.value ?? '')} s={s} onSaved={reload} />)}
          </div>
        </div>
      ))}
    </div>
  )
}

const value = (all: Setting[], key: string) => {
  const s = all.find((x) => x.key === key)
  return s ? (s.set ? s.value ?? '' : s.default ?? '') : ''
}

// Сводка скрытия (docs/STEALTH.md §1) и быстрый переезд панели на случайный порт и путь.
function Stealth({ settings }: { settings: Setting[] }) {
  const act = useAction()
  const confirm = useConfirm()
  const webPort = value(settings, 'web.port')
  const subPort = value(settings, 'sub.port') || '443'
  const ownPort = webPort !== '' && webPort !== subPort
  const pageOn = value(settings, 'sub.page_enabled') !== 'false'
  const pwOn = value(settings, 'web.password_login') !== 'false'
  const items: [boolean, string, string][] = [
    [true, 'Секретный путь', 'всё вне пути — сайт-заглушка'],
    [ownPort, 'Свой порт панели', ownPort ? `:${webPort}` : `на общем :${subPort}`],
    [!pageOn, 'Страница подписки скрыта', pageOn ? 'браузер видит страницу подписки' : 'браузер видит заглушку'],
    [!pwOn, 'Вход только через Telegram', pwOn ? 'форма логина включена' : 'форма логина выключена'],
  ]
  const run = async () => {
    if (!(await confirm('Панель переедет на случайный порт 20000–60000 и новый секретный путь. Запишите новый адрес; если на сервере есть фаервол, откройте порт до этого. Вернуть порт: vynel admin setting web.port "" на сервере.', 'Переехать', false))) return
    const r = await act(() => post<{ webUrl: string }>('settings/stealth', { port: true, path: true }), 'Панель переехала')
    if (r?.webUrl) location.href = r.webUrl + '#/settings'
  }
  return (
    <div className="card">
      <div className="card-head">
        <div><h3>Скрытие</h3><div className="card-sub">что видит посторонний, который нашёл сервер</div></div>
        <button className="btn sm" onClick={run}>Случайный порт и новый путь</button>
      </div>
      <div className="card-pad stealth-grid">
        {items.map(([on, title, sub]) => (
          <div key={title} className={'stealth-item' + (on ? ' on' : '')}>
            <span className="dot" />
            <div><div>{title}</div><div className="muted small">{sub}</div></div>
          </div>
        ))}
      </div>
    </div>
  )
}

const roleNames: Record<NetRole['kind'], string> = { panel: 'панель', sub: 'подписки', gateway: 'шлюз нод', inbound: 'вход', egress: 'выход' }
const roleColors: Record<NetRole['kind'], string> = { panel: 'pink', sub: 'pink', gateway: 'amber', inbound: 'green', egress: '' }
const purposeNames = { sub: 'подписки', panel: 'панель', node: 'нода' }

function RoleChips({ roles }: { roles: NetRole[] }) {
  if (roles.length === 0) return <span className="muted small">не используется</span>
  return <>{roles.map((r, i) => <Badge key={i} color={roleColors[r.kind]}>{roleNames[r.kind]}{r.tag ? ` ${r.tag}` : ''}</Badge>)}</>
}

function NetworkCard() {
  const { data, error, reload } = useLoad(() => get<Network>('network'))
  return (
    <div className="card">
      <div className="card-head">
        <div><h3>Адреса и роли</h3><div className="card-sub">какой IP что обслуживает и куда смотрят домены</div></div>
        <button className="btn ghost sm" onClick={reload}>Проверить DNS снова</button>
      </div>
      {!data ? <div className="card-pad"><Loading error={error} /></div> : (
        <div className="card-pad">
          {data.domains.length > 0 && (
            <div className="dns-list">
              {data.domains.map((d) => (
                <div key={d.name} className="dns-row">
                  <Badge color={d.ok ? 'green' : 'red'} dot>{d.ok ? 'DNS в порядке' : d.error || 'ошибка'}</Badge>
                  <span className="mono">{d.name}</span>
                  <span className="muted small">{purposeNames[d.purpose]}{d.purpose === 'node' ? ` ${d.node}` : ''}</span>
                  <span className="small text-2 grow" style={{ textAlign: 'right' }}>
                    {d.resolved.length > 0 ? <>→ <span className="mono">{d.resolved.join(', ')}</span></> : null}
                    {!d.ok && d.expected.length > 0 && <> · нужно <span className="mono">{d.expected.join(' или ')}</span></>}
                  </span>
                </div>
              ))}
              <div className="muted small" style={{ marginTop: 6 }}>Домен за CDN (оранжевое облако Cloudflare, VK CDN) указывает на CDN — это нормально, если так задумано.</div>
            </div>
          )}
          {data.nodes.map((n) => (
            <div key={n.id} className="net-node">
              <div className="net-node-head"><b>{n.name}</b> <span className="muted mono small">{n.code}</span> {n.local && <Badge>сервер панели</Badge>}</div>
              {n.allRoles.length > 0 && <div className="small text-2" style={{ margin: '4px 0 8px' }}>На всех адресах: <RoleChips roles={n.allRoles} /></div>}
              {n.addresses.length === 0 ? <div className="muted small">Нода ещё не прислала свои адреса</div> : (
                <table className="table compact">
                  <tbody>
                    {n.addresses.map((a) => (
                      <tr key={a.id}>
                        <td className="mono nowrap" style={{ width: 170 }}>{a.ip}{a.primary && <span className="muted"> · основной</span>}</td>
                        <td className="small muted nowrap" style={{ width: 90 }}>{a.interface || '—'}{!a.onInterface && <div className="pink-text">нет на интерфейсе</div>}</td>
                        <td><div className="row" style={{ gap: 4 }}><RoleChips roles={a.roles} /></div></td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          ))}
          <div className="muted small">Панель и подписки выбирают IP ниже, в разделах «Доступ к панели» и «Подписки». Вход и выход инбаунда — в «Ноды» → «Настроить».</div>
        </div>
      )}
    </div>
  )
}

function SettingRow({ s, onSaved }: { s: Setting; onSaved: () => void }) {
  const initial = s.value ?? ''
  const [val, setValue] = useState(initial)
  const act = useAction()
  const confirm = useConfirm()
  const dirty = val !== initial
  const effective = s.set ? initial : s.default ?? ''
  const ip = s.key === 'web.address' || s.key === 'sub.address'

  const save = async (v: string) => {
    if (s.movesPanel && !(await confirm('Панель переедет на новый адрес. Запишите его — старый перестанет работать. Если адрес окажется недоступен, на сервере поможет vynel admin web.', 'Сменить', false))) return
    const r = await act(() => put<{ webUrl: string }>('settings', { key: s.key, value: v }), 'Сохранено')
    if (r === undefined) return
    if (s.movesPanel && r.webUrl && r.webUrl !== location.origin + location.pathname) {
      location.href = r.webUrl + '#/settings'
      return
    }
    onSaved()
  }

  if (s.type === 'bool') {
    const on = effective === 'true'
    return (
      <div className="field">
        <div className="row between" style={{ flexWrap: 'nowrap' }}>
          <div>
            <div>{s.title}</div>
            {s.help && <div className="help muted small">{s.help}</div>}
          </div>
          <Switch checked={on} onChange={(v) => save(v ? 'true' : 'false')} />
        </div>
      </div>
    )
  }
  const defaultLabel = ip ? (s.key === 'web.address' ? 'как у подписок' : 'все адреса') : `по умолчанию${s.default ? ` (${s.default})` : ''}`
  return (
    <Field label={<>{s.title} <span className="muted mono" style={{ fontWeight: 400 }}>{s.key}</span></>} help={s.help}>
      <div className="row" style={{ flexWrap: 'nowrap' }}>
        {s.type === 'select' ? (
          <select className="input" value={val} onChange={(e) => setValue(e.target.value)}>
            <option value="">{defaultLabel}</option>
            {s.options?.map((o) => <option key={o} value={o}>{o}</option>)}
          </select>
        ) : (
          <input className="input" value={val} inputMode={s.type === 'int' ? 'numeric' : undefined}
            onChange={(e) => setValue(e.target.value)} placeholder={s.default ? `по умолчанию ${s.default}` : ''}
            onKeyDown={(e) => e.key === 'Enter' && dirty && save(val)} />
        )}
        {dirty && <button className="btn primary" onClick={() => save(val)}>Сохранить</button>}
        {dirty && <button className="btn ghost" onClick={() => setValue(initial)}>Отмена</button>}
      </div>
    </Field>
  )
}
