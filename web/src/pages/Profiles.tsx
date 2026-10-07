import { FormEvent, useEffect, useMemo, useState } from 'react'
import { del, get, Group, patch, post, Profile, ProfileDetails, ProfileTemplate, put, Variable } from '../api'
import { CodeEditor, parseObject, pretty } from '../code'
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
            : 'Шаблон — заготовка профиля: JSON инбаунда Xray с переменными, точка подключения для подписки и роль Caddy. Встроенные можно скопировать и править копию.'}
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
                    {(t?.variables || []).filter((v) => v.scope === 'profile').map((v) => (
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

type PreviewItem = { id: number; tag: string; node?: string; inbound?: Record<string, unknown>; error?: string }

function ProfileEditor({ id, onClose, onSaved }: { id: number; onClose: () => void; onSaved: () => void }) {
  const loaded = useLoad(() => get<ProfileDetails>(`profiles/${id}`), [id])
  const [name, setName] = useState('')
  const [tagPattern, setTagPattern] = useState('')
  const [remarkPattern, setRemarkPattern] = useState('')
  const [values, setValues] = useState<Record<string, string>>({})
  const [overrideText, setOverrideText] = useState('')
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
    setOverrideText(Object.keys(p.override).length ? pretty(p.override) : '')
  }, [loaded.data])

  const override = parseObject(overrideText)
  const vars = loaded.data?.template?.variables || []
  const body = useMemo(() => {
    const vals: Record<string, string> = {}
    for (const v of vars) if (v.scope === 'profile' && v.source !== 'derived' && values[v.name] !== undefined) vals[v.name] = values[v.name]
    return { name, tagPattern, remarkPattern, values: vals, override: override.value ?? {}, regenerateKeys: regenerate }
  }, [name, tagPattern, remarkPattern, values, overrideText, regenerate, vars])

  // Предпросмотр: что получит каждая нода, пересчитывается после паузы в правках.
  useEffect(() => {
    if (!loaded.data || override.error) return
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
        <button className="btn primary" disabled={!!override.error || !!failed} onClick={save}>Сохранить</button>
      </>}>
      <div className="editor-split">
        <div>
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

          <Field label="Ручная правка JSON поверх шаблона" help='JSON merge patch: поля заменяют поля шаблона, null удаляет поле. Например {"sniffing":{"enabled":false}}. Пусто — без правок.'>
            <CodeEditor lang="json" value={overrideText} onChange={setOverrideText} height={170} />
            {override.error && <div className="small pink-text" style={{ marginTop: 6 }}>{override.error}</div>}
          </Field>
        </div>

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
              <div className="small text-2" style={{ marginBottom: 6 }}>{item.node} · <span className="mono">{item.tag}</span></div>
              {item.error ? <div className="alert pink">{item.error}</div> : <CodeEditor lang="json" value={pretty(item.inbound)} readOnly height="calc(100vh - 330px)" />}
            </>
          )}
        </div>
      </div>
    </Modal>
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
  const [edit, setEdit] = useState<{ id: string; source: string; isNew: boolean; readOnly: boolean } | null>(null)
  const act = useAction()
  const confirm = useConfirm()
  if (!tpls.data) return <Loading error={tpls.error} />

  const copy = (t: ProfileTemplate) => {
    let id = t.id.replace(/-copy(-\d+)?$/, '') + '-copy'
    for (let i = 2; tpls.data!.some((x) => x.id === id); i++) id = t.id + '-copy-' + i
    const src = t.source
      .replace(/^id:.*$/m, `id: ${id}`)
      .replace(/^title:(.*)$/m, (_m, title) => `title:${title} (копия)`)
      .replace(/^(# .*\n)+/, '')
    setEdit({ id: '', source: src, isNew: true, readOnly: false })
  }

  return (
    <>
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
type Reference = { reference: { builtins: string[]; generators: string[]; derivations: string[]; lint: string[]; validations: string[]; caddyRoles: string[] } }

function TemplateEditor({ id, source, isNew, readOnly, onClose, onSaved }: { id: string; source: string; isNew: boolean; readOnly: boolean; onClose: () => void; onSaved: () => void }) {
  const [text, setText] = useState(source)
  const [check, setCheck] = useState<CheckResult | null>(null)
  const [side, setSide] = useState<'check' | 'help'>('check')
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
            <button className={side === 'check' ? 'on' : ''} onClick={() => setSide('check')}>Проверка</button>
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

function TemplateHelp({ r }: { r?: Reference['reference'] }) {
  const list = (xs?: string[]) => (xs || []).map((x) => <code key={x} className="chip static" style={{ marginRight: 4 }}>{x}</code>)
  return (
    <div className="small text-2 template-help">
      <p><b>Поля шаблона</b></p>
      <ul className="list-plain">
        <li><code>id</code> — латиница, цифры, дефис; <code>title</code>, <code>summary</code> — для людей; <code>version</code> — число, увеличивайте при правках.</li>
        <li><code>tag_pattern</code> — тег инбаунда, обязательно с <code>{'${NODE_CODE}'}</code>; <code>remark_pattern</code> — название в приложениях.</li>
        <li><code>client.flow</code> — flow клиентов (для Reality: xtls-rprx-vision).</li>
        <li><code>variables</code> — переменные: <code>name</code>, <code>scope</code> (profile — одна на профиль, node — своя на каждой ноде), <code>source</code> (input, const, generate, derived), <code>default</code>, <code>default_from: node.domain</code>, <code>generator</code>, <code>from</code>, <code>validate</code>, <code>options</code>, <code>secret</code>, <code>optional</code>, <code>description</code>.</li>
        <li><code>xray_inbound</code> — JSON инбаунда Xray с <code>{'${ПЕРЕМЕННЫМИ}'}</code>. Клиенты подставляются сами.</li>
        <li><code>xhttp_extra</code> — JSON extra для XHTTP (доступен как <code>{'${XHTTP_EXTRA}'}</code>).</li>
        <li><code>host</code> — точка подключения в подписке: address, port, security, sni, fingerprint, public_key, short_id, flow, network, path, host, mode, alpn.</li>
        <li><code>caddy</code> — что поднять в Caddy: <code>role</code> ({list(r?.caddyRoles)}), domain, local_port/upstream, decoy, path.</li>
        <li><code>lint</code> — дополнительные проверки.</li>
      </ul>
      <p><b>Встроенные переменные</b></p><div>{list(r?.builtins)}</div>
      <p><b>Генераторы</b> (source: generate)</p><div>{list(r?.generators)}</div>
      <p><b>Вычисления</b> (source: derived, from: имя(ПЕРЕМЕННАЯ))</p><div>{list(r?.derivations)}</div>
      <p><b>Проверки значений</b> (validate)</p><div>{list(r?.validations)}</div>
      <p><b>Lint</b></p><div>{list(r?.lint)}</div>
      <p className="muted">Подробно — docs/PROFILES.md. Шаблон проверяется пробной сборкой: ключи генерируются, вводимые переменные получают примерные значения.</p>
    </div>
  )
}
