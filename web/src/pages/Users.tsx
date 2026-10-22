import { FormEvent, useEffect, useMemo, useState } from 'react'
import { del, get, Group, patch, post, Template, User, UserDetails } from '../api'
import { UserExtras } from '../extras'
import { ago, bytes, clientTypes, date, dateTime, fromDateInput, GiB, isOnline, left, resetLabels, statusInfo, toDateInput } from '../format'
import { Badge, Bar, ChartLegend, CopyField, Drawer, Empty, Field, Icon, Loading, Modal, Switch, TrafficChart, useAction, useConfirm, useLoad } from '../ui'

const pageSize = 200

// Колонки, по которым сортирует сервер (список идёт страницами).
type SortKey = 'username' | 'status' | 'traffic' | 'expire' | 'groups' | 'online' | 'created'
const columns: [SortKey, string, string][] = [
  ['username', 'Имя', ''], ['status', 'Статус', ''], ['traffic', 'Трафик', 'hide-sm'], ['expire', 'Срок', ''],
  ['groups', 'Группы', 'hide-sm'], ['online', 'Был онлайн', 'hide-sm'], ['created', 'Создан', 'hide-sm'],
]

function savedSort(): { key: SortKey | ''; desc: boolean } {
  try {
    const v = JSON.parse(localStorage.getItem('vynel.users.sort') || '')
    if (v && typeof v.key === 'string') return v
  } catch { /* по умолчанию */ }
  return { key: '', desc: false }
}

