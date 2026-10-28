import { FormEvent, useEffect, useMemo, useState } from 'react'
import { ClientRouting, del, get, Group, patch, post, Profile, ProfileClient, ProfileDetails, ProfileTemplate, put, Variable } from '../api'
import { CascadeList } from '../extras'
import { RoutingForm } from '../routing'
import { CodeEditor, parseObject, pretty } from '../code'
import { templateStarter } from '../templateStarter'
import { Badge, Empty, Field, Icon, Info, Loading, Modal, PageInfo, Switch, useAction, useConfirm, useLoad } from '../ui'
import { t as tr, Trans } from '../i18n'
import { useAdvanced } from '../mode'
import { Link, LinkProvider, Linked, pathsWith, useActiveLink } from '../jsonlink'

const sourceLabel: Record<string, string> = {
  input: tr('profiles.typed_in'), const: tr('profiles.constant'), generate: tr('profiles.generated'), derived: tr('profiles.computed'),
}

export function Profiles() {
  const [tab, setTab] = useState<'profiles' | 'templates' | 'cascades'>(() =>
    location.hash.includes('templates') ? 'templates' : location.hash.includes('cascades') ? 'cascades' : 'profiles')
  const adv = useAdvanced()
  useEffect(() => { if (!adv && tab === 'templates') setTab('profiles') }, [adv, tab])
  return (
    <>
      <div className="toolbar">
        <div className="seg">
          <button className={tab === 'profiles' ? 'on' : ''} onClick={() => setTab('profiles')}>{tr('common.profiles')}</button>
          {adv && <button className={tab === 'templates' ? 'on' : ''} onClick={() => setTab('templates')}>{tr('profiles.profile_templates')}</button>}
          <button className={tab === 'cascades' ? 'on' : ''} onClick={() => setTab('cascades')}>{tr('profiles.cascades')}</button>
        </div>
        <PageInfo>
          {tab === 'profiles'
            ? tr('profiles.a_profile_is_the_common')
            : tab === 'cascades' ? tr('profiles.a_cascade_is_a_server')
            : tr('profiles.a_template_is_a_profile')}
        </PageInfo>
      </div>
      {tab === 'profiles' ? <ProfileList /> : tab === 'cascades' ? <CascadeList /> : <TemplateList />}
    </>
  )
}

// ================================================================ профили

function ProfileList() {
  const profiles = useLoad(() => get<Profile[]>('profiles'))
  const tpls = useLoad(() => get<ProfileTemplate[]>('profile-templates'))
  const [creating, setCreating] = useState(false)
  const [edit, setEdit] = useState<number | null>(null)
  const act = useAction()
  const confirm = useConfirm()

  if (!profiles.data || !tpls.data) return <Loading error={profiles.error || tpls.error} />
  return (
    <>
      <div className="row" style={{ marginBottom: 14, justifyContent: 'flex-end' }}>
        <button className="btn primary" onClick={() => setCreating(true)}><Icon name="plus" />{' '}{tr('common.profile')}</button>
      </div>
      {profiles.data.length === 0 ? <div className="card"><Empty>{tr('profiles.no_profiles')}</Empty></div> : (
        <div className="cards cols">
          {profiles.data.map((p) => {
            const t = tpls.data!.find((x) => x.id === p.templateId)
            const overridden = Object.keys(p.override).length > 0
            return (
              <div key={p.id} className="card">
                <div className="card-head">
                  <div>
                    <h3>{p.name}</h3>
                    <div className="card-sub">{p.templateTitle || p.templateId} {t?.custom && <Badge color="pink">{tr('profiles.own_template')}</Badge>}</div>
                  </div>
                  <div className="row" style={{ gap: 4 }}>
                    <button className="btn sm" onClick={() => setEdit(p.id)}><Icon name="edit" />{' '}{tr('common.edit')}</button>
                    <button className="btn ghost icon sm" title={tr('common.delete')} onClick={async () => {
                      if (await confirm(<><Trans k="profiles.delete_the_profile" v={{ name: p.name }} />{' '}{p.inbounds.length > 0 && tr('profiles.it_is_used_on_nodes', { join: p.inbounds.join(', ') })}</>, tr('common.delete')))
                        act(() => del(`profiles/${p.id}`), tr('profiles.profile_deleted')).then((r) => { if (r !== undefined) profiles.reload() })
                    }}><Icon name="trash" /></button>
                  </div>
                </div>
                <div className="card-pad">
                  <div className="kv small" style={{ gridTemplateColumns: '150px 1fr' }}>
                    {(t?.variables || []).filter((v) => v.scope === 'profile' && v.source !== 'derived').map((v) => (
                      <ValueRow key={v.name} k={v.name} v={p.values[v.name]} def={v.default} />
                    ))}
                    <div className="k">{tr('common.tag')}</div><div className="mono">{p.tagPattern}</div>
                    <div className="k">{tr('profiles.in_apps')}</div><div className="mono">{p.remarkPattern}</div>
                  </div>
                  <div className="row small" style={{ marginTop: 12, gap: 6 }}>
                    <span className="muted">{tr('profiles.on_nodes')}</span>
                    {p.inbounds.length === 0 ? <span className="muted">{tr('profiles.nowhere_add_it_on_the')}</span> : p.inbounds.map((tag) => <span key={tag} className="chip static mono">{tag}</span>)}
                  </div>
                  {overridden && <div className="small pink-text" style={{ marginTop: 8 }}>{tr('profiles.there_is_a_manual_json')}</div>}
                </div>
              </div>
            )
          })}
        </div>
      )}
      {creating && <CreateProfile tpls={tpls.data} onClose={() => setCreating(false)} onCreated={(id) => { setCreating(false); profiles.reload(); setEdit(id) }} />}
      {edit !== null && <ProfileEditor id={edit} onClose={() => setEdit(null)} onSaved={() => profiles.reload()} />}
    </>
  )
}

