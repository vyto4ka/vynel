import { Fragment, useEffect, useMemo, useState } from 'react'
import { get, post, put, SubApp, SubBasics, SubConfig, SubHeader, SubPage, SubRule, User } from '../api'
import { Badge, Field, Icon, Loading, Modal, Switch, useAction, useConfirm, useLoad } from '../ui'

const tabs: [string, string][] = [
  ['basics', 'Основное'],
  ['page', 'Страница'],
  ['apps', 'Приложения'],
  ['headers', 'Заголовки'],
  ['rules', 'Форматы'],
  ['test', 'Проверка'],
]

const platformNames: Record<string, string> = { android: 'Android', ios: 'iOS', windows: 'Windows', macos: 'macOS', linux: 'Linux' }

const formatNames: Record<string, string> = {
  base64: 'ссылки vless:// (base64)', mihomo: 'Clash / Mihomo YAML', singbox: 'sing-box JSON', xray: 'Xray JSON', html: 'страница для браузера',
}

// base64url для черновика в адресе предпросмотра
function encodeDraft(v: unknown): string {
  const bytes = new TextEncoder().encode(JSON.stringify(v))
  let s = ''
  bytes.forEach((b) => (s += String.fromCharCode(b)))
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

export function Subscription() {
  const cfg = useLoad(() => get<SubConfig>('subscription'))
  const users = useLoad(() => get<User[]>('users'))
  const [tab, setTab] = useState('basics')
  const [basics, setBasics] = useState<SubBasics | null>(null)
  const [page, setPage] = useState<SubPage | null>(null)
  const [headers, setHeaders] = useState<SubHeader[] | null>(null)
  const [rules, setRules] = useState<SubRule[] | null>(null)
  const [userId, setUserId] = useState(0)
  const act = useAction()
  const confirm = useConfirm()

  useEffect(() => {
    if (cfg.data) {
      setBasics(cfg.data.basics)
      setPage(cfg.data.page)
      setHeaders(cfg.data.headers)
      setRules(cfg.data.rules)
    }
  }, [cfg.data])

  const dirty = useMemo(() => {
    if (!cfg.data || !basics || !page || !headers || !rules) return false
    return JSON.stringify(basics) !== JSON.stringify(cfg.data.basics) || JSON.stringify(page) !== JSON.stringify(cfg.data.page) ||
      JSON.stringify(headers) !== JSON.stringify(cfg.data.headers) || JSON.stringify(rules) !== JSON.stringify(cfg.data.rules)
  }, [cfg.data, basics, page, headers, rules])

  // Предпросмотр обновляется с небольшой задержкой после правок.
  const [previewSrc, setPreviewSrc] = useState('')
  useEffect(() => {
    if (!basics || !page) return
    const t = setTimeout(() => setPreviewSrc(`api/subscription/preview?user=${userId}&draft=${encodeDraft({ basics, page })}`), 400)
    return () => clearTimeout(t)
  }, [basics, page, userId])

  if (!cfg.data || !basics || !page || !headers || !rules) return <Loading error={cfg.error} />
  const c = cfg.data

  const save = async () => {
    const r = await act(() => put<SubConfig>('subscription', { basics, page, headers, rules }), 'Сохранено — приложения получат изменения при следующем обновлении')
    if (r) cfg.setData(r)
  }
  const reset = async (part: 'headers' | 'page' | 'rules') => {
    const q = { headers: 'Вернуть заголовки по умолчанию? Ваши правки заголовков пропадут.', page: 'Вернуть страницу и приложения по умолчанию?', rules: 'Вернуть правила форматов по умолчанию?' }
    if (!(await confirm(q[part], 'Вернуть'))) return
    const r = await act(() => post<SubConfig>('subscription/reset', { part }), 'Возвращено по умолчанию')
    if (r) cfg.setData(r)
  }
  const showPreview = tab === 'basics' || tab === 'page' || tab === 'apps'

  return (
    <>
      <div className="toolbar">
        <div className="seg">
          {tabs.map(([id, label]) => <button key={id} className={tab === id ? 'on' : ''} onClick={() => setTab(id)}>{label}</button>)}
        </div>
        <div className="grow" />
        {dirty && <span className="pink-text small">есть несохранённые изменения</span>}
        <button className="btn ghost" disabled={!dirty} onClick={() => { setBasics(c.basics); setPage(c.page); setHeaders(c.headers); setRules(c.rules) }}>Отменить</button>
        <button className="btn primary" disabled={!dirty} onClick={save}>Сохранить</button>
      </div>

      <div className={showPreview ? 'sub-split' : ''}>
        <div className="cards">
          {tab === 'basics' && <BasicsTab b={basics} set={setBasics} />}
          {tab === 'page' && <PageTab p={page} set={setPage} onReset={() => reset('page')} />}
          {tab === 'apps' && <AppsTab p={page} set={setPage} catalog={c.appCatalog} />}
          {tab === 'headers' && <HeadersTab hs={headers} set={setHeaders} cfg={c} onReset={() => reset('headers')} />}
          {tab === 'rules' && <RulesTab rules={rules} set={setRules} formats={c.formats} onReset={() => reset('rules')} />}
          {tab === 'test' && <TestTab headers={headers} basics={basics} users={users.data || []} rules={rules} dirty={dirty} />}
        </div>
        {showPreview && (
          <div className="card sub-preview">
            <div className="card-head">
              <h3>Предпросмотр</h3>
              <select className="input" style={{ width: 180, height: 30 }} value={userId} onChange={(e) => setUserId(Number(e.target.value))}>
                <option value={0}>первый пользователь</option>
                {(users.data || []).map((u) => <option key={u.id} value={u.id}>{u.username}</option>)}
              </select>
            </div>
            {previewSrc && <iframe title="Страница подписки" src={previewSrc} />}
          </div>
        )}
      </div>
    </>
  )
}

function BasicsTab({ b, set }: { b: SubBasics; set: (b: SubBasics) => void }) {
  const f = (k: keyof SubBasics) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) =>
    set({ ...b, [k]: k === 'updateHours' ? Number(e.target.value.replace(/\D/g, '')) || 0 : e.target.value })
  return (
    <div className="card card-pad">
      <Field label="Название подписки" help="Так подписка называется в приложениях (заголовок profile-title) и на странице">
        <input className="input" value={b.title} onChange={f('title')} placeholder="VPN" />
      </Field>
      <div className="grid2">
        <Field label="Обновлять, часов" help="profile-update-interval: как часто приложение само обновляет подписку">
          <input className="input" inputMode="numeric" value={b.updateHours || ''} onChange={f('updateHours')} placeholder="12" />
        </Field>
        <Field label="Поддержка" help="support-url: кнопка в приложениях и на странице">
          <input className="input" value={b.supportUrl} onChange={f('supportUrl')} placeholder="https://t.me/your_support" />
        </Field>
      </div>
      <Field label="Объявление" help="announce: строка вверху приложения (KeqDroid и другие) и плашка на странице. Пусто — не показывать">
        <textarea className="input" rows={2} value={b.announce} onChange={f('announce')} placeholder="Например: профилактика в субботу 03:00–04:00" />
      </Field>
      <Field label="Ссылка объявления" help="announce-url: куда ведёт нажатие на объявление">
        <input className="input" value={b.announceUrl} onChange={f('announceUrl')} placeholder="https://t.me/your_channel" />
      </Field>
      <Field label="Страница «открыть в браузере»" help="profile-web-page-url. Пусто — страница этой подписки (с QR и кнопками)">
        <input className="input" value={b.pageUrl} onChange={f('pageUrl')} placeholder="страница подписки" />
      </Field>
    </div>
  )
}

