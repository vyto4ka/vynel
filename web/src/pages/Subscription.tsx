import { Fragment, useEffect, useMemo, useState } from 'react'
import { ClientFragment, ClientRouting, get, post, put, SubApp, SubBasics, SubConfig, SubHeader, SubPage, SubRule, TemplateInfo, User } from '../api'
import { ExternalList } from '../extras'
import { RoutingForm } from '../routing'
import { Badge, Field, Icon, Info, Loading, Modal, Switch, useAction, useConfirm, useLoad } from '../ui'
import { t, Trans } from '../i18n'

const tabs: [string, string][] = [
  ['basics', t('subscription.general')],
  ['page', t('subscription.page')],
  ['apps', t('common.apps')],
  ['headers', t('subscription.headers')],
  ['rules', t('subscription.clients')],
  ['routing', t('subscription.routes')],
  ['externals', t('subscription.external')],
  ['test', t('subscription.check')],
]

const platformNames: Record<string, string> = { android: 'Android', ios: 'iOS', windows: 'Windows', macos: 'macOS', linux: 'Linux' }

const formatNames: Record<string, string> = {
  base64: t('subscription.vless_links_base64'), mihomo: 'Clash / Mihomo YAML', singbox: 'sing-box JSON', xray: 'Xray JSON', html: t('subscription.browser_page'),
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
  const userPage = useLoad(() => get<{ users: User[] }>('users?limit=200'))
  const users = { ...userPage, data: userPage.data?.users }
  const [tab, setTab] = useState('basics')
  const [basics, setBasics] = useState<SubBasics | null>(null)
  const [page, setPage] = useState<SubPage | null>(null)
  const [headers, setHeaders] = useState<SubHeader[] | null>(null)
  const [rules, setRules] = useState<SubRule[] | null>(null)
  const [routing, setRouting] = useState<ClientRouting | null>(null)
  const [userId, setUserId] = useState(0)
  const act = useAction()
  const confirm = useConfirm()

  useEffect(() => {
    if (cfg.data) {
      setBasics(cfg.data.basics)
      setPage(cfg.data.page)
      setHeaders(cfg.data.headers)
      setRules(cfg.data.rules)
      setRouting(cfg.data.routing)
    }
  }, [cfg.data])

  const dirty = useMemo(() => {
    if (!cfg.data || !basics || !page || !headers || !rules || !routing) return false
    return JSON.stringify(basics) !== JSON.stringify(cfg.data.basics) || JSON.stringify(page) !== JSON.stringify(cfg.data.page) ||
      JSON.stringify(headers) !== JSON.stringify(cfg.data.headers) || JSON.stringify(rules) !== JSON.stringify(cfg.data.rules) ||
      JSON.stringify(routing) !== JSON.stringify(cfg.data.routing)
  }, [cfg.data, basics, page, headers, rules, routing])

  // Предпросмотр обновляется с небольшой задержкой после правок.
  const [previewSrc, setPreviewSrc] = useState('')
  useEffect(() => {
    if (!basics || !page) return
    const t = setTimeout(() => setPreviewSrc(`api/subscription/preview?user=${userId}&draft=${encodeDraft({ basics, page })}`), 400)
    return () => clearTimeout(t)
  }, [basics, page, userId])

  if (!cfg.data || !basics || !page || !headers || !rules || !routing) return <Loading error={cfg.error} />
  const c = cfg.data

  const save = async () => {
    const r = await act(() => put<SubConfig>('subscription', { basics, page, headers, rules, routing }), t('subscription.saved_apps_pick_up_the'))
    if (r) cfg.setData(r)
  }
  const reset = async (part: 'headers' | 'page' | 'rules' | 'routing') => {
    const q = { headers: t('subscription.reset_the_headers_to_the'), page: t('subscription.reset_the_page_and_apps'), rules: t('subscription.reset_the_format_rules_to'),
      routing: t('subscription.reset_the_routes_to_the') }
    if (!(await confirm(q[part], t('subscription.reset')))) return
    const r = await act(() => post<SubConfig>('subscription/reset', { part }), t('subscription.reset_to_defaults'))
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
        {dirty && <span className="pink-text small">{t('subscription.there_are_unsaved_changes')}</span>}
        <button className="btn ghost" disabled={!dirty} onClick={() => { setBasics(c.basics); setPage(c.page); setHeaders(c.headers); setRules(c.rules); setRouting(c.routing) }}>{t('common.revert')}</button>
        <button className="btn primary" disabled={!dirty} onClick={save}>{t('common.save')}</button>
      </div>

      <div className={showPreview ? 'sub-split' : ''}>
        <div className="cards">
          {tab === 'basics' && <BasicsTab b={basics} set={setBasics} />}
          {tab === 'page' && <PageTab p={page} set={setPage} onReset={() => reset('page')} />}
          {tab === 'apps' && <AppsTab p={page} set={setPage} catalog={c.appCatalog} />}
          {tab === 'headers' && <HeadersTab hs={headers} set={setHeaders} cfg={c} onReset={() => reset('headers')} />}
          {tab === 'rules' && <ClientsTab rules={rules} set={setRules} routing={routing} setRouting={setRouting} onReset={() => reset('rules')} />}
          {tab === 'externals' && <ExternalList />}
          {tab === 'routing' && <RoutingTab r={routing} set={setRouting} onReset={() => reset('routing')} />}
          {tab === 'test' && <TestTab headers={headers} basics={basics} users={users.data || []} rules={rules} dirty={dirty} />}
        </div>
        {showPreview && (
          <div className="card sub-preview">
            <div className="card-head">
              <h3>{t('subscription.preview')}</h3>
              <select className="input" style={{ width: 180, height: 30 }} value={userId} onChange={(e) => setUserId(Number(e.target.value))}>
                <option value={0}>{t('subscription.first_user')}</option>
                {(users.data || []).map((u) => <option key={u.id} value={u.id}>{u.username}</option>)}
              </select>
            </div>
            {previewSrc && <iframe title={t('subscription.subscription_page')} src={previewSrc} />}
          </div>
        )}
      </div>
    </>
  )
}