function ValueRow({ k, v, def }: { k: string; v: any; def?: any }) {
  const show = (x: any) => (typeof x === 'object' ? JSON.stringify(x) : String(x))
  return (
    <>
      <div className="k">{k}</div>
      <div className="mono ellipsis" title={v == null ? '' : show(v)}>
        {v == null || v === '' ? (def != null ? <span className="muted"><Trans k="profiles.default" v={{ v: show(def) }} /></span> : '—') : show(v)}
      </div>
    </>
  )
}

function CreateProfile({ tpls, onClose, onCreated }: { tpls: ProfileTemplate[]; onClose: () => void; onCreated: (id: number) => void }) {
  const groups = useLoad(() => get<Group[]>('groups'))
  const usable = tpls.filter((t) => !t.broken)
  const [name, setName] = useState('')
  const [templateId, setTemplateId] = useState(usable[0]?.id || '')
  const [groupIds, setGroupIds] = useState<number[] | null>(null)
  const act = useAction()
  const t = usable.find((x) => x.id === templateId)
  const gids = groupIds ?? (groups.data || []).filter((g) => g.name === 'Основная').map((g) => g.id)
  const required = (t?.variables || []).filter((v) => v.scope === 'profile' && v.source === 'input' && v.default == null && !v.optional)
  const [values, setValues] = useState<Record<string, string>>({})

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const r = await act(() => post<{ id: number }>('profiles', { name, templateId, values, groupIds: gids }), tr('profiles.profile_created'))
    if (r) onCreated(r.id)
  }
  return (
    <Modal title={tr('profiles.new_profile')} onClose={onClose}
      footer={<><button className="btn ghost" onClick={onClose}>{tr('common.cancel')}</button><button className="btn primary" form="new-profile" disabled={!name.trim() || !templateId}>{tr('common.create')}</button></>}>
      <form id="new-profile" onSubmit={submit}>
        <Field label={tr('common.name')}><input className="input" autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="Reality" /></Field>
        <Field label={tr('common.template')} info={t?.summary || undefined}>
          <select className="input" value={templateId} onChange={(e) => setTemplateId(e.target.value)}>
            {usable.map((x) => <option key={x.id} value={x.id}>{x.title}{x.custom ? tr('profiles.own') : ''}</option>)}
          </select>
        </Field>
        {required.map((v) => (
          <Field key={v.name} label={<span className="mono">{v.name}</span>} info={v.description || undefined}>
            <input className="input" value={values[v.name] || ''} onChange={(e) => setValues({ ...values, [v.name]: e.target.value })} />
          </Field>
        ))}
        <Field label={tr('profiles.give_access_to_groups')} info={tr('profiles.users_of_these_groups_get')}>
          <div className="row">
            {(groups.data || []).map((g) => (
              <label key={g.id} className="check">
                <input type="checkbox" checked={gids.includes(g.id)} onChange={(e) => setGroupIds(e.target.checked ? [...gids, g.id] : gids.filter((x) => x !== g.id))} />
                {g.name}
              </label>
            ))}
          </div>
        </Field>
      </form>
    </Modal>
  )
}

type PreviewItem = { id: number; tag: string; node?: string; inbound?: Record<string, unknown>; host?: Record<string, unknown>; hostError?: string; error?: string }

// canon renders JSON with sorted keys, so «as in the template» does not depend on key order.
function canon(v: unknown): string {
  return JSON.stringify(v, (_, x) => (x && typeof x === 'object' && !Array.isArray(x) ? Object.fromEntries(Object.entries(x).sort(([a], [b]) => (a < b ? -1 : 1))) : x))
}

