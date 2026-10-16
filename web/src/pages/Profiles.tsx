import { FormEvent, useEffect, useMemo, useState } from 'react'
import { del, get, Group, patch, post, Profile, ProfileDetails, ProfileTemplate, put, Variable } from '../api'
import { CodeEditor, parseObject, pretty } from '../code'
import { templateStarter } from '../templateStarter'
import { Badge, Empty, Field, Icon, Loading, Modal, useAction, useConfirm, useLoad } from '../ui'

const sourceLabel: Record<string, string> = {
  input: 'вводится', const: 'константа', generate: 'генерируется', derived: 'вычисляется',
}

export function Profiles() {
  const [tab, setTab] = useState<'profiles' | 'templates'>(() => (location.hash.includes('templates') ? 'templates' : 'profiles'))
  return (
    <>
      <div className="toolbar">
        <div className="seg">
          <button className={tab === 'profiles' ? 'on' : ''} onClick={() => setTab('profiles')}>Профили</button>
          <button className={tab === 'templates' ? 'on' : ''} onClick={() => setTab('templates')}>Шаблоны профилей</button>
        </div>
        <div className="text-2 grow small">
          {tab === 'profiles'
            ? 'Профиль — общие настройки VLESS-инбаунда по шаблону. На каждой ноде из профиля получается свой инбаунд со своими ключами.'
            : 'Шаблон — заготовка профиля: JSON инбаунда Xray с переменными, точка подключения для подписки и роль nginx. Свой шаблон — кнопка «Новый шаблон» (заготовка с пояснениями) или копия встроенного.'}
        </div>
      </div>
      {tab === 'profiles' ? <ProfileList /> : <TemplateList />}
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
        <button className="btn primary" onClick={() => setCreating(true)}><Icon name="plus" /> Профиль</button>
      </div>
      {profiles.data.length === 0 ? <div className="card"><Empty>Профилей нет</Empty></div> : (
        <div className="cards cols">
          {profiles.data.map((p) => {
            const t = tpls.data!.find((x) => x.id === p.templateId)
            const overridden = Object.keys(p.override).length > 0
            return (
              <div key={p.id} className="card">
                <div className="card-head">
                  <div>
                    <h3>{p.name}</h3>
                    <div className="card-sub">{p.templateTitle || p.templateId} {t?.custom && <Badge color="pink">свой шаблон</Badge>}</div>
                  </div>
                  <div className="row" style={{ gap: 4 }}>
                    <button className="btn sm" onClick={() => setEdit(p.id)}><Icon name="edit" /> Изменить</button>
                    <button className="btn ghost icon sm" title="Удалить" onClick={async () => {
                      if (await confirm(<>Удалить профиль <b>{p.name}</b>? {p.inbounds.length > 0 && `Он используется на нодах: ${p.inbounds.join(', ')}.`}</>, 'Удалить'))
                        act(() => del(`profiles/${p.id}`), 'Профиль удалён').then((r) => { if (r !== undefined) profiles.reload() })
                    }}><Icon name="trash" /></button>
                  </div>
                </div>
                <div className="card-pad">
                  <div className="kv small" style={{ gridTemplateColumns: '150px 1fr' }}>
                    {(t?.variables || []).filter((v) => v.scope === 'profile' && v.source !== 'derived').map((v) => (
                      <ValueRow key={v.name} k={v.name} v={p.values[v.name]} def={v.default} />
                    ))}
                    <div className="k">Тег</div><div className="mono">{p.tagPattern}</div>
                    <div className="k">В приложениях</div><div className="mono">{p.remarkPattern}</div>
                  </div>
                  <div className="row small" style={{ marginTop: 12, gap: 6 }}>
                    <span className="muted">На нодах:</span>
                    {p.inbounds.length === 0 ? <span className="muted">нигде — добавьте в разделе «Ноды»</span> : p.inbounds.map((tag) => <span key={tag} className="chip static mono">{tag}</span>)}
                  </div>
                  {overridden && <div className="small pink-text" style={{ marginTop: 8 }}>Есть ручная правка JSON поверх шаблона</div>}
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
        {v == null || v === '' ? (def != null ? <span className="muted">{show(def)} (по умолчанию)</span> : '—') : show(v)}
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
    const r = await act(() => post<{ id: number }>('profiles', { name, templateId, values, groupIds: gids }), 'Профиль создан')
    if (r) onCreated(r.id)
  }
  return (
    <Modal title="Новый профиль" onClose={onClose}
      footer={<><button className="btn ghost" onClick={onClose}>Отмена</button><button className="btn primary" form="new-profile" disabled={!name.trim() || !templateId}>Создать</button></>}>
      <form id="new-profile" onSubmit={submit}>
        <Field label="Название"><input className="input" autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="Reality" /></Field>
        <Field label="Шаблон" help={t?.summary}>
          <select className="input" value={templateId} onChange={(e) => setTemplateId(e.target.value)}>
            {usable.map((x) => <option key={x.id} value={x.id}>{x.title}{x.custom ? ' (свой)' : ''}</option>)}
          </select>
        </Field>
        {required.map((v) => (
          <Field key={v.name} label={<span className="mono">{v.name}</span>} help={v.description}>
            <input className="input" value={values[v.name] || ''} onChange={(e) => setValues({ ...values, [v.name]: e.target.value })} />
          </Field>
        ))}
        <Field label="Дать доступ группам" help="Пользователи этих групп получат профиль на всех нодах, где он стоит">
          <div className="row">
            {(groups.data || []).map((g) => (
              <label key={g.id} className="check">
                <input type="checkbox" checked={gids.includes(g.id)} onChange={(e) => setGroupIds(e.target.checked ? [...gids, g.id] : gids.filter((x) => x !== g.id))} />
                {g.name}
              </label>
            ))}
          </div>
        </Field>
        <div className="muted small">Остальное — параметры, ключи, правка JSON — настраивается в редакторе, он откроется сразу после создания.</div>
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
  const [tab, setTab] = useState<'params' | 'inbound' | 'host'>('params')
  const [regenerate, setRegenerate] = useState(false)
  const [preview, setPreview] = useState<{ items: PreviewItem[]; error?: string } | null>(null)
  const [selected, setSelected] = useState(0)
  const act = useAction()
  const confirm = useConfirm()

  useEffect(() => {
    const p = loaded.data
    if (!p) return
    setName(p.name)
    setTagPattern(p.tagPattern)
    setRemarkPattern(p.remarkPattern)
    setValues(Object.fromEntries(Object.entries(p.values).map(([k, v]) => [k, v == null ? '' : typeof v === 'object' ? JSON.stringify(v) : String(v)])))
    setInboundText(pretty(p.inboundSource ?? {}))
    setHostText(pretty(p.hostSource ?? {}))
  }, [loaded.data])

  const inboundSrc = parseObject(inboundText)
  const hostSrc = parseObject(hostText)
  const jsonError = inboundSrc.error || hostSrc.error
  const tplInbound = loaded.data?.template?.inboundSource
  const tplHost = loaded.data?.template?.hostSource
  const ownInbound = !!inboundSrc.value && !!tplInbound && canon(inboundSrc.value) !== canon(tplInbound)
  const ownHost = !!hostSrc.value && !!tplHost && canon(hostSrc.value) !== canon(tplHost)
  const vars = loaded.data?.template?.variables || []
  const body = useMemo(() => {
    const vals: Record<string, string> = {}
    for (const v of vars) if (v.scope === 'profile' && v.source !== 'derived' && values[v.name] !== undefined) vals[v.name] = values[v.name]
    return { name, tagPattern, remarkPattern, values: vals, inboundSource: inboundSrc.value, hostSource: hostSrc.value, regenerateKeys: regenerate }
  }, [name, tagPattern, remarkPattern, values, inboundText, hostText, regenerate, vars])

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

  if (!loaded.data) return <Modal title="Профиль" onClose={onClose}><Loading error={loaded.error} /></Modal>
  const p = loaded.data
  const failed = preview?.error || preview?.items.some((i) => i.error)

  const save = async () => {
    if (regenerate && !(await confirm('Сгенерировать значения профиля заново? Клиенты получат их при следующем обновлении подписки.', 'Да'))) return
    const r = await act(() => patch(`profiles/${id}`, body), 'Профиль сохранён — ноды получат изменения за несколько секунд')
    if (r !== undefined) {
      onSaved()
      onClose()
    }
  }
  const item = preview?.items[Math.min(selected, (preview?.items.length || 1) - 1)]

  return (
    <Modal title={<>Профиль «{p.name}» <span className="muted small" style={{ fontWeight: 400 }}>· {p.template?.title}</span></>} xl onClose={onClose}
      footer={<>
        {failed ? <span className="pink-text small grow">Есть ошибки — сохранить не получится</span> : <span className="muted small grow">Изменения применятся на всех нодах профиля</span>}
        <button className="btn ghost" onClick={onClose}>Отмена</button>
        <button className="btn primary" disabled={!!jsonError || !!failed} onClick={save}>Сохранить</button>
      </>}>
      <div className="seg" style={{ marginBottom: 14 }}>
        <button className={tab === 'params' ? 'on' : ''} onClick={() => setTab('params')}>Параметры</button>
        <button className={tab === 'inbound' ? 'on' : ''} onClick={() => setTab('inbound')}>Сервер · JSON{ownInbound && <span className="dot-own" title="свой, не как в шаблоне" />}</button>
        <button className={tab === 'host' ? 'on' : ''} onClick={() => setTab('host')}>Подписка · JSON{ownHost && <span className="dot-own" title="свой, не как в шаблоне" />}</button>
      </div>
      <div className="editor-split">
        {tab === 'params' ? <div>
          <div className="grid2">
            <Field label="Название"><input className="input" value={name} onChange={(e) => setName(e.target.value)} /></Field>
            <Field label="Тег инбаунда" help="${NODE_CODE} — код ноды: VLESS_NL, VLESS_DE…">
              <input className="input mono" value={tagPattern} onChange={(e) => setTagPattern(e.target.value)} />
            </Field>
          </div>
          <Field label="Название сервера в приложениях" help="${NODE_FLAG} ${NODE_NAME} → «🇳🇱 Нидерланды». Можно добавить своё: ${NODE_FLAG} ${NODE_NAME} · Reality">
            <input className="input mono" value={remarkPattern} onChange={(e) => setRemarkPattern(e.target.value)} />
          </Field>

          <div className="label" style={{ margin: '6px 0 8px' }}>Параметры профиля</div>
          <VariableTable vars={vars.filter((v) => v.scope === 'profile')} values={values} onChange={setValues}
            empty="У этого шаблона нет параметров уровня профиля" />
          {vars.some((v) => v.scope === 'profile' && v.source === 'generate') && (
            <label className="check" style={{ margin: '8px 0 14px' }}>
              <input type="checkbox" checked={regenerate} onChange={(e) => setRegenerate(e.target.checked)} /> Сгенерировать значения профиля заново
            </label>
          )}
          {vars.some((v) => v.scope === 'node') && (
            <div className="muted small" style={{ margin: '6px 0 14px' }}>
              На каждой ноде свои: {vars.filter((v) => v.scope === 'node').map((v) => <span key={v.name} className="mono">{v.name} </span>)} — они в «Ноды» → инбаунд → «Настроить».
            </div>
          )}
        </div> : (
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

        <div className="preview-pane">
          <div className="row between" style={{ marginBottom: 8 }}>
            <span className="label">Что получат ноды</span>
            {preview && preview.items.length > 1 && (
              <select className="input" style={{ width: 'auto', height: 30 }} value={selected} onChange={(e) => setSelected(Number(e.target.value))}>
                {preview.items.map((it, i) => <option key={it.id} value={i}>{it.tag}{it.error ? ' ⚠' : ''}</option>)}
              </select>
            )}
          </div>
          {!preview ? <Loading /> : preview.error ? <div className="alert pink">{preview.error}</div> : preview.items.length === 0 ? (
            <div className="muted small">Профиль ещё не стоит ни на одной ноде — добавьте его в разделе «Ноды». Шаблон при этом проверяется при сохранении.</div>
          ) : item && (
            <>
              <div className="small text-2" style={{ marginBottom: 6 }}>
                {item.node} · <span className="mono">{item.tag}</span> · {tab === 'host' ? 'точка подключения для подписки' : 'inbound на ноде'}
              </div>
              {item.error ? <div className="alert pink">{item.error}</div>
                : tab === 'host'
                  ? (item.hostError ? <div className="alert pink">{item.hostError}</div> : <CodeEditor lang="json" value={pretty(item.host ?? {})} readOnly height="calc(100vh - 360px)" />)
                  : <CodeEditor lang="json" value={pretty(item.inbound)} readOnly height="calc(100vh - 360px)" />}
            </>
          )}
        </div>
      </div>
    </Modal>
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
            ? <>Полный inbound Xray, который получит каждая нода. Клиенты (<span className="mono">settings.clients</span>) добавляются сами.</>
            : <>Как сервер выглядит в подписке: адрес, SNI, отпечаток, путь и клиентский <span className="mono">xhttp_client_extra</span> (там же <span className="mono">xmux</span>).</>}
        </div>
        {own ? <Badge color="pink">свой</Badge> : <Badge>как в шаблоне</Badge>}
      </div>
      <CodeEditor lang="json" value={text} onChange={onChange} height="calc(100vh - 420px)" />
      {error && <div className="small pink-text" style={{ marginTop: 6 }}>{error}</div>}
      <div className="row between" style={{ marginTop: 10, alignItems: 'flex-start', flexWrap: 'nowrap', gap: 14 }}>
        <div className="small muted" style={{ minWidth: 0 }}>
          Переменные: {[...vars.map((v) => v.name), ...builtinVars].map((n) => <code key={n} style={{ marginRight: 6, display: 'inline-block' }}>{'${' + n + '}'}</code>)}
        </div>
        <button className="btn sm" disabled={!own} onClick={onReset} title="Взять JSON из шаблона: профиль снова будет следовать шаблону и его обновлениям">Как в шаблоне</button>
      </div>
    </div>
  )
}

// Таблица переменных: вводимые и константы правятся, генерируемые показываются (можно задать своё), выводимые — только для справки.
export function VariableTable({ vars, values, onChange, empty }: { vars: Variable[]; values: Record<string, string>; onChange: (v: Record<string, string>) => void; empty: string }) {
  if (vars.length === 0) return <div className="muted small" style={{ marginBottom: 14 }}>{empty}</div>
  return (
    <div className="var-table">
      {vars.map((v) => {
        const derived = v.source === 'derived'
        const secret = v.secret && values[v.name] === '•••'
        return (
          <div key={v.name} className="var-row">
            <div>
              <div className="mono small"><b>{v.name}</b></div>
              <div className="small muted">{sourceLabel[v.source] || v.source}{v.validate ? ` · ${v.validate}` : ''}{v.optional ? ' · необязательная' : ''}</div>
            </div>
            <div>
              {derived ? <div className="small muted mono">= {v.from}</div> : v.options && v.options.length > 0 ? (
                <select className="input" value={values[v.name] ?? ''} onChange={(e) => onChange({ ...values, [v.name]: e.target.value })}>
                  <option value="">по умолчанию{v.default != null ? ` (${v.default})` : ''}</option>
                  {v.options.map((o) => <option key={o} value={o}>{o}</option>)}
                </select>
              ) : (
                <input className="input mono" value={values[v.name] ?? ''} disabled={secret}
                  onChange={(e) => onChange({ ...values, [v.name]: e.target.value })}
                  placeholder={v.default != null ? `по умолчанию ${v.default}` : v.default_from ? `из ${v.default_from}` : v.source === 'generate' ? 'сгенерируется' : ''} />
              )}
              {v.description && <div className="small muted" style={{ marginTop: 3 }}>{v.description}{secret && ' Значение скрыто.'}</div>}
            </div>
          </div>
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
      .replace(/^title:(.*)$/m, (_m, title) => `title:${title} (копия)`)
      .replace(/^(# .*\n)+/, '')
    setEdit({ id: '', source: src, isNew: true, readOnly: false })
  }

  return (
    <>
      <div className="row" style={{ marginBottom: 14, justifyContent: 'flex-end' }}>
        <button className="btn primary" onClick={create}><Icon name="plus" /> Новый шаблон</button>
      </div>
      <div className="cards cols">
        {tpls.data.map((t) => (
          <div key={t.id} className="card">
            <div className="card-head">
              <div>
                <h3>{t.title}</h3>
                <div className="card-sub mono">{t.id} · v{t.version || '?'}</div>
              </div>
              {t.custom ? <Badge color="pink">свой</Badge> : <Badge>встроенный</Badge>}
            </div>
            <div className="card-pad">
              {t.broken && <div className="alert pink" style={{ marginBottom: 10 }}>Шаблон не разбирается: {t.broken}</div>}
              <div className="small text-2">{t.summary}</div>
              <div className="small muted" style={{ marginTop: 8 }}>
                Переменные: {(t.variables || []).map((v) => v.name).join(', ') || '—'}
              </div>
              <div className="small" style={{ marginTop: 8 }}>
                <span className="muted">Профили: </span>{t.profiles.length ? t.profiles.join(', ') : <span className="muted">не используется</span>}
              </div>
              <div className="row" style={{ marginTop: 12, gap: 6 }}>
                {t.custom ? (
                  <>
                    <button className="btn sm primary" onClick={() => setEdit({ id: t.id, source: t.source, isNew: false, readOnly: false })}><Icon name="edit" /> Изменить</button>
                    <button className="btn sm" onClick={() => copy(t)}><Icon name="copy" /> Копия</button>
                    <button className="btn sm danger" disabled={t.profiles.length > 0} title={t.profiles.length ? 'Используется профилями' : ''} onClick={async () => {
                      if (await confirm(<>Удалить шаблон <b>{t.title}</b>?</>, 'Удалить'))
                        act(() => del(`profile-templates/${t.id}`), 'Шаблон удалён').then((r) => { if (r !== undefined) tpls.reload() })
                    }}><Icon name="trash" /></button>
                  </>
                ) : (
                  <>
                    <button className="btn sm" onClick={() => setEdit({ id: t.id, source: t.source, isNew: false, readOnly: true })}><Icon name="code" /> Посмотреть</button>
                    <button className="btn sm primary" onClick={() => copy(t)}><Icon name="copy" /> Скопировать и изменить</button>
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
      isNew ? 'Шаблон создан — выберите его при создании профиля' : 'Шаблон сохранён — профили на нём обновятся на нодах')
    if (r !== undefined) onSaved()
  }

  return (
    <Modal title={readOnly ? `Встроенный шаблон ${id}` : isNew ? 'Новый шаблон' : `Шаблон ${id}`} xl onClose={onClose}
      footer={readOnly ? <><span className="muted small grow">Встроенные шаблоны не меняются — скопируйте и правьте копию</span><button className="btn primary" onClick={onClose}>Закрыть</button></> : <>
        {check && !check.ok ? <span className="pink-text small grow">Шаблон с ошибкой не сохранится</span> : <span className="muted small grow">Проверяется пробной сборкой на тестовой ноде NL</span>}
        <button className="btn ghost" onClick={onClose}>Отмена</button>
        <button className="btn primary" disabled={!check?.ok} onClick={save}>Сохранить</button>
      </>}>
      <div className="editor-split">
        <CodeEditor lang="yaml" value={text} onChange={setText} readOnly={readOnly} height="calc(100vh - 230px)" />
        <div className="preview-pane">
          <div className="seg" style={{ marginBottom: 10 }}>
            <button className={side === 'check' ? 'on' : ''} onClick={() => setSide('check')}>Проверка{check && !check.ok && ' ✗'}</button>
            <button className={side === 'help' ? 'on' : ''} onClick={() => setSide('help')}>Справка</button>
          </div>
          {side === 'check' ? (
            !check ? <Loading /> : check.ok ? (
              <>
                <div className="alert green" style={{ marginBottom: 10 }}>✓ Шаблон в порядке: <b>{check.title}</b> <span className="mono">({check.id})</span></div>
                <div className="label" style={{ marginBottom: 6 }}>Пример инбаунда (нода NL, сгенерированные ключи)</div>
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
  TAG: 'тег инбаунда по tag_pattern',
  INBOUND_PORT: 'порт инбаунда на ноде (только в host)',
  LISTEN_IP: 'IP, на котором слушает инбаунд (0.0.0.0, если IP не выбран)',
  NODE_CODE: 'код ноды, например NL',
  NODE_NAME: 'название ноды',
  NODE_FLAG: 'флаг страны ноды',
  NODE_COUNTRY: 'код страны, например NL',
  XHTTP_EXTRA: 'содержимое xhttp_extra, только в xray_inbound и в кавычках: "${XHTTP_EXTRA}"',
  hex16: 'случайная hex-строка (16 байт)',
  path: 'путь: случайный из options или /xxxxxx',
  shortid: 'shortId для Reality (8 байт hex)',
  uuid: 'UUID',
  x25519: 'приватный ключ Reality',
  x25519_public: 'публичный ключ из приватного: x25519_public(REALITY_PRIVATE_KEY)',
  xhttp_session_placement: 'где сессия XHTTP, по режиму маскировки: cookie или path',
  xhttp_seq_placement: 'где номер пакета XHTTP, по режиму маскировки: query или path',
  xhttp_padding_placement: 'где набивка XHTTP, по режиму маскировки: query или queryInHeader',
  xhttp_padding_key: 'имя параметра набивки, по режиму маскировки: cb или x_padding',
  xhttp_padding_obfs: 'включать ли маскировку набивки, по режиму маскировки',
  domain: 'домен',
  port: 'порт 1–65535',
  ip: 'IP-адрес',
  xhttp_no_nested_extra: 'в extra нет вложенного extra (частая ошибка при копировании)',
  xhttp_cdn_get_only: 'uplinkHTTPMethod только GET',
  xhttp_no_header_placement: 'ничего не кладётся в заголовки (CDN режет длинные заголовки)',
  xhttp_server_max_header_bytes: 'serverMaxHeaderBytes не меньше 65536 (данные в куках)',
  selfsteal: 'сайт-заглушка для Reality: domain, local_port, decoy',
  reverse_proxy_xhttp: 'TLS на domain и проксирование path в upstream (Xray на 127.0.0.1)',
  certificate: 'nginx только получает сертификат domain для инбаунда (Hysteria2)',
}

function TemplateHelp({ r }: { r?: Reference['reference'] }) {
  const list = (xs?: string[]) => (
    <ul className="list-plain">
      {(xs || []).map((x) => <li key={x}><code>{x}</code>{refHelp[x] && ` — ${refHelp[x]}`}</li>)}
    </ul>
  )
  return (
    <div className="small text-2 template-help">
      <p><b>Как добавить свой шаблон</b></p>
      <ol className="list-plain">
        <li>«Новый шаблон» открывает рабочую заготовку (VLESS + XHTTP за nginx) с пояснениями прямо в тексте. Если схема похожа на встроенную, удобнее «Скопировать и изменить» её.</li>
        <li>Задайте свой <code>id</code>, <code>title</code> и <code>tag_pattern</code> (префикс тега не должен совпадать с другими шаблонами).</li>
        <li>Вставьте свой инбаунд Xray в <code>xray_inbound</code>. Всё, что отличается между нодами (домен, ключи, shortId), замените на <code>{'${ПЕРЕМЕННЫЕ}'}</code> и опишите их в <code>variables</code>. Список <code>clients</code> оставьте пустым: пользователи добавляются сами.</li>
        <li>В <code>host</code> опишите, что уйдёт клиентам в подписку: адрес, порт, SNI, отпечаток.</li>
        <li>Если инбаунд стоит за nginx (TLS на своём домене или сайт-заглушка), добавьте раздел <code>nginx</code>. Готовый конфиг nginx из инструкции вставляется в него как есть: <code>location: |</code> и <code>server: |</code>.</li>
        <li>Смотрите вкладку «Проверка»: после каждой правки шаблон собирается на пробной ноде NL, ошибка показывается там же. Сохранить можно только рабочий шаблон.</li>
        <li>Дальше: «Профили» → «Профиль» → выберите шаблон; потом в «Нодах» добавьте профиль на ноды. Переменные с <code>scope: node</code> вводятся при добавлении на ноду.</li>
      </ol>
      <p className="muted">Правка своего шаблона сразу пересобирает все инбаунды на нём. Если правка ломает хотя бы одну ноду, она не сохранится.</p>

      <p><b>Поля шаблона</b></p>
      <ul className="list-plain">
        <li><code>id</code> — латиница, цифры, дефис; пока на шаблоне есть профили, не меняется. <code>version</code> — число, увеличивайте при правках. <code>title</code>, <code>summary</code> — для людей.</li>
        <li><code>tag_pattern</code> — тег инбаунда, обязательно с <code>{'${NODE_CODE}'}</code>: <code>MYX_{'${NODE_CODE}'}</code> → MYX_NL. <code>remark_pattern</code> — название подключения в приложениях.</li>
        <li><code>protocol</code> — <code>vless</code> или <code>hysteria</code>. <code>client.flow</code> — flow клиентов (для Reality по TCP: <code>xtls-rprx-vision</code>, иначе пусто).</li>
        <li><code>variables</code> — переменные, ниже подробно.</li>
        <li><code>xray_inbound</code> — JSON инбаунда Xray с <code>{'${ПЕРЕМЕННЫМИ}'}</code>. Переменная, которая стоит в строке целиком, подставляется со своим типом: <code>"port": "{'${XRAY_PORT}'}"</code> станет числом. Пути к сертификатам на ноде — метками: <code>vynel:cert:домен</code>, <code>vynel:key:домен</code> (сертификат, который получает нода), <code>vynel:decoy:сайт</code>.</li>
        <li><code>xhttp_extra</code> — extra для XHTTP, в инбаунде подставляется как <code>"{'${XHTTP_EXTRA}'}"</code>. Клиенты получают тот же extra, если в <code>host</code> не задано иное.</li>
        <li><code>host</code> — точка подключения в подписке: <code>address</code>, <code>port</code> (можно <code>{'${INBOUND_PORT}'}</code>), <code>security</code> (tls, reality), <code>sni</code>, <code>host</code>, <code>path</code>, <code>alpn</code>, <code>fingerprint</code>, <code>public_key</code>, <code>short_id</code>, <code>flow</code>, <code>network</code>, <code>mode</code>.
          Для XHTTP: <code>xhttp_client_extra</code> — полный клиентский extra вместо серверного (когда у сервера и клиента разные ключи), <code>xhttp_client_only</code> — ключи поверх серверного (например xmux). <code>mihomo: true</code> — отдавать XHTTP в Mihomo (1.19+).</li>
        <li><code>nginx</code> — что поднять в nginx на ноде: <code>role</code> (ниже), <code>domain</code> (на него получается сертификат), <code>local_port</code> или <code>upstream</code>, <code>path</code>, <code>decoy</code>.
          <code>server_names</code> — ещё имена этого сайта без своего сертификата (домен CDN, который тот пересылает в Host).
          <code>location: |</code> — свои директивы внутри <code>location</code> пути вместо стандартного прокси (для роли reverse_proxy_xhttp), <code>server: |</code> — директивы уровня <code>server</code>.
          Переменные nginx пишутся как обычно (<code>$host</code>), а <code>{'${ИМЯ}'}</code> — это переменные шаблона. Нельзя то, что трогает файлы ноды или подгружает код: <code>include</code>, <code>load_module</code>, <code>root</code>, <code>alias</code>, логи, сертификаты, директивы на <code>…_file</code>, сокеты <code>unix:</code>. Шаблон проверяется настоящим <code>nginx -t</code>.
          Старое имя раздела <code>caddy</code> тоже работает.</li>
        <li><code>node_requirements</code> — порты, DNS-записи и sysctl; попадают в чек-лист ноды.</li>
        <li><code>lint</code> — дополнительные проверки готового инбаунда (ниже).</li>
      </ul>

      <p><b>Переменные</b></p>
      <ul className="list-plain">
        <li><code>name</code> — имя заглавными: <code>DOMAIN</code> → в тексте <code>{'${DOMAIN}'}</code>.</li>
        <li><code>scope</code>: <code>profile</code> — одно значение на профиль, <code>node</code> — своё на каждой ноде.</li>
        <li><code>source</code>: <code>input</code> — вводит админ; <code>const</code> — константа из <code>default</code> (можно поменять в профиле); <code>generate</code> — генерируется один раз и хранится (<code>generator</code>); <code>derived</code> — вычисляется (<code>from: функция(ПЕРЕМЕННАЯ)</code>), не хранится.</li>
        <li><code>default</code> — значение по умолчанию; <code>default_from: node.domain</code> — подставить домен ноды.</li>
        <li><code>options</code> — список вариантов (выпадающий список; для генератора path — из чего выбирать).</li>
        <li><code>validate</code> — проверка ввода; <code>optional: true</code> — можно не заполнять; <code>secret: true</code> — не показывать значение; <code>description</code> — подсказка под полем.</li>
      </ul>

      <p><b>Встроенные переменные</b> (объявлять не нужно)</p>{list(r?.builtins)}
      <p><b>Генераторы</b> (<code>source: generate</code>, <code>generator: …</code>)</p>{list(r?.generators)}
      <p><b>Вычисления</b> (<code>source: derived</code>, <code>from: …</code>)</p>{list(r?.derivations)}
      <p><b>Проверки значений</b> (<code>validate</code>)</p>{list(r?.validations)}
      <p><b>Проверки инбаунда</b> (<code>lint</code>)</p>{list(r?.lint)}
      <p><b>Роли nginx</b> (<code>nginx.role</code>)</p>{list(r?.webRoles)}
      <p className="muted">Подробно — docs/PROFILES.md. При проверке ключи генерируются, а вводимые переменные получают примерные значения.</p>
    </div>
  )
}