function RoutingTab({ r, set, onReset }: { r: ClientRouting; set: (r: ClientRouting) => void; onReset: () => void }) {
  return (
    <>
      <div className="card card-pad">
        <h3 style={{ marginTop: 0 }}>{t('subscription.routes_in_apps')}{' '}<Info>{t('subscription.routes_go_along_with_the')}</Info></h3>
        <div className="card-sub" style={{ marginBottom: 14 }}>{t('subscription.xray_json_mihomo_and_sing')}</div>
        <RoutingForm r={r} set={set} />
      </div>
      <FragmentCard f={r.fragment ?? defaultFragment} set={(fragment) => set({ ...r, fragment })} />
      <div className="card card-pad small text-2 row">
        <button className="btn ghost sm" onClick={onReset}>{t('subscription.reset_the_routes_to_the_2')}</button>
        <span>{t('subscription.a_profile_s_own_routes')}{' '}<Info>{t('subscription.a_profile_can_turn_on')}</Info></span>
      </div>
    </>
  )
}

const defaultFragment: ClientFragment = { enabled: false, packets: 'tlshello', length: '10-20', delay: '10-20' }

function FragmentCard({ f, set }: { f: ClientFragment; set: (f: ClientFragment) => void }) {
  const field = (k: 'packets' | 'length' | 'delay') => (e: React.ChangeEvent<HTMLInputElement>) => set({ ...f, [k]: e.target.value })
  return (
    <div className="card card-pad">
      <div className="row" style={{ justifyContent: 'space-between' }}>
        <h3 style={{ margin: 0 }}>{t('subscription.clienthello_fragmentation')}{' '}<Info>{t('subscription.the_app_sends_the_start')}</Info></h3>
        <Switch checked={f.enabled} onChange={(enabled) => set({ ...f, enabled })} />
      </div>
      <div className="card-sub" style={{ margin: '4px 0 14px' }}>{t('subscription.for_operators_that_cut_tls')}</div>
      {f.enabled && (
        <div className="grid3">
          <Field label={t('subscription.packets')} info={t('subscription.tlshello_the_clienthello_only')}>
            <input className="input mono" value={f.packets} onChange={field('packets')} placeholder="tlshello" />
          </Field>
          <Field label={t('subscription.piece_length_bytes')} info={t('subscription.the_size_of_each_piece')}>
            <input className="input mono" value={f.length} onChange={field('length')} placeholder="10-20" />
          </Field>
          <Field label={t('subscription.pause_ms')} info={t('subscription.the_pause_between_pieces_0')}>
            <input className="input mono" value={f.delay} onChange={field('delay')} placeholder="10-20" />
          </Field>
        </div>
      )}
    </div>
  )
}