function PageTab({ p, set, onReset }: { p: SubPage; set: (p: SubPage) => void; onReset: () => void }) {
  const f = (k: keyof SubPage) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>) => set({ ...p, [k]: e.target.value })
  const accents = ['#39c5bb', '#ff5fa2', '#7c9cff', '#f2b84b', '#4ade80', '#e2e8f0']
  return (
    <div className="card card-pad">
      <div className="muted small" style={{ marginBottom: 14 }}>
        Эту страницу видит человек, открывший ссылку подписки в браузере: QR-код, кнопки приложений, трафик и срок. В тексте можно использовать переменные, например {'{username}'}, {'{expire_date}'}, {'{days_left}'}.
      </div>
      <Field label="Заголовок" help="Пусто — название подписки">
        <input className="input" value={p.heading} onChange={f('heading')} placeholder="как в «Основное»" />
      </Field>
      <Field label="Описание под заголовком">
        <textarea className="input" rows={2} value={p.description} onChange={f('description')} placeholder="Например: Привет, {username}! Подписка до {expire_date}." />
      </Field>
      <div className="grid2">
        <Field label="Тема">
          <select className="input" value={p.theme} onChange={f('theme')}>
            <option value="dark">тёмная</option>
            <option value="light">светлая</option>
            <option value="auto">как в системе</option>
          </select>
        </Field>
        <Field label="Акцентный цвет">
          <div className="row" style={{ flexWrap: 'nowrap' }}>
            <input type="color" value={p.accent} onChange={f('accent')} style={{ width: 44, height: 36, border: 0, background: 'none', padding: 0 }} />
            <input className="input mono" value={p.accent} onChange={f('accent')} />
          </div>
          <div className="row" style={{ gap: 6, marginTop: 6 }}>
            {accents.map((a) => <button key={a} type="button" title={a} onClick={() => set({ ...p, accent: a })}
              style={{ width: 22, height: 22, borderRadius: 6, border: '1px solid var(--border-2)', background: a, cursor: 'pointer' }} />)}
          </div>
        </Field>
      </div>
      <div className="row" style={{ gap: 22, marginBottom: 14 }}>
        <label className="check"><input type="checkbox" checked={p.showQr} onChange={(e) => set({ ...p, showQr: e.target.checked })} /> QR-код</label>
        <label className="check"><input type="checkbox" checked={p.showTraffic} onChange={(e) => set({ ...p, showTraffic: e.target.checked })} /> Статус, трафик и срок</label>
      </div>
      <Field label="Инструкция «Как подключиться»" help="Пусто — блок не показывается">
        <textarea className="input" rows={4} value={p.instructions} onChange={f('instructions')} />
      </Field>
      <Field label="Подвал">
        <input className="input" value={p.footer} onChange={f('footer')} placeholder="Например: © MikuVPN" />
      </Field>
      <button className="btn ghost sm" onClick={onReset}>Вернуть страницу по умолчанию</button>
    </div>
  )
}