function ProfileEditor({ id, onClose, onSaved }: { id: number; onClose: () => void; onSaved: () => void }) {
  const loaded = useLoad(() => get<ProfileDetails>(`profiles/${id}`), [id])
  const [name, setName] = useState('')
  const [tagPattern, setTagPattern] = useState('')
  const [remarkPattern, setRemarkPattern] = useState('')
  const [values, setValues] = useState<Record<string, string>>({})
  const [inboundText, setInboundText] = useState('')
  const [hostText, setHostText] = useState('')
  const [tab, setTab] = useState<'params' | 'inbound' | 'host' | 'client'>('params')
  const [client, setClient] = useState<ProfileClient>({ custom: false, mode: '' })
  const [regenerate, setRegenerate] = useState(false)
  const [preview, setPreview] = useState<{ items: PreviewItem[]; error?: string } | null>(null)
  const [selected, setSelected] = useState(0)
  const act = useAction()
  const confirm = useConfirm()
  const adv = useAdvanced()
  // The raw JSON tabs belong to the advanced mode.
  useEffect(() => { if (!adv && (tab === 'inbound' || tab === 'host')) setTab('params') }, [adv, tab])

  useEffect(() => {
    const p = loaded.data
    if (!p) return
    setName(p.name)
    setTagPattern(p.tagPattern)
    setRemarkPattern(p.remarkPattern)
    setValues(Object.fromEntries(Object.entries(p.values).map(([k, v]) => [k, v == null ? '' : typeof v === 'object' ? JSON.stringify(v) : String(v)])))
    setInboundText(pretty(p.inboundSource ?? {}))
    setHostText(pretty(p.hostSource ?? {}))
    setClient(p.client ?? { custom: false, mode: '' })
  }, [loaded.data])

  const inboundSrc = parseObject(inboundText)
  const hostSrc = parseObject(hostText)
  const clientError = client.custom && client.mode === 'json' ? clientConfigError(client.config || '') : undefined
  const jsonError = inboundSrc.error || hostSrc.error || clientError
  const tplInbound = loaded.data?.template?.inboundSource
  const tplHost = loaded.data?.template?.hostSource
  const ownInbound = !!inboundSrc.value && !!tplInbound && canon(inboundSrc.value) !== canon(tplInbound)
  const ownHost = !!hostSrc.value && !!tplHost && canon(hostSrc.value) !== canon(tplHost)
  const vars = loaded.data?.template?.variables || []
  const body = useMemo(() => {
    const vals: Record<string, string> = {}
    for (const v of vars) if (v.scope === 'profile' && v.source !== 'derived' && values[v.name] !== undefined) vals[v.name] = values[v.name]
    return { name, tagPattern, remarkPattern, values: vals, inboundSource: inboundSrc.value, hostSource: hostSrc.value, regenerateKeys: regenerate, client }
  }, [name, tagPattern, remarkPattern, values, inboundText, hostText, regenerate, vars, client])

  // Предпросмотр: что получит каждая нода, пересчитывается после паузы в правках.
  useEffect(() => {
    if (!loaded.data || jsonError) return
    const t = setTimeout(() => {
      post<{ inbounds: PreviewItem[]; error?: string }>(`profiles/${id}/preview`, body)
        .then((r) => setPreview({ items: r.inbounds, error: r.error }))
        .catch((e) => setPreview({ items: [], error: e.message }))
    }, 500)
    return () => clearTimeout(t)
  }, [JSON.stringify(body), loaded.data])

  if (!loaded.data) return <Modal title={tr('common.profile')} onClose={onClose}><Loading error={loaded.error} /></Modal>
  const p = loaded.data
  const failed = preview?.error || preview?.items.some((i) => i.error)

  const save = async () => {
    if (regenerate && !(await confirm(tr('profiles.generate_the_profile_values'), tr('common.yes')))) return
    const r = await act(() => patch(`profiles/${id}`, body), tr('profiles.profile_saved_nodes_get_the'))
    if (r !== undefined) {
      onSaved()
      onClose()
    }
  }

  return (
    <Modal title={<><Trans k="profiles.profile" v={{ name: p.name, v: <span className="muted small" style={{ fontWeight: 400 }}>· {p.template?.title}</span> }} /></>} xl={adv} onClose={onClose}
      footer={<>
        {failed ? <span className="pink-text small grow">{tr('profiles.there_are_errors_saving_is')}</span> : <span className="muted small grow">{tr('profiles.changes_apply_on_all_nodes')}</span>}
        <button className="btn ghost" onClick={onClose}>{tr('common.cancel')}</button>
        <button className="btn primary" disabled={!!jsonError || !!failed} onClick={save}>{tr('common.save')}</button>
      </>}>
      <LinkProvider>
      <div className="seg" style={{ marginBottom: 14 }}>
        <button className={tab === 'params' ? 'on' : ''} onClick={() => setTab('params')}>{tr('profiles.parameters')}</button>
        {adv && <button className={tab === 'inbound' ? 'on' : ''} onClick={() => setTab('inbound')}>{tr('profiles.server_json')}{ownInbound && <span className="dot-own" title={tr('profiles.own_not_as_in_the')} />}</button>}
        {adv && <button className={tab === 'host' ? 'on' : ''} onClick={() => setTab('host')}>{tr('profiles.subscription_json')}{ownHost && <span className="dot-own" title={tr('profiles.own_not_as_in_the')} />}</button>}
        <button className={tab === 'client' ? 'on' : ''} onClick={() => setTab('client')}>{tr('common.client')}{client.custom && <span className="dot-own" title={tr('profiles.custom_client_profile')} />}</button>
      </div>
      {!adv && failed && <div className="alert pink" style={{ marginBottom: 12 }}>{preview?.error || preview?.items.find((i) => i.error)?.error}</div>}
      <div className={adv ? 'editor-split' : ''}>
        {tab === 'params' ? <div>
          <div className="grid2">
            <Field label={tr('common.name')}><input className="input" value={name} onChange={(e) => setName(e.target.value)} /></Field>
            <Field label={tr('profiles.inbound_tag')} help={tr('profiles.is_the_node_code_vless')} link={{ inbound: ['tag'] }}>
              <input className="input mono" value={tagPattern} onChange={(e) => setTagPattern(e.target.value)} />
            </Field>
          </div>
          <Field label={tr('common.server_name_in_apps')} help={tr('profiles.remark_help')} info={tr('profiles.remark_info')}>
            <input className="input mono" value={remarkPattern} onChange={(e) => setRemarkPattern(e.target.value)} />
          </Field>

          <div className="label" style={{ margin: '6px 0 8px' }}>{tr('profiles.profile_parameters')}</div>
          <VariableTable vars={vars.filter((v) => v.scope === 'profile')} values={values} onChange={setValues}
            empty={tr('profiles.this_template_has_no_profile')}
            link={(name) => ({ inbound: pathsWith(inboundSrc.value, name), host: pathsWith(hostSrc.value, name) })} />
          {vars.some((v) => v.scope === 'profile' && v.source === 'generate') && (
            <label className="check" style={{ margin: '8px 0 14px' }}>
              <input type="checkbox" checked={regenerate} onChange={(e) => setRegenerate(e.target.checked)} />{' '}{tr('profiles.generate_the_profile_values_2')}</label>
          )}
          {vars.some((v) => v.scope === 'node') && (
            <div className="muted small" style={{ margin: '6px 0 14px' }}><Trans k="profiles.each_node_has_its_own" v={{ map: vars.filter((v) => v.scope === 'node').map((v) => <span key={v.name} className="mono">{v.name} </span>) }} /></div>
          )}
        </div> : tab === 'client' ? (
          <ClientEditor c={client} set={setClient} base={p.subRouting} error={clientError} />
        ) : (
          <SourceEditor
            kind={tab}
            text={tab === 'inbound' ? inboundText : hostText}
            onChange={tab === 'inbound' ? setInboundText : setHostText}
            error={tab === 'inbound' ? inboundSrc.error : hostSrc.error}
            own={tab === 'inbound' ? ownInbound : ownHost}
            onReset={() => (tab === 'inbound' ? setInboundText(pretty(tplInbound ?? {})) : setHostText(pretty(tplHost ?? {})))}
            vars={vars}
          />
        )}

        {adv && <PreviewPane preview={preview} selected={selected} onSelect={setSelected} tab={tab} />}
      </div>
      </LinkProvider>
    </Modal>
  )
}