function BasicsTab({ b, set }: { b: SubBasics; set: (b: SubBasics) => void }) {
  const f = (k: keyof SubBasics) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) =>
    set({ ...b, [k]: k === 'updateHours' ? Number(e.target.value.replace(/\D/g, '')) || 0 : e.target.value })
  return (
    <div className="card card-pad">
      <Field label={t('subscription.subscription_name')} info={t('subscription.this_is_how_the_subscription')}>
        <input className="input" value={b.title} onChange={f('title')} placeholder="VPN" />
      </Field>
      <div className="grid2">
        <Field label={t('subscription.refresh_hours')} info={t('subscription.the_profile_update_interval')}>
          <input className="input" inputMode="numeric" value={b.updateHours || ''} onChange={f('updateHours')} placeholder="12" />
        </Field>
        <Field label={t('subscription.support')} info={t('subscription.the_support_url_header_the')}>
          <input className="input" value={b.supportUrl} onChange={f('supportUrl')} placeholder="https://t.me/your_support" />
        </Field>
      </div>
      <Field label={t('subscription.announcement')} help={t('subscription.empty_not_shown')} info={t('subscription.the_announce_header_a_line')}>
        <textarea className="input" rows={2} value={b.announce} onChange={f('announce')} placeholder={t('subscription.e_g_maintenance_on_saturday')} />
      </Field>
      <Field label={t('subscription.announcement_link')} info={t('subscription.the_announce_url_header_where')}>
        <input className="input" value={b.announceUrl} onChange={f('announceUrl')} placeholder="https://t.me/your_channel" />
      </Field>
      <Field label={t('subscription.open_in_browser_page')} help={t('subscription.empty_this_subscription_s_page')} info={t('subscription.the_profile_web_page_url')}>
        <input className="input" value={b.pageUrl} onChange={f('pageUrl')} placeholder={t('subscription.subscription_page_2')} />
      </Field>
    </div>
  )
}