function openId(): number | null {
  const m = location.hash.match(/^#\/users\/(\d+)/)
  return m ? Number(m[1]) : null
}

export function Users() {
  const [sort, setSort] = useState(savedSort)
  const [search, setSearch] = useState('')
  const [creating, setCreating] = useState(false)
  const [open, setOpen] = useState<number | null>(openId)
  const [page, setPage] = useState(0)
  useEffect(() => setPage(0), [sort, search])
  useEffect(() => {
    try { localStorage.setItem('vynel.users.sort', JSON.stringify(sort)) } catch { /* приватное окно */ }
  }, [sort])
  // Страницами: с десятками тысяч пользователей весь список разом тормозил и панель, и браузер.
  const list = useLoad(() => get<{ users: User[]; total: number }>(`users?search=${encodeURIComponent(search)}&sort=${sort.key}&dir=${sort.desc ? 'desc' : 'asc'}` +
    `&limit=${pageSize}&offset=${page * pageSize}`), [sort, search, page], 20000)
  const users = { ...list, data: list.data?.users }
  const total = list.data?.total ?? 0
  const groups = useLoad(() => get<Group[]>('groups'))
  const groupName = useMemo(() => new Map((groups.data || []).map((g) => [g.id, g.name])), [groups.data])

  useEffect(() => {
    const h = () => setOpen(openId())
    window.addEventListener('hashchange', h)
    return () => window.removeEventListener('hashchange', h)
  }, [])
  const show = (id: number | null) => {
    history.replaceState(null, '', id ? `#/users/${id}` : '#/users')
    setOpen(id)
  }
  // Первое нажатие — по возрастанию, второе — по убыванию, третье — сброс.
  const toggle = (k: SortKey) => setSort(sort.key !== k ? { key: k, desc: false } : !sort.desc ? { key: k, desc: true } : { key: '', desc: false })

  return (
    <>
      <div className="toolbar">
        <div className="grow search-box">
          <input className="input" placeholder="Поиск по имени или заметке" value={search} onChange={(e) => setSearch(e.target.value)} />
          <span className="muted search-icon"><Icon name="search" size={18} /></span>
        </div>
        <button className="btn primary" onClick={() => setCreating(true)}><Icon name="plus" /> <span className="hide-sm">Пользователь</span></button>
      </div>

      <div className="card">
        {!users.data ? <Loading error={users.error} /> : users.data.length === 0 ? (
          <Empty icon="🌱">{search ? 'Никого не нашлось' : <>Пользователей пока нет.<br />Нажмите «Пользователь» — достаточно ввести имя.</>}</Empty>
        ) : (
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  {columns.map(([k, label, cls]) => (
                    <th key={k} className={'sortable ' + cls + (sort.key === k ? ' sorted' : '')} onClick={() => toggle(k)} aria-sort={sort.key === k ? (sort.desc ? 'descending' : 'ascending') : 'none'}>
                      {label}<span className="sort-mark">{sort.key === k ? (sort.desc ? ' ↓' : ' ↑') : ' ↕'}</span>
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {users.data.map((u) => {
                  const st = statusInfo[u.status]
                  return (
                    <tr key={u.id} className={'click' + (open === u.id ? ' selected' : '')} onClick={() => show(u.id)}>
                      <td>
                        <div className="row" style={{ gap: 8, flexWrap: 'nowrap' }}>
                          {isOnline(u.onlineAt) && <span className="online-dot" title="онлайн" />}
                          <b>{u.username}</b>
                        </div>
                        {u.note && <div className="muted small ellipsis" style={{ maxWidth: 220 }}>{u.note}</div>}
                      </td>
                      <td><Badge color={st.color} dot>{st.label}</Badge></td>
                      <td className="hide-sm" style={{ minWidth: 150 }}>
                        <div className="small text-2">{bytes(u.trafficUsed)} / {bytes(u.trafficLimit)}</div>
                        {u.trafficLimit != null && <Bar value={u.trafficUsed} max={u.trafficLimit} />}
                      </td>
                      <td className="nowrap">
                        <div>{u.expireAt ? date(u.expireAt) : '∞'}</div>
                        <div className={'small ' + (u.expireAt && u.expireAt - Date.now() / 1000 < 7 * 86400 ? 'pink-text' : 'muted')}>{left(u.expireAt)}</div>
                      </td>
                      <td className="small text-2 hide-sm">{u.groupIds.map((g) => groupName.get(g)).filter(Boolean).join(', ') || '—'}</td>
                      <td className="small muted nowrap hide-sm">{isOnline(u.onlineAt) ? <span className="miku-text">сейчас</span> : ago(u.onlineAt)}</td>
                      <td className="small muted nowrap hide-sm">{date(u.createdAt)}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
        {total > pageSize && (
          <div className="row between card-pad small text-2">
            <span>{page * pageSize + 1}–{Math.min(total, (page + 1) * pageSize)} из {total}</span>
            <div className="row" style={{ gap: 6 }}>
              <button className="btn sm ghost" disabled={page === 0} onClick={() => setPage(page - 1)}>← Назад</button>
              <button className="btn sm ghost" disabled={(page + 1) * pageSize >= total} onClick={() => setPage(page + 1)}>Дальше →</button>
            </div>
          </div>
        )}
      </div>

      {creating && <CreateUser groups={groups.data || []} onClose={() => setCreating(false)} onCreated={(u) => { setCreating(false); users.reload(); show(u.id) }} />}
      {open && <UserDrawer id={open} groups={groups.data || []} onClose={() => show(null)} onChanged={users.reload} />}
    </>
  )
}

// Число из поля: пусто — «как в шаблоне» (null).
const num = (v: string): number | null => (v.trim() === '' ? null : Math.max(0, Number(v.replace(',', '.')) || 0))

function CreateUser({ groups, onClose, onCreated }: { groups: Group[]; onClose: () => void; onCreated: (u: User) => void }) {
  const templates = useLoad(() => get<Template[]>('templates'))
  const [username, setUsername] = useState('')
  const [templateId, setTemplateId] = useState(0)
  const [note, setNote] = useState('')
  const [ownGroups, setOwnGroups] = useState<number[] | null>(null)
  const [subToken, setSubToken] = useState('')
  // Пустые поля берутся из шаблона: он показан в них серым.
  const [months, setMonths] = useState('')
  const [days, setDays] = useState('')
  const [never, setNever] = useState(false)
  const [traffic, setTraffic] = useState('')
  const [reset, setReset] = useState('')
  const [hwid, setHwid] = useState('')
  const [ip, setIp] = useState('')
  const session = useLoad(() => get<{ subBase: string; ipDefault: number; hwidDefault: number }>('session'))
  const act = useAction()
  const tpl = templates.data?.find((t) => (templateId ? t.id === templateId : t.isDefault))

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const body: Record<string, unknown> = { username, templateId, note, groupIds: ownGroups ?? undefined, subToken: subToken.trim() }
    if (never) body.neverExpires = true
    else if (num(months) != null || num(days) != null) {
      const d = new Date()
      d.setMonth(d.getMonth() + (num(months) ?? 0))
      d.setDate(d.getDate() + (num(days) ?? 0))
      body.expireAt = Math.floor(d.getTime() / 1000)
    }
    if (num(traffic) != null) body.trafficLimit = Math.round(num(traffic)! * GiB)
    if (reset) body.resetStrategy = reset
    if (num(hwid) != null) body.hwidLimit = num(hwid)
    if (num(ip) != null) body.ipLimit = num(ip)
    const u = await act(() => post<User>('users', body), `Пользователь ${username} создан`)
    if (u) onCreated(u)
  }
  const tplExpiry = tpl && (tpl.expireMonths || tpl.expireDays)
  const fromTpl = <span className="from-tpl">из шаблона</span>

  return (
    <Modal title="Новый пользователь" onClose={onClose}
      footer={<><button className="btn ghost" onClick={onClose}>Отмена</button><button className="btn primary" form="create-user" disabled={!username.trim()}>Создать</button></>}>
      <form id="create-user" onSubmit={submit}>
        <Field label="Имя" help="Латиница, цифры и _ . - @. Например vasya или ivan.petrov">
          <input className="input" autoFocus value={username} onChange={(e) => setUsername(e.target.value)} placeholder="vasya" />
        </Field>
        <Field label="Шаблон" help="Серым — значения шаблона. Не трогайте поле, и оно возьмётся из шаблона; введите своё — будет своё">
          <select className="input" value={templateId} onChange={(e) => setTemplateId(Number(e.target.value))}>
            <option value={0}>по умолчанию{templates.data?.find((t) => t.isDefault) ? ` (${templates.data.find((t) => t.isDefault)!.name})` : ''}</option>
            {templates.data?.filter((t) => !t.isDefault).map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
          </select>
        </Field>
        <div className="grid2 keep2">
          <Field label={<>Срок, месяцев {months === '' && !never && fromTpl}</>}>
            <input className="input" inputMode="numeric" disabled={never} value={months} onChange={(e) => setMonths(e.target.value.replace(/\D/g, ''))}
              placeholder={tplExpiry ? String(tpl!.expireMonths) : '—'} />
          </Field>
          <Field label={<>и дней {days === '' && !never && fromTpl}</>}>
            <input className="input" inputMode="numeric" disabled={never} value={days} onChange={(e) => setDays(e.target.value.replace(/\D/g, ''))}
              placeholder={tplExpiry ? String(tpl!.expireDays) : 'бессрочно'} />
          </Field>
        </div>
        <label className="check" style={{ marginTop: -6, marginBottom: 12 }}>
          <input type="checkbox" checked={never} onChange={(e) => setNever(e.target.checked)} /> бессрочно
        </label>
        <div className="grid2 keep2">
          <Field label={<>Трафик, ГБ {traffic === '' && fromTpl}</>} help="0 — без лимита">
            <input className="input" inputMode="decimal" value={traffic} onChange={(e) => setTraffic(e.target.value.replace(/[^\d.,]/g, ''))}
              placeholder={tpl?.trafficLimit ? String(+(tpl.trafficLimit / GiB).toFixed(2)) : 'без лимита'} />
          </Field>
          <Field label={<>Сброс трафика {reset === '' && fromTpl}</>}>
            <select className={'input' + (reset === '' ? ' placeholder' : '')} value={reset} onChange={(e) => setReset(e.target.value)}>
              <option value="">{resetLabels[tpl?.resetStrategy || 'no']}</option>
              {Object.entries(resetLabels).map(([k, l]) => <option key={k} value={k}>{l}</option>)}
            </select>
          </Field>
          <Field label={<>Устройств {hwid === '' && fromTpl}</>} help="0 — без лимита">
            <input className="input" inputMode="numeric" value={hwid} onChange={(e) => setHwid(e.target.value.replace(/\D/g, ''))}
              placeholder={tpl?.hwidLimit != null ? String(tpl.hwidLimit || '∞') : `общий: ${session.data?.hwidDefault || '∞'}`} />
          </Field>
          <Field label={<>Адресов сразу {ip === '' && <span className="from-tpl">общий лимит</span>}</>} help="0 — без лимита">
            <input className="input" inputMode="numeric" value={ip} onChange={(e) => setIp(e.target.value.replace(/\D/g, ''))}
              placeholder={String(session.data?.ipDefault || '∞')} />
          </Field>
        </div>
        <Field label={<>Группы {ownGroups === null && fromTpl}</>}>
          <div className="row">
            {groups.map((g) => {
              const on = (ownGroups ?? tpl?.groupIds ?? []).includes(g.id)
              return (
                <label key={g.id} className={'check' + (ownGroups === null ? ' muted' : '')}>
                  <input type="checkbox" checked={on} onChange={() => {
                    const cur = ownGroups ?? tpl?.groupIds ?? []
                    setOwnGroups(on ? cur.filter((x) => x !== g.id) : [...cur, g.id])
                  }} />
                  {g.name}
                </label>
              )
            })}
          </div>
        </Field>
        <Field label="Заметка">
          <input className="input" value={note} onChange={(e) => setNote(e.target.value)} placeholder={tpl?.note || 'например, телеграм @vasya'} />
        </Field>
        <Field label="Ссылка подписки" help={<>Пусто — случайная (её не подобрать). Своя: 8–64 символа, латиница, цифры, _ и -. Короткие слова легко угадать.</>}>
          <div className="link-input">
            <span className="mono muted">{session.data?.subBase || '…/s/'}</span>
            <input className="input mono" value={subToken} onChange={(e) => setSubToken(e.target.value.replace(/[^A-Za-z0-9_-]/g, ''))} placeholder="случайная" maxLength={64} />
          </div>
        </Field>
      </form>
    </Modal>
  )
}

export function templateSummary(t: Template, groups: Group[]): string {
  const parts: string[] = []
  if (t.expireMonths || t.expireDays) parts.push('срок ' + [t.expireMonths && `${t.expireMonths} мес`, t.expireDays && `${t.expireDays} дн`].filter(Boolean).join(' '))
  else parts.push('бессрочно')
  parts.push(t.trafficLimit ? `${bytes(t.trafficLimit)}, сброс: ${resetLabels[t.resetStrategy]}` : 'трафик без лимита')
  if (t.hwidLimit != null) parts.push(`устройств: ${t.hwidLimit || '∞'}`)
  const g = t.groupIds.map((id) => groups.find((x) => x.id === id)?.name).filter(Boolean)
  if (g.length) parts.push('группы: ' + g.join(', '))
  if (t.grants?.length) parts.push(`каскадов и подписок: ${t.grants.length}`)
  return parts.join(' · ')
}

function UserDrawer({ id, groups, onClose, onChanged }: { id: number; groups: Group[]; onClose: () => void; onChanged: () => void }) {
  const { data, error, reload } = useLoad(() => get<UserDetails>(`users/${id}`), [id], 20000)
  const act = useAction()
  const confirm = useConfirm()
  const [editing, setEditing] = useState(false)
  const [showQR, setShowQR] = useState(false)
  const [linkEdit, setLinkEdit] = useState<string | null>(null)

  const run = async (fn: () => Promise<unknown>, ok: string) => {
    const r = await act(fn, ok)
    if (r !== undefined) {
      reload()
      onChanged()
    }
  }

  if (!data) return <Drawer title="…" onClose={onClose}><Loading error={error} /></Drawer>
  const u = data.user
  const st = statusInfo[u.status]

  return (
    <Drawer title={<>{u.username} <Badge color={st.color} dot>{st.label}</Badge></>} onClose={onClose}
      actions={<button className="btn sm" onClick={() => setEditing(true)}><Icon name="edit" /> Изменить</button>}>

      <div className="card card-pad">
        <div className="label" style={{ marginBottom: 8 }}>Ссылка подписки — отправьте её пользователю</div>
        {data.subUrl ? (
          <>
            <CopyField value={data.subUrl} />
            {linkEdit !== null && (
              <div className="link-input" style={{ marginTop: 10 }}>
                <span className="mono muted">{data.subUrl.slice(0, data.subUrl.length - (data.subUrl.split('/').pop() || '').length)}</span>
                <input className="input mono" autoFocus value={linkEdit} maxLength={64}
                  onChange={(e) => setLinkEdit(e.target.value.replace(/[^A-Za-z0-9_-]/g, ''))} />
                <button className="btn sm primary" disabled={linkEdit.length < 8} onClick={async () => {
                  if (!(await confirm('Старая ссылка перестанет работать: пользователю нужно будет добавить новую.', 'Сменить'))) return
                  await run(() => post(`users/${u.id}/link`, { token: linkEdit }), 'Ссылка изменена')
                  setLinkEdit(null)
                }}>Сохранить</button>
                <button className="btn sm ghost" onClick={() => setLinkEdit(null)}>Отмена</button>
              </div>
            )}
            <div className="row" style={{ marginTop: 10 }}>
              <button className="btn sm" onClick={() => setShowQR(!showQR)}>{showQR ? 'Скрыть QR' : 'Показать QR'}</button>
              {linkEdit === null && <button className="btn sm" onClick={() => setLinkEdit(data.subUrl!.split('/').pop() || '')}><Icon name="edit" /> Своя ссылка</button>}
              <a className="btn sm" href={data.subUrl} target="_blank" rel="noreferrer"><Icon name="link" /> Открыть страницу</a>
            </div>
            {showQR && <img className="qr" style={{ marginTop: 12 }} src={`api/qr?text=${encodeURIComponent(data.subUrl)}`} alt="QR" />}
            <div className="muted small" style={{ marginTop: 10 }}>
              На телефоне ссылка открывает страницу с QR-кодом и кнопками приложений. Её же можно вставить в приложение как подписку.
            </div>
          </>
        ) : <div className="alert amber">Ссылки нет: {data.subError}. Проверьте «Домен подписок» в настройках.</div>}
      </div>

      <div className="card card-pad">
        <div className="kv">
          <div className="k">Срок</div>
          <div>{u.expireAt ? <>{dateTime(u.expireAt)} <span className="muted">({left(u.expireAt)})</span></> : 'бессрочно'}</div>
          <div className="k">Трафик</div>
          <div>
            {bytes(u.trafficUsed)} из {bytes(u.trafficLimit)} <span className="muted">· сброс: {resetLabels[u.resetStrategy] || u.resetStrategy}</span>
            {u.trafficLimit != null && <div style={{ marginTop: 6 }}><Bar value={u.trafficUsed} max={u.trafficLimit} /></div>}
          </div>
          <div className="k">Всего за всё время</div><div>{bytes(u.lifetimeUsed)}</div>
          <div className="k">Устройства</div><div>{u.hwidOff
            ? <span className="amber-text">HWID не проверяется — пускает любое приложение</span>
            : <>{data.devices.length} из {(u.hwidLimit ?? data.hwidDefault) || '∞'} {u.hwidLimit == null && <span className="muted">(по умолчанию)</span>}</>}</div>
          <div className="k">Адреса сразу</div><div>
            {data.ips.length} из {(u.ipLimit ?? data.ipDefault) || '∞'} {u.ipLimit == null && <span className="muted">(по умолчанию)</span>}
            {data.ips.length > 0 && <div className="row small" style={{ marginTop: 6, gap: 6 }}>
              {data.ips.map((a) => (
                <span key={a.addr} className={'chip static' + (a.blocked ? ' pink-text' : '')}
                  title={a.blocked ? 'сверх лимита — заблокирован на нодах' : a.admitted ? `подключён с ${dateTime(a.since)}` : 'проверяется (короткие подключения не считаются)'}>
                  {a.blocked ? '⛔ ' : a.admitted ? '' : '… '}<span className="mono">{a.addr}</span>
                </span>
              ))}
            </div>}
          </div>
          <div className="k">Группы</div><div>{u.groupIds.map((g) => groups.find((x) => x.id === g)?.name).filter(Boolean).join(', ') || <span className="pink-text">нет — серверов в подписке не будет</span>}</div>
          <div className="k">Онлайн</div><div>{isOnline(u.onlineAt) ? <span className="miku-text">сейчас</span> : ago(u.onlineAt)}</div>
          <div className="k">Подписка обновлялась</div><div>{ago(u.subLastAt)} {u.subLastUA && <span className="muted small">· {u.subLastUA}</span>}</div>
          <div className="k">Формат</div><div>{clientTypes[u.clientType] || u.clientType}</div>
          {u.note && <><div className="k">Заметка</div><div>{u.note}</div></>}
          <div className="k">Создан</div><div>{dateTime(u.createdAt)}</div>
        </div>
      </div>

      <div className="card card-pad">
        <div className="label" style={{ marginBottom: 10 }}>Действия</div>
        <div className="row">
          <span className="text-2 small">Продлить:</span>
          {[1, 3, 6, 12].map((m) => (
            <button key={m} className="btn sm primary" onClick={() => run(() => post(`users/${id}/extend`, { months: m }), `Продлено на ${m} мес`)}>+{m} мес</button>
          ))}
        </div>
        <div className="row" style={{ marginTop: 10 }}>
          {u.enabled
            ? <button className="btn sm" onClick={() => run(() => patch(`users/${id}`, { enabled: false }), 'Отключён')}>Отключить</button>
            : <button className="btn sm primary" onClick={() => run(() => patch(`users/${id}`, { enabled: true }), 'Включён')}>Включить</button>}
          <button className="btn sm" onClick={() => run(() => post(`users/${id}/reset`), 'Трафик сброшен')}>Сбросить трафик</button>
          <button className="btn sm" onClick={async () => {
            if (await confirm('Старая ссылка подписки перестанет работать. Пользователю нужно будет добавить новую.', 'Выпустить новую'))
              run(() => post(`users/${id}/reissue`, { token: true }), 'Новая ссылка выпущена')
          }}>Новая ссылка</button>
          <button className="btn sm" onClick={async () => {
            if (await confirm('Сменить UUID: все устройства отключатся, пока не обновят подписку. Нужно, если ключ утёк.', 'Сменить'))
              run(() => post(`users/${id}/reissue`, { uuid: true }), 'UUID сменён')
          }}>Сменить UUID</button>
          <button className="btn sm danger" onClick={async () => {
            if (await confirm(<>Удалить пользователя <b>{u.username}</b>? Это нельзя отменить.</>, 'Удалить')) {
              const r = await act(() => del(`users/${id}`), 'Пользователь удалён')
              if (r !== undefined) { onChanged(); onClose() }
            }
          }}><Icon name="trash" /> Удалить</button>
        </div>
      </div>

      <div className="card">
        <div className="card-head">
          <div>
            <h3>Устройства (HWID)</h3>
            <div className="card-sub">{u.hwidOff ? 'проверка выключена для этого пользователя' : `${data.devices.length} из ${(u.hwidLimit ?? data.hwidDefault) || '∞'}`}</div>
          </div>
          <label className="row small text-2" style={{ gap: 8, flexWrap: 'nowrap' }} title="Выключите для приложений без HWID — подписка будет выдаваться без проверки устройств">
            Проверять HWID
            <Switch checked={!u.hwidOff} onChange={(v) => run(() => patch(`users/${id}`, { hwidOff: !v }), v ? 'Проверка HWID включена' : 'HWID для пользователя выключен')} />
          </label>
        </div>
        {data.devices.length === 0 ? <Empty>Устройства появятся после первого обновления подписки в приложении</Empty> : (
          <div className="table-wrap">
            <table className="table">
              <tbody>
                {data.devices.map((d) => (
                  <tr key={d.id}>
                    <td>
                      <div>{d.model || 'устройство'} <span className="muted small">{d.platform} {d.osVersion}</span></div>
                      <div className="muted small ellipsis" style={{ maxWidth: 300 }}>{d.userAgent}</div>
                    </td>
                    <td className="small muted nowrap">{ago(d.lastSeen)}<br />{d.lastIp}</td>
                    <td style={{ width: 40 }}>
                      <button className="btn ghost icon sm" title="Отвязать" onClick={async () => {
                        if (await confirm('Отвязать устройство? Место освободится; если приложение снова обновит подписку, устройство добавится заново.', 'Отвязать'))
                          run(() => del(`users/${id}/devices/${d.id}`), 'Устройство отвязано')
                      }}><Icon name="trash" /></button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      <div className="card">
        <div className="card-head"><h3>Трафик за 30 дней</h3><ChartLegend /></div>
        <div className="card-pad">
          <TrafficChart days={data.daily} />
          {data.trafficByNode.length > 0 && (
            <div className="row small text-2" style={{ marginTop: 8 }}>
              {data.trafficByNode.map((t) => <span key={t.code} className="chip static">{t.code}: {bytes(t.bytes)}</span>)}
            </div>
          )}
        </div>
      </div>

      <UserExtras userId={id} grants={data.grants} groups={groups} onChanged={reload} />

      <div className="card">
        <div className="card-head"><h3>Серверы в подписке</h3><span className="card-sub">отдельные ссылки — для ручного импорта</span></div>
        <div className="card-pad">
          {data.links.length === 0 ? <div className="muted">Нет доступных серверов: проверьте группы пользователя и доступы групп.</div> :
            data.links.map((l, i) => (
              <div key={i} style={{ marginBottom: 12 }}>
                <div className="small text-2" style={{ marginBottom: 5 }}>{l.remark} <span className="muted">· {l.tag}{l.hidden ? ' · скрыт' : ''}</span></div>
                <CopyField value={l.link} />
              </div>
            ))}
        </div>
      </div>

      {editing && <EditUser u={u} hwidDefault={data.hwidDefault} ipDefault={data.ipDefault} groups={groups} onClose={() => setEditing(false)}
        onSaved={() => { setEditing(false); reload(); onChanged() }} />}
    </Drawer>
  )
}

function EditUser({ u, hwidDefault, ipDefault, groups, onClose, onSaved }: { u: User; hwidDefault: number; ipDefault: number; groups: Group[]; onClose: () => void; onSaved: () => void }) {
  const [expire, setExpire] = useState(toDateInput(u.expireAt))
  const [limitGB, setLimitGB] = useState(u.trafficLimit ? String(Math.round((u.trafficLimit / GiB) * 100) / 100) : '')
  const [hwid, setHwid] = useState(u.hwidLimit == null ? '' : String(u.hwidLimit))
  const [ipLimit, setIPLimit] = useState(u.ipLimit == null ? '' : String(u.ipLimit))
  const [reset, setReset] = useState(u.resetStrategy)
  const [clientType, setClientType] = useState(u.clientType)
  const [note, setNote] = useState(u.note)
  const [groupIds, setGroupIds] = useState(u.groupIds)
  const act = useAction()

  const save = async (e: FormEvent) => {
    e.preventDefault()
    const exp = fromDateInput(expire)
    const body: any = {
      note, clientType, resetStrategy: reset, groupIds,
      hwidLimit: hwid === '' ? -1 : Number(hwid),
      ipLimit: ipLimit === '' ? -1 : Number(ipLimit),
      setTrafficLimit: true, trafficLimit: limitGB ? Math.round(Number(limitGB) * GiB) : 0,
    }
    if (exp) body.expireAt = exp
    else body.neverExpires = true
    const r = await act(() => patch(`users/${u.id}`, body), 'Сохранено')
    if (r !== undefined) onSaved()
  }

  return (
    <Modal title={`Изменить ${u.username}`} onClose={onClose}
      footer={<><button className="btn ghost" onClick={onClose}>Отмена</button><button className="btn primary" form="edit-user">Сохранить</button></>}>
      <form id="edit-user" onSubmit={save}>
        <div className="grid2 keep2">
          <Field label="Действует до" help="Пусто — бессрочно">
            <input className="input" type="date" value={expire} onChange={(e) => setExpire(e.target.value)} />
          </Field>
          <Field label="Лимит трафика, ГБ" help="Пусто — без лимита">
            <input className="input" inputMode="decimal" value={limitGB} onChange={(e) => setLimitGB(e.target.value.replace(',', '.'))} placeholder="∞" />
          </Field>
          <Field label="Сброс трафика">
            <select className="input" value={reset} onChange={(e) => setReset(e.target.value)}>
              {Object.entries(resetLabels).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
            </select>
          </Field>
          <Field label="Лимит устройств" help={`Пусто — по умолчанию (${hwidDefault || '∞'}), 0 — без лимита`}>
            <input className="input" inputMode="numeric" value={hwid} onChange={(e) => setHwid(e.target.value.replace(/\D/g, ''))} placeholder={String(hwidDefault)} />
          </Field>
          <Field label="Лимит IP одновременно" help={`Пусто — по умолчанию (${ipDefault || '∞'}), 0 — без лимита`}>
            <input className="input" inputMode="numeric" value={ipLimit} onChange={(e) => setIPLimit(e.target.value.replace(/\D/g, ''))} placeholder={String(ipDefault)} />
          </Field>
        </div>
        <Field label="Формат подписки" help="«Авто» определяет формат по приложению — обычно менять не нужно">
          <select className="input" value={clientType} onChange={(e) => setClientType(e.target.value)}>
            {Object.entries(clientTypes).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
          </select>
        </Field>
        <Field label="Группы">
          <div className="row">
            {groups.map((g) => (
              <label key={g.id} className="check">
                <input type="checkbox" checked={groupIds.includes(g.id)}
                  onChange={(e) => setGroupIds(e.target.checked ? [...groupIds, g.id] : groupIds.filter((x) => x !== g.id))} />
                {g.name}
              </label>
            ))}
          </div>
        </Field>
        <Field label="Заметка">
          <input className="input" value={note} onChange={(e) => setNote(e.target.value)} />
        </Field>
      </form>
    </Modal>
  )
}