// PreviewPane shows what the nodes get. Next to the parameters it follows the field under the
// pointer: the lines the field fills light up, and the pane turns to the connection point when the
// field belongs there.
function PreviewPane({ preview, selected, onSelect, tab }: {
  preview: { items: PreviewItem[]; error?: string } | null; selected: number; onSelect: (i: number) => void; tab: 'params' | 'inbound' | 'host' | 'client'
}) {
  const active = useActiveLink()
  const [kind, setKind] = useState<'inbound' | 'host'>('inbound')
  const linkedKind = active?.inbound?.length ? 'inbound' : active?.host?.length ? 'host' : null
  const shown: 'inbound' | 'host' = tab === 'host' ? 'host' : tab === 'inbound' ? 'inbound' : linkedKind ?? kind
  const item = preview?.items[Math.min(selected, (preview?.items.length || 1) - 1)]
  return (
    <div className="preview-pane">
      <div className="row between" style={{ marginBottom: 8 }}>
        <span className="label">{tr('profiles.what_the_nodes_get')}</span>
        <div className="row" style={{ gap: 6 }}>
          {tab === 'params' && (
            <div className="seg sm">
              <button className={shown === 'inbound' ? 'on' : ''} onClick={() => setKind('inbound')}>{tr('profiles.preview_server')}</button>
              <button className={shown === 'host' ? 'on' : ''} onClick={() => setKind('host')}>{tr('profiles.preview_subscription')}</button>
            </div>
          )}
          {preview && preview.items.length > 1 && (
            <select className="input" style={{ width: 'auto', height: 30 }} value={selected} onChange={(e) => onSelect(Number(e.target.value))}>
              {preview.items.map((it, i) => <option key={it.id} value={i}>{it.tag}{it.error ? ' ⚠' : ''}</option>)}
            </select>
          )}
        </div>
      </div>
      {!preview ? <Loading /> : preview.error ? <div className="alert pink">{preview.error}</div> : preview.items.length === 0 ? (
        <div className="muted small">{tr('profiles.the_profile_is_not_on')}</div>
      ) : item && (
        <>
          <div className="small text-2" style={{ marginBottom: 6 }}>
            {item.node} · <span className="mono">{item.tag}</span> · {shown === 'host' ? tr('profiles.connection_point_for_the_subscription') : tr('profiles.inbound_on_the_node')}
          </div>
          {item.error ? <div className="alert pink">{item.error}</div>
            : shown === 'host'
              ? (item.hostError ? <div className="alert pink">{item.hostError}</div> : <CodeEditor lang="json" value={pretty(item.host ?? {})} readOnly highlight={active?.host} height="calc(100vh - 360px)" />)
              : <CodeEditor lang="json" value={pretty(item.inbound)} readOnly highlight={active?.inbound} height="calc(100vh - 360px)" />}
        </>
      )}
    </div>
  )
}