function PageTab({ p, set, onReset }: { p: SubPage; set: (p: SubPage) => void; onReset: () => void }) {
  const f = (k: keyof SubPage) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>) => set({ ...p, [k]: e.target.value })
  const accents = ['#39c5bb', '#ff5fa2', '#7c9cff', '#f2b84b', '#4ade80', '#e2e8f0']
  return (
    <div className="card card-pad">
      <div className="muted small" style={{ marginBottom: 14 }}><Trans k="subscription.variables_in_the_text" />{' '}<Info>{t('subscription.this_is_the_page_a')}</Info>
      </div>
      <Field label={t('subscription.title')} help={t('subscription.empty_the_subscription_name')}>
        <input className="input" value={p.heading} onChange={f('heading')} placeholder={t('subscription.as_in_general')} />
      </Field>
      <Field label={t('subscription.description_under_the_title')}>
        <textarea className="input" rows={2} value={p.description} onChange={f('description')} placeholder={t('subscription.e_g_hi_subscription_until')} />
      </Field>
      <div className="grid2">
        <Field label={t('subscription.theme')}>
          <select className="input" value={p.theme} onChange={f('theme')}>
            <option value="dark">{t('subscription.dark')}</option>
            <option value="light">{t('subscription.light')}</option>
            <option value="auto">{t('subscription.as_in_the_system')}</option>
          </select>
        </Field>
        <Field label={t('subscription.accent_colour')}>
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
        <label className="check"><input type="checkbox" checked={p.showQr} onChange={(e) => set({ ...p, showQr: e.target.checked })} />{' '}{t('subscription.qr_code')}</label>
        <label className="check"><input type="checkbox" checked={p.showTraffic} onChange={(e) => set({ ...p, showTraffic: e.target.checked })} />{' '}{t('subscription.status_traffic_and_expiry')}</label>
      </div>
      <Field label={t('subscription.how_to_connect_instructions')} help={t('subscription.empty_the_block_is_not')}>
        <textarea className="input" rows={4} value={p.instructions} onChange={f('instructions')} />
      </Field>
      <Field label={t('subscription.footer')}>
        <input className="input" value={p.footer} onChange={f('footer')} placeholder={t('subscription.e_g_mikuvpn')} />
      </Field>
      <button className="btn ghost sm" onClick={onReset}>{t('subscription.reset_the_page_to_the')}</button>
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
          <h3>{t('subscription.app_buttons')}</h3>
          <div className="card-sub">{t('subscription.the_first_enabled_one_is')}{' '}<Info>{t('subscription.on_a_phone_the_apps')}</Info></div>
        </div>
        <button className="btn sm" onClick={() => { set({ ...p, apps: [...apps, { id: '', name: t('subscription.new_app'), link: 'app://import/{url}', download: '', platforms: ['android'], enabled: true }] }); setEdit(apps.length) }}>
          <Icon name="plus" />{' '}{t('subscription.custom')}</button>
      </div>
      <div className="table-wrap">
        <table className="table">
          <tbody>
            {apps.map((a, i) => (
              <tr key={a.id + i}>
                <td style={{ width: 50 }}><Switch checked={a.enabled} onChange={(v) => update(i, { ...a, enabled: v })} /></td>
                <td>
                  <b>{a.name}</b> {a.id === 'keqdroid' && <Badge color="pink">{t('subscription.recommended')}</Badge>}
                  <div className="small muted mono ellipsis" style={{ maxWidth: 330 }}>{a.link}</div>
                  <div className="small text-2">{a.platforms.map((x) => platformNames[x] || x).join(', ')}{a.note && ` · ${a.note}`}</div>
                </td>
                <td className="nowrap" style={{ width: 110 }}>
                  <button className="btn ghost icon sm" title={t('subscription.up')} onClick={() => move(i, -1)}>↑</button>
                  <button className="btn ghost icon sm" title={t('subscription.down')} onClick={() => move(i, 1)}>↓</button>
                  <button className="btn ghost icon sm" title={t('common.edit')} onClick={() => setEdit(i)}><Icon name="edit" /></button>
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
      footer={<>{!known && <button className="btn danger" onClick={onDelete}>{t('common.delete')}</button>}<div className="grow" />
        <button className="btn ghost" onClick={onClose}>{t('common.cancel')}</button><button className="btn primary" onClick={() => onSave(v)}>{t('common.done')}</button></>}>
      <Field label={t('common.name')}><input className="input" value={v.name} onChange={(e) => setV({ ...v, name: e.target.value })} /></Field>
      <Field label={t('subscription.add_link')} help={t('subscription.variables_the_subscription')}>
        <input className="input mono" value={v.link} onChange={(e) => setV({ ...v, link: e.target.value })} />
      </Field>
      <Field label={t('subscription.where_to_download_optional')}><input className="input" value={v.download} onChange={(e) => setV({ ...v, download: e.target.value })} placeholder="https://…" /></Field>
      <Field label={t('subscription.label')}><input className="input" value={v.note || ''} onChange={(e) => setV({ ...v, note: e.target.value })} /></Field>
      <Field label={t('common.platforms')}>
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
            <h3>{t('subscription.subscription_response_headers')}</h3>
            <div className="card-sub">{t('subscription.an_empty_value_is_not')}{' '}<Info>{t('subscription.apps_read_the_http_response')}</Info></div>
          </div>
          <div className="row" style={{ gap: 6 }}>
            <button className="btn sm" onClick={() => setCatalogOpen(true)}><Icon name="plus" />{' '}{t('subscription.from_the_catalogue')}</button>
            <button className="btn sm" onClick={() => { set([...hs, { name: 'X-My-Header', value: '', base64: false, clients: '', enabled: true }]); setEdit(hs.length) }}>
              <Icon name="plus" />{' '}{t('subscription.custom_2')}</button>
          </div>
        </div>
        <div className="table-wrap">
          <table className="table">
            <thead><tr><th></th><th>{t('subscription.header')}</th><th>{t('subscription.value')}</th><th>{t('subscription.for')}</th><th></th></tr></thead>
            <tbody>
              {hs.length === 0 && <tr><td colSpan={5} className="muted">{t('subscription.no_headers_apps_will_see')}</td></tr>}
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
                      <span className="mono small">{h.value || <span className="muted">{t('subscription.empty')}</span>}</span>
                      {h.base64 && <> <Badge>base64</Badge></>}
                      {!h.base64 && /[^\x20-\x7e]/.test(h.value) && <div className="small pink-text">{t('subscription.not_latin_turn_on_base64')}</div>}
                    </td>
                    <td className="small text-2">{h.clients ? <span className="mono">{h.clients}</span> : t('subscription.everyone')}{p && <div className="muted">{p.apps}</div>}</td>
                    <td className="nowrap" style={{ width: 80 }}>
                      <button className="btn ghost icon sm" title={t('common.edit')} onClick={() => setEdit(i)}><Icon name="edit" /></button>
                      <button className="btn ghost icon sm" title={t('common.remove')} onClick={() => set(hs.filter((_, j) => j !== i))}><Icon name="trash" /></button>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      </div>

      <div className="card card-pad">
        <div className="label" style={{ marginBottom: 8 }}>{t('subscription.variables_in_values')}</div>
        <div className="kv small" style={{ gridTemplateColumns: '130px 1fr' }}>
          {cfg.variables.map((v) => <Fragment key={v.name}><div className="mono miku-text">{'{' + v.name + '}'}</div><div className="text-2">{v.description} <span className="muted">· {v.example}</span></div></Fragment>)}
        </div>
        <div className="row" style={{ marginTop: 14 }}>
          <button className="btn ghost sm" onClick={onReset}>{t('subscription.reset_the_headers_to_the_2')}</button>
        </div>
      </div>

      {edit !== null && hs[edit] && (
        <HeaderForm h={hs[edit]} onClose={() => setEdit(null)} onSave={(h) => { update(edit, h); setEdit(null) }} />
      )}
      {catalogOpen && (
        <Modal title={t('subscription.header_catalogue')} wide onClose={() => setCatalogOpen(false)}>
          <p className="muted small" style={{ marginTop: 0 }}>{t('subscription.some_work_only_with_a')}{' '}<Info>{t('subscription.known_headers_of_popular_apps')}</Info></p>
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
                        <div className="small mono muted">{p.value || t('subscription.the_value_is_yours_to')}</div>
                      </td>
                      <td style={{ width: 120 }}>
                        <button className="btn sm" disabled={added} onClick={() => {
                          set([...hs, { name: p.name, value: p.value, base64: p.base64, clients: p.clients, enabled: true }])
                          if (!p.value || p.value.includes('…')) setEdit(hs.length)
                          setCatalogOpen(false)
                        }}>{added ? t('subscription.already_there') : t('common.add')}</button>
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
  const presets: [string, string][] = [['', t('subscription.all_apps')], ['(?i)\\bhapp\\b', 'Happ'], ['(?i)\\b(happ|incy)\\b', t('subscription.happ_and_incy')], ['(?i)v2raytun', 'v2RayTun'],
    ['(?i)keqdroid', 'KeqDroid'], ['(?i)hiddify', 'Hiddify'], ['(?i)clash|mihomo|stash|flclash|koala', t('subscription.clash_clients')]]
  return (
    <Modal title={h.name} onClose={onClose}
      footer={<><button className="btn ghost" onClick={onClose}>{t('common.cancel')}</button><button className="btn primary" onClick={() => onSave(v)}>{t('common.done')}</button></>}>
      <Field label={t('subscription.header_name')}><input className="input mono" value={v.name} onChange={(e) => setV({ ...v, name: e.target.value })} /></Field>
      <Field label={t('subscription.value')} help={t('subscription.variables_are_allowed_empty')}>
        <textarea className="input mono" rows={3} value={v.value} onChange={(e) => setV({ ...v, value: e.target.value })} />
      </Field>
      <label className="check" style={{ marginBottom: 14 }}>
        <input type="checkbox" checked={v.base64} onChange={(e) => setV({ ...v, base64: e.target.checked })} />{t('subscription.send_as_base64')}{' '}<Info>{t('subscription.needed_for_russian_text_and')}</Info>
      </label>
      <Field label={t('subscription.which_apps')} help={t('subscription.empty_all')} info={t('subscription.a_regular_expression_on_the')}>
        <select className="input" value={presets.some(([p]) => p === v.clients) ? v.clients : '__custom'} onChange={(e) => e.target.value !== '__custom' && setV({ ...v, clients: e.target.value })}>
          {presets.map(([p, n]) => <option key={p} value={p}>{n}</option>)}
          <option value="__custom">{t('subscription.your_own_expression')}</option>
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
  const { data, error } = useLoad(() => post<{ user: string; format: string; client: string; ownTemplate: boolean; headers: { name: string; value: string }[] }>('subscription/test', { userId, userAgent: ua, headers, basics, rules }),
    [ua, userId, JSON.stringify(headers), JSON.stringify(basics), JSON.stringify(rules)])
  return (
    <>
      <div className="card card-pad">
        <div className="muted small" style={{ marginBottom: 12 }}>
          {dirty ? <span className="pink-text">{t('subscription.with_unsaved_changes')}</span> : t('subscription.as_saved')} <Info>{t('subscription.what_the_app_will_get')}</Info>
        </div>
        <div className="grid2">
          <Field label={t('subscription.app_user_agent')}>
            <input className="input mono" list="uas" value={ua} onChange={(e) => setUa(e.target.value)} />
            <datalist id="uas">{uas.map((u) => <option key={u} value={u} />)}</datalist>
            <div className="row" style={{ gap: 6, marginTop: 8 }}>
              {uas.slice(0, 6).map((u) => <button key={u} className={'btn sm' + (u === ua ? ' primary' : '')} onClick={() => setUa(u)}>{u.split('/')[0]}</button>)}
            </div>
          </Field>
          <Field label={t('common.user')}>
            <select className="input" value={userId} onChange={(e) => setUserId(Number(e.target.value))}>
              <option value={0}>{t('subscription.first_user')}</option>
              {users.map((u) => <option key={u.id} value={u.id}>{u.username}</option>)}
            </select>
          </Field>
        </div>
      </div>
      <div className="card">
        <div className="card-head">
          <div><h3>{t('subscription.response')}</h3>{data && <div className="card-sub">{data.client ? t('subscription.client', { client: data.client }) : t('subscription.client_not_recognised')}{data.ownTemplate ? t('subscription.own_template') : ''}</div>}</div>
          {data && <Badge color="green">{formatNames[data.format] || data.format}</Badge>}
        </div>
        {!data ? <div className="card-pad"><Loading error={error} /></div> : (
          <pre className="code" style={{ border: 0, borderRadius: 0, maxHeight: 'none' }}>
            {data.headers.length === 0 ? t('subscription.no_headers') : data.headers.map((h) => `${h.name}: ${h.value}`).join('\n')}
            {'\nCache-Control: no-store'}
          </pre>
        )}
      </div>
      <div className="card">
        <div className="card-head"><h3>{t('subscription.how_the_format_is_chosen')}</h3></div>
        <div className="card-pad small text-2">
          <ol className="list-plain">
            <li><Trans k="subscription.the_format_at_the_end" /></li>
            <li>{t('subscription.otherwise_by_user_agent_first')}</li>
          </ol>
          <div className="kv mono" style={{ gridTemplateColumns: '1fr 180px', marginTop: 8 }}>
            {rules.filter((r) => r.enabled).map((r, i) => <Fragment key={i}><div>{r.note || r.pattern}</div><div className="miku-text">{formatNames[r.format] || r.format}{r.template ? ' ✦' : ''}</div></Fragment>)}
          </div>
          <div style={{ marginTop: 8 }}>{t('subscription.a_browser_gets_the_page')}</div>
        </div>
      </div>
    </>
  )
}

// Что можно отдавать клиенту: ссылки — галочка «просто ссылками» для любого приложения.
const clientFormats: [string, string, string][] = [
  ['base64', t('subscription.links'), t('subscription.vless_hysteria2_understood')],
  ['xray', 'Xray JSON', t('subscription.a_full_config_with_routes')],
  ['mihomo', 'Mihomo', 'Clash Meta YAML (FlClashX, Clash Verge, Koala)'],
  ['singbox', 'sing-box', t('subscription.json_for_sing_box_and')],
  ['html', t('subscription.page'), t('subscription.a_page_with_qr_and')],
]
const templateFormats = ['mihomo', 'singbox', 'xray']

// ClientsTab — приложения по User-Agent: что каждому отдавать и, при желании, свой шаблон конфига.
function ClientsTab({ rules, set, routing, setRouting, onReset }: {
  rules: SubRule[]; set: (r: SubRule[]) => void; routing: ClientRouting; setRouting: (r: ClientRouting) => void; onReset: () => void
}) {
  const [cur, setCur] = useState(0)
  const [info, setInfo] = useState<TemplateInfo | null>(null)
  const act = useAction()
  const i = Math.min(cur, rules.length - 1)
  const r = rules[i]
  const update = (x: SubRule) => set(rules.map((y, j) => (j === i ? x : y)))
  const move = (d: number) => {
    const j = i + d
    if (j < 0 || j >= rules.length) return
    const next = [...rules]
    ;[next[i], next[j]] = [next[j], next[i]]
    set(next)
    setCur(j)
  }
  const bad = (p: string) => {
    try {
      new RegExp(p.replace(/^\(\?i\)/, ''))
      return false
    } catch {
      return true
    }
  }
  const read = async () => {
    if (!r?.template?.trim()) return
    const x = await act(() => post<TemplateInfo>('subscription/template', { format: r.format, text: r.template }))
    if (x) setInfo(x)
  }
  // Перенос маршрутов шаблона в вкладку «Маршруты»: списки дополняются, DNS — если найден.
  const takeRouting = (t: ClientRouting) => {
    const merge = (a: string[], b: string[]) => [...a, ...b.filter((x) => !a.includes(x))]
    setRouting({
      ...routing, ruDirect: routing.ruDirect || t.ruDirect, blockAds: routing.blockAds || t.blockAds,
      dnsDirect: t.dnsDirect.length ? t.dnsDirect : routing.dnsDirect, dnsProxy: t.dnsProxy.length ? t.dnsProxy : routing.dnsProxy,
      direct: merge(routing.direct, t.direct), proxy: merge(routing.proxy, t.proxy), block: merge(routing.block, t.block),
    })
  }
  return (
    <>
      <div className="card card-pad">
        <div className="row between" style={{ marginBottom: 10 }}>
          <div className="small text-2">{t('subscription.an_app_is_recognised_by')}</div>
          <button className="btn sm" onClick={() => { set([...rules, { pattern: '(?i)', format: 'base64', enabled: true, note: t('subscription.new_app') }]); setCur(rules.length); setInfo(null) }}>
            <Icon name="plus" />{' '}{t('common.client')}</button>
        </div>
        <div className="chips-scroll">
          {rules.map((x, j) => (
            <button key={j} className={'chip-btn' + (j === i ? ' on' : '') + (x.enabled ? '' : ' off')} onClick={() => { setCur(j); setInfo(null) }}>
              {x.note || x.pattern}{x.template ? ' ✦' : ''}
            </button>
          ))}
          {rules.length === 0 && <span className="muted small">{t('subscription.no_clients_apps_get_links')}</span>}
        </div>
      </div>

      {r && (
        <div className="card">
          <div className="card-head">
            <div className="row" style={{ gap: 10 }}>
              <Switch checked={r.enabled} onChange={(v) => update({ ...r, enabled: v })} />
              <h3>{r.note || r.pattern}</h3>
            </div>
            <div className="row" style={{ gap: 2 }}>
              <button className="btn ghost icon sm" title={t('subscription.up_checked_earlier')} disabled={i === 0} onClick={() => move(-1)}>↑</button>
              <button className="btn ghost icon sm" title={t('subscription.down')} disabled={i === rules.length - 1} onClick={() => move(1)}>↓</button>
              <button className="btn ghost icon sm" title={t('common.remove')} onClick={() => { set(rules.filter((_, j) => j !== i)); setCur(Math.max(0, i - 1)) }}><Icon name="trash" /></button>
            </div>
          </div>
          <div className="card-pad">
            <div className="grid2">
              <Field label={t('common.name')}><input className="input" value={r.note || ''} onChange={(e) => update({ ...r, note: e.target.value })} placeholder="FlClashX" /></Field>
              <Field label="User-Agent" help={bad(r.pattern) ? <span className="pink-text">{t('subscription.expression_with_an_error')}</span> : t('subscription.a_regular_expression_i_case')}>
                <input className="input mono" value={r.pattern} onChange={(e) => update({ ...r, pattern: e.target.value })} />
              </Field>
            </div>
            <div className="label" style={{ marginBottom: 8 }}>{t('subscription.what_to_serve')}</div>
            <div className="format-pick">
              {clientFormats.map(([f, label, hint]) => (
                <label key={f} className={'format-opt' + (r.format === f ? ' on' : '')}>
                  <input type="radio" name={'fmt' + i} checked={r.format === f} onChange={() => { update({ ...r, format: f }); setInfo(null) }} />
                  <b>{label}</b><span className="small muted">{hint}</span>
                </label>
              ))}
            </div>
            {templateFormats.includes(r.format) && (
              <div style={{ marginTop: 16 }}>
                <Field label={t('subscription.own_template_2')} help={t('subscription.empty_the_panel_builds_the')} info={<><Trans k="subscription.paste_a_ready_config_for" v={{ v: r.format === 'mihomo' ? 'Mihomo YAML' : r.format === 'singbox' ? 'sing-box JSON' : 'Xray JSON' }} />{' '}{r.format === 'mihomo' ? t('subscription.into_proxies_and_into_all') :
                  r.format === 'singbox' ? t('subscription.into_outbounds_where_selector') : t('subscription.in_place_of_the_outbound')}.</>}>
                  <textarea className="input mono" rows={10} spellCheck={false} value={r.template || ''} onChange={(e) => { update({ ...r, template: e.target.value }); setInfo(null) }}
                    onBlur={read} placeholder={r.format === 'mihomo' ? 'proxy-groups:\n  - name: Proxy\n    type: select\nrules:\n  - MATCH,Proxy' : '{ "outbounds": [ … ] }'} />
                </Field>
                {r.template?.trim() && (
                  <div className="row" style={{ gap: 8 }}>
                    <button className="btn sm" onClick={read}>{t('subscription.parse_the_template')}</button>
                    <button className="btn sm ghost" onClick={() => { update({ ...r, template: '' }); setInfo(null) }}>{t('subscription.remove_the_template')}</button>
                  </div>
                )}
                {info && (
                  <div className="template-info">
                    <ul>{info.summary.map((x, k) => <li key={k}>{x}</li>)}</ul>
                    {info.routing && (
                      <>
                        <div className="small"><Trans k="subscription.in_the_routes_fields_that" v={{ length: info.routing.direct.length, length2: info.routing.proxy.length, length3: info.routing.block.length }} />{info.routing.ruDirect ? t('subscription.russia_direct') : ''}{info.routing.blockAds ? t('subscription.ad_blocking') : ''}
                          {info.routing.dnsProxy.length ? `, DNS: ${info.routing.dnsProxy.join(', ')}` : ''}.</div>
                        <div className="row" style={{ marginTop: 8, gap: 0 }}>
                          <button className="btn sm primary" onClick={() => takeRouting(info.routing!)}>{t('subscription.move_to_the_routes_tab')}</button>
                          <Info>{t('subscription.other_apps_get_the_routes')}</Info>
                        </div>
                      </>
                    )}
                    {info.skipped.length > 0 && (
                      <details className="small" style={{ marginTop: 8 }}>
                        <summary><Trans k="subscription.rules_without_a_field_on" v={{ length: info.skipped.length }} /></summary>
                        <div className="mono small">{info.skipped.slice(0, 50).map((x, k) => <div key={k}>{x}</div>)}</div>
                      </details>
                    )}
                  </div>
                )}
              </div>
            )}
          </div>
        </div>
      )}

      <div className="card card-pad small text-2 row">
        <button className="btn ghost sm" onClick={onReset}>{t('subscription.reset_the_clients_to_the')}</button>
        <span>{t('subscription.how_the_format_is_chosen')}{' '}<Info>
          <p><Trans k="subscription.the_format_at_the_end_2" /></p>
          <p>{t('subscription.to_see_what_a_specific')}</p>
        </Info></span>
      </div>
    </>
  )
}