function AppsTab({ p, set, catalog }: { p: SubPage; set: (p: SubPage) => void; catalog: SubApp[] }) {
  const [edit, setEdit] = useState<number | null>(null)
  const apps = p.apps
  const update = (i: number, a: SubApp) => set({ ...p, apps: apps.map((x, j) => (j === i ? a : x)) })
  const move = (i: number, d: number) => {
    const j = i + d
    if (j < 0 || j >= apps.length) return
    const next = [...apps]
    ;[next[i], next[j]] = [next[j], next[i]]
    set({ ...p, apps: next })
  }
  return (
    <div className="card">
      <div className="card-head">
        <div>
          <h3>Кнопки приложений</h3>
          <div className="card-sub">Первая включённая — «рекомендуем». На телефоне сначала показываются приложения для его системы.</div>
        </div>
        <button className="btn sm" onClick={() => { set({ ...p, apps: [...apps, { id: '', name: 'Новое приложение', link: 'app://import/{url}', download: '', platforms: ['android'], enabled: true }] }); setEdit(apps.length) }}>
          <Icon name="plus" /> Своё
        </button>
      </div>
      <div className="table-wrap">
        <table className="table">
          <tbody>
            {apps.map((a, i) => (
              <tr key={a.id + i}>
                <td style={{ width: 50 }}><Switch checked={a.enabled} onChange={(v) => update(i, { ...a, enabled: v })} /></td>
                <td>
                  <b>{a.name}</b> {a.id === 'keqdroid' && <Badge color="pink">рекомендуем</Badge>}
                  <div className="small muted mono ellipsis" style={{ maxWidth: 330 }}>{a.link}</div>
                  <div className="small text-2">{a.platforms.map((x) => platformNames[x] || x).join(', ')}{a.note && ` · ${a.note}`}</div>
                </td>
                <td className="nowrap" style={{ width: 110 }}>
                  <button className="btn ghost icon sm" title="Выше" onClick={() => move(i, -1)}>↑</button>
                  <button className="btn ghost icon sm" title="Ниже" onClick={() => move(i, 1)}>↓</button>
                  <button className="btn ghost icon sm" title="Изменить" onClick={() => setEdit(i)}><Icon name="edit" /></button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {edit !== null && apps[edit] && (
        <AppForm a={apps[edit]} known={catalog.some((x) => x.id === apps[edit].id)}
          onClose={() => setEdit(null)}
          onSave={(a) => { update(edit, a); setEdit(null) }}
          onDelete={() => { set({ ...p, apps: apps.filter((_, j) => j !== edit) }); setEdit(null) }} />
      )}
    </div>
  )
}

function AppForm({ a, known, onClose, onSave, onDelete }: { a: SubApp; known: boolean; onClose: () => void; onSave: (a: SubApp) => void; onDelete: () => void }) {
  const [v, setV] = useState(a)
  return (
    <Modal title={a.name} onClose={onClose}
      footer={<>{!known && <button className="btn danger" onClick={onDelete}>Удалить</button>}<div className="grow" />
        <button className="btn ghost" onClick={onClose}>Отмена</button><button className="btn primary" onClick={() => onSave(v)}>Готово</button></>}>
      <Field label="Название"><input className="input" value={v.name} onChange={(e) => setV({ ...v, name: e.target.value })} /></Field>
      <Field label="Ссылка «Добавить»" help="Переменные: {url} — ссылка подписки, {url_enc} — она же закодированная для ?url=, {url_b64} — в base64, {title_url} — название">
        <input className="input mono" value={v.link} onChange={(e) => setV({ ...v, link: e.target.value })} />
      </Field>
      <Field label="Где скачать (необязательно)"><input className="input" value={v.download} onChange={(e) => setV({ ...v, download: e.target.value })} placeholder="https://…" /></Field>
      <Field label="Подпись"><input className="input" value={v.note || ''} onChange={(e) => setV({ ...v, note: e.target.value })} /></Field>
      <Field label="Платформы">
        <div className="row">
          {Object.entries(platformNames).map(([k, n]) => (
            <label key={k} className="check">
              <input type="checkbox" checked={v.platforms.includes(k)} onChange={(e) => setV({ ...v, platforms: e.target.checked ? [...v.platforms, k] : v.platforms.filter((x) => x !== k) })} />
              {n}
            </label>
          ))}
        </div>
      </Field>
    </Modal>
  )
}

function HeadersTab({ hs, set, cfg, onReset }: { hs: SubHeader[]; set: (h: SubHeader[]) => void; cfg: SubConfig; onReset: () => void }) {
  const [edit, setEdit] = useState<number | null>(null)
  const [catalogOpen, setCatalogOpen] = useState(false)
  const update = (i: number, h: SubHeader) => set(hs.map((x, j) => (j === i ? h : x)))
  const presetFor = (h: SubHeader) => cfg.headerCatalog.find((p) => p.name.toLowerCase() === h.name.toLowerCase())
  return (
    <>
      <div className="card">
        <div className="card-head">
          <div>
            <h3>Заголовки ответа подписки</h3>
            <div className="card-sub">Их читают приложения: название, трафик, объявления и настройки отдельных приложений. Пустое значение — заголовок не отправляется.</div>
          </div>
          <div className="row" style={{ gap: 6 }}>
            <button className="btn sm" onClick={() => setCatalogOpen(true)}><Icon name="plus" /> Из каталога</button>
            <button className="btn sm" onClick={() => { set([...hs, { name: 'X-My-Header', value: '', base64: false, clients: '', enabled: true }]); setEdit(hs.length) }}>
              <Icon name="plus" /> Свой
            </button>
          </div>
        </div>
        <div className="table-wrap">
          <table className="table">
            <thead><tr><th></th><th>Заголовок</th><th>Значение</th><th>Кому</th><th></th></tr></thead>
            <tbody>
              {hs.length === 0 && <tr><td colSpan={5} className="muted">Заголовков нет — приложения не увидят ни названия, ни трафика</td></tr>}
              {hs.map((h, i) => {
                const p = presetFor(h)
                return (
                  <tr key={i}>
                    <td style={{ width: 50 }}><Switch checked={h.enabled} onChange={(v) => update(i, { ...h, enabled: v })} /></td>
                    <td className="nowrap">
                      <b className="mono">{h.name}</b>
                      {p && <div className="small muted" style={{ maxWidth: 260, whiteSpace: 'normal' }}>{p.description}</div>}
                    </td>
                    <td>
                      <span className="mono small">{h.value || <span className="muted">пусто</span>}</span>
                      {h.base64 && <> <Badge>base64</Badge></>}
                      {!h.base64 && /[^\x20-\x7e]/.test(h.value) && <div className="small pink-text">не латиница — включите base64, иначе часть приложений покажет кракозябры</div>}
                    </td>
                    <td className="small text-2">{h.clients ? <span className="mono">{h.clients}</span> : 'всем'}{p && <div className="muted">{p.apps}</div>}</td>
                    <td className="nowrap" style={{ width: 80 }}>
                      <button className="btn ghost icon sm" title="Изменить" onClick={() => setEdit(i)}><Icon name="edit" /></button>
                      <button className="btn ghost icon sm" title="Убрать" onClick={() => set(hs.filter((_, j) => j !== i))}><Icon name="trash" /></button>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      </div>

      <div className="card card-pad">
        <div className="label" style={{ marginBottom: 8 }}>Переменные в значениях</div>
        <div className="kv small" style={{ gridTemplateColumns: '130px 1fr' }}>
          {cfg.variables.map((v) => <Fragment key={v.name}><div className="mono miku-text">{'{' + v.name + '}'}</div><div className="text-2">{v.description} <span className="muted">· {v.example}</span></div></Fragment>)}
        </div>
        <div className="row" style={{ marginTop: 14 }}>
          <button className="btn ghost sm" onClick={onReset}>Вернуть заголовки по умолчанию</button>
        </div>
      </div>

      {edit !== null && hs[edit] && (
        <HeaderForm h={hs[edit]} onClose={() => setEdit(null)} onSave={(h) => { update(edit, h); setEdit(null) }} />
      )}
      {catalogOpen && (
        <Modal title="Каталог заголовков" wide onClose={() => setCatalogOpen(false)}>
          <p className="muted small" style={{ marginTop: 0 }}>Известные заголовки популярных приложений. Часть из них работает только с ProviderID из кабинета разработчика приложения.</p>
          <div className="table-wrap">
            <table className="table">
              <tbody>
                {cfg.headerCatalog.map((p) => {
                  const added = hs.some((h) => h.name.toLowerCase() === p.name.toLowerCase() && h.clients === p.clients)
                  return (
                    <tr key={p.name + p.clients}>
                      <td>
                        <b className="mono">{p.name}</b> <span className="small muted">· {p.apps}</span>
                        <div className="small text-2">{p.description}</div>
                        <div className="small mono muted">{p.value || 'значение задаётся вами'}</div>
                      </td>
                      <td style={{ width: 120 }}>
                        <button className="btn sm" disabled={added} onClick={() => {
                          set([...hs, { name: p.name, value: p.value, base64: p.base64, clients: p.clients, enabled: true }])
                          if (!p.value || p.value.includes('…')) setEdit(hs.length)
                          setCatalogOpen(false)
                        }}>{added ? 'уже есть' : 'Добавить'}</button>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        </Modal>
      )}
    </>
  )
}

function HeaderForm({ h, onClose, onSave }: { h: SubHeader; onClose: () => void; onSave: (h: SubHeader) => void }) {
  const [v, setV] = useState(h)
  const presets: [string, string][] = [['', 'всем приложениям'], ['(?i)\\bhapp\\b', 'Happ'], ['(?i)\\b(happ|incy)\\b', 'Happ и INCY'], ['(?i)v2raytun', 'v2RayTun'],
    ['(?i)keqdroid', 'KeqDroid'], ['(?i)hiddify', 'Hiddify'], ['(?i)clash|mihomo|stash|flclash|koala', 'Clash-клиентам']]
  return (
    <Modal title={h.name} onClose={onClose}
      footer={<><button className="btn ghost" onClick={onClose}>Отмена</button><button className="btn primary" onClick={() => onSave(v)}>Готово</button></>}>
      <Field label="Имя заголовка"><input className="input mono" value={v.name} onChange={(e) => setV({ ...v, name: e.target.value })} /></Field>
      <Field label="Значение" help="Можно с переменными: {title}, {username}, {expire_date}… Пустое — не отправлять">
        <textarea className="input mono" rows={3} value={v.value} onChange={(e) => setV({ ...v, value: e.target.value })} />
      </Field>
      <label className="check" style={{ marginBottom: 14 }}>
        <input type="checkbox" checked={v.base64} onChange={(e) => setV({ ...v, base64: e.target.checked })} />
        Отправлять как base64:… (для русского текста и эмодзи; так требует KeqDroid и большинство приложений)
      </label>
      <Field label="Каким приложениям" help="Регулярное выражение по User-Agent. Пусто — всем">
        <select className="input" value={presets.some(([p]) => p === v.clients) ? v.clients : '__custom'} onChange={(e) => e.target.value !== '__custom' && setV({ ...v, clients: e.target.value })}>
          {presets.map(([p, n]) => <option key={p} value={p}>{n}</option>)}
          <option value="__custom">своё выражение…</option>
        </select>
        <input className="input mono" style={{ marginTop: 8 }} value={v.clients} onChange={(e) => setV({ ...v, clients: e.target.value })} placeholder="(?i)happ" />
      </Field>
    </Modal>
  )
}

function TestTab({ headers, basics, users, rules, dirty }: { headers: SubHeader[]; basics: SubBasics; users: User[]; rules: SubRule[]; dirty: boolean }) {
  const uas = ['keqdroid/0.25.1', 'Happ/4.1.0', 'v2RayTun/5.25.82', 'Hiddify/4.1.1 (android)', 'INCY/3.6.5', 'clash-verge/v2.2.3', 'FlClashX/0.4.2', 'SFA/1.14.0', 'Streisand/1.6.76', 'v2rayNG/2.2.6']
  const [ua, setUa] = useState(uas[0])
  const [userId, setUserId] = useState(0)
  const { data, error } = useLoad(() => post<{ user: string; format: string; headers: { name: string; value: string }[] }>('subscription/test', { userId, userAgent: ua, headers, basics, rules }),
    [ua, userId, JSON.stringify(headers), JSON.stringify(basics), JSON.stringify(rules)])
  return (
    <>
      <div className="card card-pad">
        <div className="muted small" style={{ marginBottom: 12 }}>
          Что получит приложение: формат и заголовки. {dirty && <span className="pink-text">Показано с несохранёнными изменениями.</span>} Устройство при проверке не регистрируется.
        </div>
        <div className="grid2">
          <Field label="Приложение (User-Agent)">
            <input className="input mono" list="uas" value={ua} onChange={(e) => setUa(e.target.value)} />
            <datalist id="uas">{uas.map((u) => <option key={u} value={u} />)}</datalist>
            <div className="row" style={{ gap: 6, marginTop: 8 }}>
              {uas.slice(0, 6).map((u) => <button key={u} className={'btn sm' + (u === ua ? ' primary' : '')} onClick={() => setUa(u)}>{u.split('/')[0]}</button>)}
            </div>
          </Field>
          <Field label="Пользователь">
            <select className="input" value={userId} onChange={(e) => setUserId(Number(e.target.value))}>
              <option value={0}>первый пользователь</option>
              {users.map((u) => <option key={u.id} value={u.id}>{u.username}</option>)}
            </select>
          </Field>
        </div>
      </div>
      <div className="card">
        <div className="card-head"><h3>Ответ</h3>{data && <Badge color="green">{formatNames[data.format] || data.format}</Badge>}</div>
        {!data ? <div className="card-pad"><Loading error={error} /></div> : (
          <pre className="code" style={{ border: 0, borderRadius: 0, maxHeight: 'none' }}>
            {data.headers.length === 0 ? '(заголовков нет)' : data.headers.map((h) => `${h.name}: ${h.value}`).join('\n')}
            {'\nCache-Control: no-store'}
          </pre>
        )}
      </div>
      <div className="card">
        <div className="card-head"><h3>Как выбирается формат</h3></div>
        <div className="card-pad small text-2">
          <ol className="list-plain">
            <li>Формат в конце ссылки (<span className="mono">/s/токен/mihomo</span>) или выбранный у пользователя.</li>
            <li>Иначе по User-Agent, первое совпадение (меняется на вкладке «Форматы»):</li>
          </ol>
          <div className="kv mono" style={{ gridTemplateColumns: '1fr 180px', marginTop: 8 }}>
            {rules.filter((r) => r.enabled).map((r, i) => <Fragment key={i}><div>{r.pattern}</div><div className="miku-text">{formatNames[r.format] || r.format}</div></Fragment>)}
          </div>
          <div style={{ marginTop: 8 }}>Браузер получает страницу, всё остальное — ссылки vless:// в base64.</div>
        </div>
      </div>
    </>
  )
}

function RulesTab({ rules, set, formats, onReset }: { rules: SubRule[]; set: (r: SubRule[]) => void; formats: string[]; onReset: () => void }) {
  const update = (i: number, r: SubRule) => set(rules.map((x, j) => (j === i ? r : x)))
  const move = (i: number, d: number) => {
    const j = i + d
    if (j < 0 || j >= rules.length) return
    const next = [...rules]
    ;[next[i], next[j]] = [next[j], next[i]]
    set(next)
  }
  const bad = (p: string) => {
    try {
      new RegExp(p.replace(/^\(\?i\)/, ''))
      return false
    } catch {
      return true
    }
  }
  return (
    <>
      <div className="card">
        <div className="card-head">
          <div><h3>Формат по приложению</h3><div className="card-sub">правила проверяются сверху вниз, срабатывает первое совпадение User-Agent</div></div>
          <button className="btn sm" onClick={() => set([...rules, { pattern: '(?i)', format: 'base64', enabled: true }])}><Icon name="plus" /> Правило</button>
        </div>
        <div className="table-wrap">
          <table className="table">
            <thead><tr><th /><th>User-Agent (регулярное выражение)</th><th>Формат</th><th>Заметка</th><th /></tr></thead>
            <tbody>
              {rules.length === 0 && <tr><td colSpan={5} className="muted">Правил нет — приложения получат ссылки, браузер — страницу</td></tr>}
              {rules.map((r, i) => (
                <tr key={i}>
                  <td style={{ width: 50 }}><Switch checked={r.enabled} onChange={(v) => update(i, { ...r, enabled: v })} /></td>
                  <td>
                    <input className="input mono" value={r.pattern} onChange={(e) => update(i, { ...r, pattern: e.target.value })} />
                    {bad(r.pattern) && <div className="small pink-text">выражение с ошибкой</div>}
                  </td>
                  <td style={{ width: 260 }}>
                    <select className="input" value={r.format} onChange={(e) => update(i, { ...r, format: e.target.value })}>
                      {formats.map((f) => <option key={f} value={f}>{formatNames[f] || f}</option>)}
                    </select>
                  </td>
                  <td><input className="input" value={r.note || ''} onChange={(e) => update(i, { ...r, note: e.target.value })} placeholder="для кого" /></td>
                  <td className="nowrap" style={{ width: 110 }}>
                    <button className="btn ghost icon sm" title="Выше" disabled={i === 0} onClick={() => move(i, -1)}>↑</button>
                    <button className="btn ghost icon sm" title="Ниже" disabled={i === rules.length - 1} onClick={() => move(i, 1)}>↓</button>
                    <button className="btn ghost icon sm" title="Убрать" onClick={() => set(rules.filter((_, j) => j !== i))}><Icon name="trash" /></button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
      <div className="card card-pad small text-2">
        <p style={{ marginTop: 0 }}>
          Правила нужны, только если приложение не указывает формат само. Формат в конце ссылки (<span className="mono">/s/токен/mihomo</span>) и формат, выбранный у пользователя, важнее правил.
          Если ни одно правило не подошло, браузер получает страницу, остальные — ссылки vless:// в base64.
        </p>
        <p>Проверить, что получит конкретное приложение, можно на вкладке «Проверка» — она учитывает несохранённые правила.</p>
        <button className="btn ghost sm" onClick={onReset}>Вернуть правила по умолчанию</button>
      </div>
    </>
  )
}