// Пример для режима «свой JSON»: сервер подставится в outbound с тегом proxy.
const clientExample = {
  log: { loglevel: 'warning' },
  dns: { servers: [{ address: '77.88.8.8', domains: ['geosite:category-ru'], skipFallback: true }, 'https://1.1.1.1/dns-query'] },
  inbounds: [{ tag: 'socks', listen: '127.0.0.1', port: 10808, protocol: 'socks', settings: { udp: true }, sniffing: { enabled: true, routeOnly: true, destOverride: ['http', 'tls', 'quic'] } }],
  outbounds: [{ tag: 'proxy' }, { tag: 'direct', protocol: 'freedom' }, { tag: 'block', protocol: 'blackhole' }],
  routing: { domainStrategy: 'IPIfNonMatch', rules: [
    { type: 'field', ip: ['geoip:private', 'geoip:ru'], outboundTag: 'direct' },
    { type: 'field', domain: ['geosite:category-ru'], outboundTag: 'direct' },
    { type: 'field', network: 'tcp,udp', outboundTag: 'proxy' },
  ] },
}

// Конфиг клиента — объект или список из одного объекта (так его отдают подписки).
function clientConfigError(text: string): string | undefined {
  try {
    const v = JSON.parse(text)
    const one = Array.isArray(v) ? (v.length === 1 ? v[0] : null) : v
    if (one === null || typeof one !== 'object' || Array.isArray(one)) return tr('profiles.a_json_config_object_is')
  } catch (e: any) {
    return e.message
  }
  return undefined
}

// ClientEditor — «Кастомный профиль клиента»: свои маршруты или целый конфиг Xray для серверов профиля.
function ClientEditor({ c, set, base, error }: { c: ProfileClient; set: (c: ProfileClient) => void; base?: ClientRouting; error?: string }) {
  const adv = useAdvanced()
  const mode = c.mode || 'routing'
  return (
    <div>
      <label className="row" style={{ gap: 10, marginBottom: 12 }}>
        <Switch checked={c.custom} onChange={(v) => set({ ...c, custom: v, mode: c.mode || 'routing', routing: c.routing ?? base })} />
        <span>
          <b>{tr('profiles.custom_client_profile_2')}</b>
          <div className="small text-2">{tr('profiles.xray_json_only_keqdroid_happ')}{' '}<Info>{tr('profiles.its_own_routes_and_dns')}</Info></div>
        </span>
      </label>
      {c.custom && <>
        <div className="seg" style={{ marginBottom: 12 }}>
          <button className={mode === 'routing' ? 'on' : ''} onClick={() => set({ ...c, mode: 'routing', routing: c.routing ?? base })}>{tr('common.settings')}</button>
          {(adv || mode === 'json') && <button className={mode === 'json' ? 'on' : ''} onClick={() => set({ ...c, mode: 'json', config: c.config || pretty(clientExample) })}>{tr('profiles.json_config')}</button>}
        </div>
        {mode === 'routing' && c.routing && <RoutingForm r={c.routing} set={(r) => set({ ...c, routing: r })} />}
        {mode === 'json' && <>
          <div className="small text-2" style={{ marginBottom: 8 }}>{tr('profiles.a_full_xray_client_config')}{' '}<Info><Trans k="profiles.the_outbound_with_the_proxy" /></Info>
          </div>
          <CodeEditor lang="json" value={c.config || ''} onChange={(v) => set({ ...c, config: v })} height="calc(100vh - 380px)" />
          {error && <div className="small pink-text">{error}</div>}
        </>}
      </>}
    </div>
  )
}

const builtinVars = ['TAG', 'LISTEN_IP', 'NODE_CODE', 'NODE_NAME', 'NODE_FLAG', 'NODE_COUNTRY', 'INBOUND_PORT']

// SourceEditor edits the profile's own copy of the template JSON. ${VARIABLES} stay in place and
// are filled per node; a copy equal to the template follows the template again.
function SourceEditor({ kind, text, onChange, error, own, onReset, vars }: {
  kind: 'inbound' | 'host'; text: string; onChange: (s: string) => void; error?: string; own: boolean; onReset: () => void; vars: Variable[]
}) {
  return (
    <div>
      <div className="row between" style={{ marginBottom: 8, flexWrap: 'nowrap' }}>
        <div className="small text-2">
          {kind === 'inbound'
            ? <><Trans k="profiles.the_full_xray_inbound_each" /></>
            : <><Trans k="profiles.how_the_server_looks_in" /></>}
        </div>
        {own ? <Badge color="pink">{tr('profiles.own_2')}</Badge> : <Badge>{tr('profiles.as_in_the_template')}</Badge>}
      </div>
      <CodeEditor lang="json" value={text} onChange={onChange} height="calc(100vh - 420px)" />
      {error && <div className="small pink-text" style={{ marginTop: 6 }}>{error}</div>}
      <div className="row between" style={{ marginTop: 10, alignItems: 'flex-start', flexWrap: 'nowrap', gap: 14 }}>
        <div className="small muted" style={{ minWidth: 0 }}><Trans k="profiles.variables" v={{ map: [...vars.map((v) => v.name), ...builtinVars].map((n) => <code key={n} style={{ marginRight: 6, display: 'inline-block' }}>{'${' + n + '}'}</code>) }} /></div>
        <button className="btn sm" disabled={!own} onClick={onReset} title={tr('profiles.take_the_json_from_the')}>{tr('profiles.as_in_the_template_2')}</button>
      </div>
    </div>
  )
}

// Таблица переменных: вводимые и константы правятся, генерируемые показываются (можно задать своё), выводимые — только для справки.
export function VariableTable({ vars, values, onChange, empty, link }: { vars: Variable[]; values: Record<string, string>; onChange: (v: Record<string, string>) => void; empty: string; link?: (name: string) => Link }) {
  if (vars.length === 0) return <div className="muted small" style={{ marginBottom: 14 }}>{empty}</div>
  return (
    <div className="var-table">
      {vars.map((v) => {
        const derived = v.source === 'derived'
        const secret = v.secret && values[v.name] === '•••'
        return (
          <Linked key={v.name} className="var-row" link={link?.(v.name)}>
            <div>
              <div className="mono small"><b>{v.name}</b>{v.description && <Info>{v.description}</Info>}</div>
              <div className="small muted">{sourceLabel[v.source] || v.source}{v.validate ? ` · ${v.validate}` : ''}{v.optional ? tr('profiles.optional') : ''}</div>
            </div>
            <div>
              {derived ? <div className="small muted mono">= {v.from}</div> : v.options && v.options.length > 0 ? (
                <select className="input" value={values[v.name] ?? ''} onChange={(e) => onChange({ ...values, [v.name]: e.target.value })}>
                  <option value=""><Trans k="common.default_2" v={{ v: v.default != null ? ` (${v.default})` : '' }} /></option>
                  {v.options.map((o) => <option key={o} value={o}>{o}</option>)}
                </select>
              ) : (
                <input className="input mono" value={values[v.name] ?? ''} disabled={secret}
                  onChange={(e) => onChange({ ...values, [v.name]: e.target.value })}
                  placeholder={v.default != null ? tr('common.default_3', { default: v.default }) : v.default_from ? tr('profiles.from', { default_from: v.default_from }) : v.source === 'generate' ? tr('profiles.will_be_generated') : ''} />
              )}
              {secret && <div className="small muted" style={{ marginTop: 3 }}>{tr('profiles.value_hidden')}</div>}
            </div>
          </Linked>
        )
      })}
    </div>
  )
}

// ================================================================ шаблоны

function TemplateList() {
  const tpls = useLoad(() => get<ProfileTemplate[]>('profile-templates'))
  const [edit, setEdit] = useState<{ id: string; source: string; isNew: boolean; readOnly: boolean; help?: boolean } | null>(null)
  const act = useAction()
  const confirm = useConfirm()
  if (!tpls.data) return <Loading error={tpls.error} />

  const freeID = (base: string) => {
    let id = base
    for (let i = 2; tpls.data!.some((x) => x.id === id); i++) id = `${base}-${i}`
    return id
  }
  const create = () => {
    const src = templateStarter.replace(/^id:.*$/m, `id: ${freeID('my-xhttp')}`)
    setEdit({ id: '', source: src, isNew: true, readOnly: false, help: true })
  }

  const copy = (t: ProfileTemplate) => {
    const id = freeID(t.id.replace(/-copy(-\d+)?$/, '') + '-copy')
    const src = t.source
      .replace(/^id:.*$/m, `id: ${id}`)
      .replace(/^title:(.*)$/m, (_m, title) => tr('profiles.title_copy', { title }))
      .replace(/^(# .*\n)+/, '')
    setEdit({ id: '', source: src, isNew: true, readOnly: false })
  }

  return (
    <>
      <div className="row" style={{ marginBottom: 14, justifyContent: 'flex-end' }}>
        <button className="btn primary" onClick={create}><Icon name="plus" />{' '}{tr('common.new_template')}</button>
      </div>
      <div className="cards cols">
        {tpls.data.map((t) => (
          <div key={t.id} className="card">
            <div className="card-head">
              <div>
                <h3>{t.title}</h3>
                <div className="card-sub mono">{t.id} · v{t.version || '?'}</div>
              </div>
              {t.custom ? <Badge color="pink">{tr('profiles.own_2')}</Badge> : <Badge>{tr('profiles.built_in')}</Badge>}
            </div>
            <div className="card-pad">
              {t.broken && <div className="alert pink" style={{ marginBottom: 10 }}><Trans k="profiles.the_template_cannot_be_parsed" v={{ broken: t.broken }} /></div>}
              <div className="small text-2">{t.summary}</div>
              <div className="small muted" style={{ marginTop: 8 }}><Trans k="profiles.variables_2" v={{ v: (t.variables || []).map((v) => v.name).join(', ') || '—' }} /></div>
              <div className="small" style={{ marginTop: 8 }}>
                <span className="muted">{tr('profiles.profiles')}{' '}</span>{t.profiles.length ? t.profiles.join(', ') : <span className="muted">{tr('common.not_used')}</span>}
              </div>
              <div className="row" style={{ marginTop: 12, gap: 6 }}>
                {t.custom ? (
                  <>
                    <button className="btn sm primary" onClick={() => setEdit({ id: t.id, source: t.source, isNew: false, readOnly: false })}><Icon name="edit" />{' '}{tr('common.edit')}</button>
                    <button className="btn sm" onClick={() => copy(t)}><Icon name="copy" />{' '}{tr('profiles.copy')}</button>
                    <button className="btn sm danger" disabled={t.profiles.length > 0} title={t.profiles.length ? tr('profiles.used_by_profiles') : ''} onClick={async () => {
                      if (await confirm(<><Trans k="profiles.delete_the_template" v={{ title: t.title }} /></>, tr('common.delete')))
                        act(() => del(`profile-templates/${t.id}`), tr('common.template_deleted')).then((r) => { if (r !== undefined) tpls.reload() })
                    }}><Icon name="trash" /></button>
                  </>
                ) : (
                  <>
                    <button className="btn sm" onClick={() => setEdit({ id: t.id, source: t.source, isNew: false, readOnly: true })}><Icon name="code" />{' '}{tr('profiles.view')}</button>
                    <button className="btn sm primary" onClick={() => copy(t)}><Icon name="copy" />{' '}{tr('profiles.copy_and_edit')}</button>
                  </>
                )}
              </div>
            </div>
          </div>
        ))}
      </div>
      {edit && <TemplateEditor {...edit} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); tpls.reload() }} />}
    </>
  )
}

type CheckResult = { ok: boolean; error?: string; id?: string; title?: string; variables?: Variable[]; sample?: Record<string, unknown> }
type Reference = { reference: { builtins: string[]; generators: string[]; derivations: string[]; lint: string[]; validations: string[]; webRoles: string[] } }

function TemplateEditor({ id, source, isNew, readOnly, help, onClose, onSaved }: { id: string; source: string; isNew: boolean; readOnly: boolean; help?: boolean; onClose: () => void; onSaved: () => void }) {
  const [text, setText] = useState(source)
  const [check, setCheck] = useState<CheckResult | null>(null)
  const [side, setSide] = useState<'check' | 'help'>(help ? 'help' : 'check')
  const ref = useLoad(() => get<Reference>('profile-templates/reference'))
  const act = useAction()

  useEffect(() => {
    const t = setTimeout(() => {
      post<CheckResult>('profile-templates/check', { source: text }).then(setCheck).catch((e) => setCheck({ ok: false, error: e.message }))
    }, 500)
    return () => clearTimeout(t)
  }, [text])

  const save = async () => {
    const r = await act(() => (isNew ? post('profile-templates', { source: text }) : put(`profile-templates/${id}`, { source: text })),
      isNew ? tr('profiles.template_created_choose_it') : tr('profiles.template_saved_profiles_on'))
    if (r !== undefined) onSaved()
  }

  return (
    <Modal title={readOnly ? tr('profiles.built_in_template', { id }) : isNew ? tr('common.new_template') : tr('profiles.template', { id })} xl onClose={onClose}
      footer={readOnly ? <><span className="muted small grow">{tr('profiles.built_in_templates_are_not')}</span><button className="btn primary" onClick={onClose}>{tr('common.close')}</button></> : <>
        {check && !check.ok ? <span className="pink-text small grow">{tr('profiles.a_template_with_an_error')}</span> : <span className="muted small grow">{tr('profiles.checked_by_a_trial_build')}</span>}
        <button className="btn ghost" onClick={onClose}>{tr('common.cancel')}</button>
        <button className="btn primary" disabled={!check?.ok} onClick={save}>{tr('common.save')}</button>
      </>}>
      <div className="editor-split">
        <CodeEditor lang="yaml" value={text} onChange={setText} readOnly={readOnly} height="calc(100vh - 230px)" />
        <div className="preview-pane">
          <div className="seg" style={{ marginBottom: 10 }}>
            <button className={side === 'check' ? 'on' : ''} onClick={() => setSide('check')}><Trans k="profiles.check" v={{ v: check && !check.ok && ' ✗' }} /></button>
            <button className={side === 'help' ? 'on' : ''} onClick={() => setSide('help')}>{tr('profiles.reference')}</button>
          </div>
          {side === 'check' ? (
            !check ? <Loading /> : check.ok ? (
              <>
                <div className="alert green" style={{ marginBottom: 10 }}><Trans k="profiles.the_template_is_fine" v={{ title: check.title, id: check.id }} /></div>
                <div className="label" style={{ marginBottom: 6 }}>{tr('profiles.an_inbound_example_node_nl')}</div>
                <CodeEditor lang="json" value={pretty(check.sample)} readOnly height="calc(100vh - 380px)" />
              </>
            ) : <div className="alert pink" style={{ whiteSpace: 'pre-wrap' }}>{check.error}</div>
          ) : <TemplateHelp r={ref.data?.reference} />}
        </div>
      </div>
    </Modal>
  )
}

// What the built-in names from /profile-templates/reference mean; unknown names are listed bare.
const refHelp: Record<string, string> = {
  TAG: tr('profiles.the_inbound_tag_by_tag'),
  INBOUND_PORT: tr('profiles.the_inbound_s_port_on'),
  LISTEN_IP: tr('profiles.the_ip_the_inbound_listens'),
  NODE_CODE: tr('profiles.the_node_code_e_g'),
  NODE_NAME: tr('profiles.the_node_name'),
  NODE_FLAG: tr('profiles.the_node_s_country_flag'),
  NODE_COUNTRY: tr('profiles.the_country_code_e_g'),
  XHTTP_EXTRA: tr('profiles.the_content_of_xhttp_extra'),
  hex16: tr('profiles.a_random_hex_string_16'),
  path: tr('profiles.a_path_random_from_options'),
  shortid: tr('profiles.a_shortid_for_reality_8'),
  uuid: 'UUID',
  x25519: tr('profiles.the_reality_private_key'),
  x25519_public: tr('profiles.the_public_key_from_the'),
  xhttp_session_placement: tr('profiles.where_the_xhttp_session_goes'),
  xhttp_seq_placement: tr('profiles.where_the_xhttp_packet_number'),
  xhttp_padding_placement: tr('profiles.where_the_xhttp_padding_goes'),
  xhttp_padding_key: tr('profiles.the_padding_parameter_name'),
  xhttp_padding_obfs: tr('profiles.whether_padding_obfuscation'),
  domain: tr('profiles.a_domain'),
  port: tr('profiles.a_port_1_65535'),
  ip: tr('profiles.an_ip_address'),
  xhttp_no_nested_extra: tr('profiles.no_nested_extra_inside_extra'),
  xhttp_cdn_get_only: tr('profiles.uplinkhttpmethod_is_get_only'),
  xhttp_no_header_placement: tr('profiles.nothing_goes_into_headers_cdns'),
  xhttp_server_max_header_bytes: tr('profiles.servermaxheaderbytes_is_at'),
  selfsteal: tr('profiles.a_decoy_site_for_reality'),
  reverse_proxy_xhttp: tr('profiles.tls_on_domain_and_proxying'),
  certificate: tr('profiles.nginx_only_obtains_the_certificate'),
}

function TemplateHelp({ r }: { r?: Reference['reference'] }) {
  const list = (xs?: string[]) => (
    <ul className="list-plain">
      {(xs || []).map((x) => <li key={x}><code>{x}</code>{refHelp[x] && ` — ${refHelp[x]}`}</li>)}
    </ul>
  )
  return (
    <div className="small text-2 template-help">
      <p><b>{tr('profiles.how_to_add_your_own')}</b></p>
      <ol className="list-plain">
        <li>{tr('profiles.new_template_opens_a_working')}</li>
        <li><Trans k="profiles.set_your_own_id_title" /></li>
        <li><Trans k="profiles.put_your_xray_inbound_into" /></li>
        <li><Trans k="profiles.in_host_describe_what_goes" /></li>
        <li><Trans k="profiles.if_the_inbound_sits_behind" /></li>
        <li>{tr('profiles.watch_the_check_tab_after')}</li>
        <li><Trans k="profiles.next_profiles_profile_choose" /></li>
      </ol>
      <p className="muted">{tr('profiles.editing_your_template_rebuilds')}</p>

      <p><b>{tr('profiles.template_fields')}</b></p>
      <ul className="list-plain">
        <li><Trans k="profiles.id_latin_letters_digits_hyphen" /></li>
        <li><Trans k="profiles.tag_pattern_the_inbound_tag" /></li>
        <li><Trans k="profiles.protocol_vless_or_hysteria" /></li>
        <li><Trans k="profiles.variables_variables_in_detail" /></li>
        <li><Trans k="profiles.xray_inbound_the_xray_inbound" /></li>
        <li><Trans k="profiles.xhttp_extra_the_extra_for" /></li>
        <li><Trans k="profiles.host_the_connection_point_in" /></li>
        <li><Trans k="profiles.nginx_what_to_set_up" /></li>
        <li><Trans k="profiles.node_requirements_ports_dns" /></li>
        <li><Trans k="profiles.lint_extra_checks_of_the" /></li>
      </ul>

      <p><b>{tr('profiles.variables_3')}</b></p>
      <ul className="list-plain">
        <li><Trans k="profiles.name_the_name_in_capitals" /></li>
        <li><Trans k="profiles.scope_profile_one_value_per" /></li>
        <li><Trans k="profiles.source_input_typed_by_the" /></li>
        <li><Trans k="profiles.default_the_default_value_default" /></li>
        <li><Trans k="profiles.options_a_list_of_choices" /></li>
        <li><Trans k="profiles.validate_input_check_optional" /></li>
      </ul>

      <p><Trans k="profiles.built_in_variables_no_need" /></p>{list(r?.builtins)}
      <p><b>{tr('profiles.generators')}</b> (<code>source: generate</code>, <code>generator: …</code>)</p>{list(r?.generators)}
      <p><b>{tr('profiles.computations')}</b> (<code>source: derived</code>, <code>from: …</code>)</p>{list(r?.derivations)}
      <p><b>{tr('profiles.value_checks')}</b> (<code>validate</code>)</p>{list(r?.validations)}
      <p><b>{tr('profiles.inbound_checks')}</b> (<code>lint</code>)</p>{list(r?.lint)}
      <p><b>{tr('profiles.nginx_roles')}</b> (<code>nginx.role</code>)</p>{list(r?.webRoles)}
      <p className="muted">{tr('profiles.in_detail_docs_profiles_md')}</p>
    </div>
  )
}
