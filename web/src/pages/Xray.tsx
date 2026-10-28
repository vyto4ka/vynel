import { useEffect, useMemo, useRef, useState } from 'react'
import { get, post, put } from '../api'
import { CodeEditor, pretty } from '../code'
import { Badge, copyText, Icon, Info, Loading, Modal, PageInfo, useAction, useConfirm, useLoad, useToast } from '../ui'
import { splitConfig } from '../xrayImport'
import { Linked, LinkProvider, useActiveLink } from '../jsonlink'
import { Key, t, Trans } from '../i18n'

// «Xray»: конфиг, общий для всех нод, одним JSON. Здесь можно сделать всё, что умеет Xray, без
// формы под каждую настройку. Разделы — это те же данные, разрезанные по ключам верхнего уровня:
// правка в разделе меняет общий конфиг, и наоборот.

interface BaseInfo {
  json: string
  default: string
  customized: boolean
  panelKeys: string[]
  tester: boolean
  nodes: { id: number; code: string; name: string; flag: string }[]
}

interface Check {
  ok: boolean
  error?: string
  problems?: string[]
  tested?: boolean
  nodes?: number
}

type Tab = 'base' | 'outbounds' | 'routing' | 'dns' | 'rest' | 'result'

const tabs: { id: Tab; title: string }[] = [
  { id: 'base', title: t('xray.common_config') },
  { id: 'outbounds', title: 'Outbounds' },
  { id: 'routing', title: t('xray.routing') },
  { id: 'dns', title: 'DNS' },
  { id: 'rest', title: t('xray.other') },
  { id: 'result', title: t('xray.result_per_node') },
]

const known = ['outbounds', 'routing', 'dns']

function parseObj(text: string): Record<string, any> | null {
  try {
    const v = JSON.parse(text)
    return v && typeof v === 'object' && !Array.isArray(v) ? v : null
  } catch {
    return null
  }
}

function sameText(a: string, b: string): boolean {
  try {
    return JSON.stringify(JSON.parse(a)) === JSON.stringify(JSON.parse(b))
  } catch {
    return a.trim() === b.trim()
  }
}

export function Xray() {
  const base = useLoad(() => get<BaseInfo>('xray/base'))
  if (!base.data) return <Loading error={base.error} />
  // key: после сохранения редактор начинается заново с того, что лежит на сервере.
  return <XrayEditor key={base.data.json} info={base.data} reload={base.reload} />
}

function XrayEditor({ info, reload }: { info: BaseInfo; reload: () => Promise<void> }) {
  const [tab, setTab] = useState<Tab>('base')
  const [text, setText] = useState(info.json)
  const [syntax, setSyntax] = useState<string | null>(null)
  const [check, setCheck] = useState<Check | null>(null)
  const [busy, setBusy] = useState(false)
  const [importing, setImporting] = useState(false)
  const act = useAction()
  const confirm = useConfirm()

  const dirty = !sameText(text, info.json)
  const obj = useMemo(() => parseObj(text), [text])

  // Закрытие вкладки с несохранённой правкой.
  useEffect(() => {
    if (!dirty) return
    const h = (e: BeforeUnloadEvent) => { e.preventDefault() }
    window.addEventListener('beforeunload', h)
    return () => window.removeEventListener('beforeunload', h)
  }, [dirty])

  // Результат проверки относится к тексту, который проверяли.
  const edit = (v: string) => { setText(v); setCheck(null) }

  const run = async () => {
    setBusy(true)
    const r = await act(() => post<Check>('xray/base/check', { json: text }))
    setBusy(false)
    if (r) setCheck(r)
  }

  const save = async () => {
    setBusy(true)
    // Saved as typed: the panel does not reformat the config.
    const r = await act(() => put('xray/base', { json: text }), t('xray.saved_nodes_get_the_config'))
    setBusy(false)
    if (r !== undefined) reload()
    else run() // что именно не так, видно в проверке
  }

  const reset = async () => {
    if (!(await confirm(t('xray.restore_the_standard_config'), t('xray.restore')))) return
    edit(info.default)
  }

  const blocked = syntax !== null || obj === null

  return (
    <>
      <div className="toolbar">
        <div className="seg">
          {tabs.map((t) => (
            <button key={t.id} className={tab === t.id ? 'on' : ''} onClick={() => setTab(t.id)}>{t.title}</button>
          ))}
        </div>
        <PageInfo><Trans k="xray.the_common_config_is_the" /></PageInfo>
      </div>

      {tab === 'base' && (
        <Pane
          title={t('xray.xray_common_config')}
          hint={<><Trans k="xray.json_as_in_the_xray" v={{ v: <a href="https://xtls.github.io/en/config/" target="_blank" rel="noreferrer">{t('xray.xray_documentation')}</a> }} /></>}
        >
          <CodeEditor lang="json" value={text} onChange={edit} onValidity={setSyntax} height="max(260px, calc(100vh - 440px))" />
        </Pane>
      )}

      {(tab === 'outbounds' || tab === 'routing' || tab === 'dns') && (obj ? (
        <Section key={tab} obj={obj} id={tab} onCommit={(o) => edit(pretty(o))} />
      ) : <Broken />)}

      {tab === 'rest' && (obj ? <Section key="rest" obj={obj} id="rest" onCommit={(o) => edit(pretty(o))} /> : <Broken />)}

      {tab === 'result' && <Result info={info} text={text} blocked={blocked} />}

      {importing && <ImportModal onClose={() => setImporting(false)} onApply={(base) => { edit(pretty(base)); setImporting(false) }} />}

      {tab !== 'result' && (
        <div className="card" style={{ marginTop: 14 }}>
          <div className="card-pad xray-bar">
            <div className="grow">
              {check ? <CheckView c={check} tester={info.tester} /> : (
                <span className="small muted">
                  {dirty ? t('xray.there_are_unsaved_changes') : info.customized ? t('xray.your_own_config_is_saved') : t('xray.the_standard_config')}
                  <Info>{info.tester ? t('xray.the_check_runs_xray_on') : t('xray.there_is_no_xray_on')}</Info>
                </span>
              )}
              {obj && 'log' in obj && <div className="small amber-text" style={{ marginTop: 4 }}>{t('xray.log_overridden')}</div>}
            </div>
            <div className="row" style={{ gap: 8 }}>
              {dirty && <Badge color="pink">{t('xray.not_saved')}</Badge>}
              <button className="btn" onClick={() => setImporting(true)}>{t('xray.import')}</button>
              <button className="btn" disabled={busy || sameText(text, info.default)} onClick={reset} title={t('xray.put_in_the_panel_s')}>{t('xray.standard')}</button>
              <button className="btn" disabled={!dirty} onClick={() => { edit(info.json); setCheck(null) }}>{t('common.revert')}</button>
              <button className="btn" disabled={busy || blocked} onClick={run}>{t('xray.check')}</button>
              <button className="btn primary" disabled={busy || blocked || !dirty} onClick={save}><Icon name="check" />{' '}{t('common.save')}</button>
            </div>
          </div>
          {blocked && (
            <div className="card-pad small pink-text" style={{ paddingTop: 0 }}>
              {syntax ? t('xray.cannot_save', { syntax }) : t('xray.cannot_save_a_json_object')}
            </div>
          )}
        </div>
      )}
    </>
  )
}

function Pane({ title, hint, children }: { title: string; hint: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="card">
      <div className="card-head"><h3>{title}{' '}<Info>{hint}</Info></h3></div>
      <div className="card-pad">
        {children}
      </div>
    </div>
  )
}

function Broken() {
  return <div className="alert pink">{t('xray.the_common_config_has_a')}</div>
}

function CheckView({ c, tester }: { c: Check; tester: boolean }) {
  if (c.error) return <div className="small pink-text">✗ {c.error}</div>
  if (!c.ok) return <div className="small pink-text">{(c.problems || []).map((p, i) => <div key={i}>✗ {p}</div>)}</div>
  return (
    <div className="small code-ok">{t('xray.fits')}{' '}{c.nodes ? t('xray.all_nodes', { nodes: c.nodes }) : t('xray.building')}{c.tested ? t('xray.xray_read_the_config') : tester ? '' : t('xray.there_is_no_xray_on_2')}
    </div>
  )
}

// ---------------------------------------------------------------- разделы

const sectionInfo: Record<string, { title: string; hint: React.ReactNode; empty: any }> = {
  outbounds: {
    title: 'Outbounds',
    empty: [],
    hint: <><Trans k="xray.where_traffic_goes_freedom" /></>,
  },
  routing: {
    title: t('xray.routing'),
    empty: {},
    hint: <><Trans k="xray.rules_from_top_to_bottom" /></>,
  },
  dns: {
    title: 'DNS',
    empty: {},
    hint: <><Trans k="xray.xray_s_own_name_servers" /></>,
  },
  rest: {
    title: t('xray.other'),
    empty: {},
    hint: <><Trans k="xray.all_other_top_level_keys" /></>,
  },
}

function Section({ obj, id, onCommit }: { obj: Record<string, any>; id: string; onCommit: (o: Record<string, any>) => void }) {
  const meta = sectionInfo[id]
  const rest = id === 'rest'
  const value = rest ? Object.fromEntries(Object.entries(obj).filter(([k]) => !known.includes(k))) : (obj[id] ?? meta.empty)
  const [draft, setDraft] = useState(() => pretty(value))
  const [syntax, setSyntax] = useState<string | null>(null)
  const last = useRef(draft)

  const change = (v: string) => {
    setDraft(v)
    last.current = v
    let parsed: any
    try {
      parsed = JSON.parse(v)
    } catch {
      return // пока не JSON: общий конфиг остаётся прежним, ошибку показывает редактор
    }
    if (rest) {
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return
      const head = Object.fromEntries(Object.entries(obj).filter(([k]) => known.includes(k)))
      onCommit({ ...head, ...parsed })
    } else if (id === 'outbounds' ? Array.isArray(parsed) : parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      onCommit({ ...obj, [id]: parsed })
    }
  }

  const wrongType = !syntax && (() => {
    try {
      const p = JSON.parse(draft)
      if (id === 'outbounds') return !Array.isArray(p) && t('xray.an_array_is_needed')
      return (!p || typeof p !== 'object' || Array.isArray(p)) && t('xray.an_object_is_needed')
    } catch {
      return false
    }
  })()

  return (
    <Pane title={meta.title} hint={meta.hint}>
      <CodeEditor lang="json" value={draft} onChange={change} onValidity={setSyntax} height="max(260px, calc(100vh - 440px))" />
      {wrongType && <div className="small pink-text" style={{ marginTop: 6 }}><Trans k="xray.the_change_has_not_reached" v={{ wrongType: wrongType }} /></div>}
    </Pane>
  )
}

// ---------------------------------------------------------------- итог по ноде

function Result({ info, text, blocked }: { info: BaseInfo; text: string; blocked: boolean }) {
  const [node, setNode] = useState<number | null>(info.nodes[0]?.id ?? null)
  const [out, setOut] = useState<{ config: unknown; problems: string[]; origins: Origin[] } | null>(null)
  const [err, setErr] = useState('')

  useEffect(() => {
    if (node === null || blocked) return
    let live = true
    setErr('')
    post<{ config: unknown; problems: string[]; origins: Origin[] }>(`xray/nodes/${node}/config`, { json: text })
      .then((r) => { if (live) setOut(r) }, (e) => { if (live) { setOut(null); setErr(e.message) } })
    return () => { live = false }
  }, [node, text, blocked])

  if (info.nodes.length === 0) return <div className="alert">{t('xray.no_nodes_yet_the_resulting')}</div>
  return (
    <LinkProvider>
      <Pane title={t('xray.result_per_node')} hint={<Trans k="xray.the_config_a_node_will" />}>
        <div className="row" style={{ marginBottom: 10 }}>
          <select className="input" style={{ maxWidth: 280 }} value={node ?? ''} onChange={(e) => setNode(+e.target.value)}>
            {info.nodes.map((n) => <option key={n.id} value={n.id}>{n.flag} {n.name} ({n.code})</option>)}
          </select>
        </div>
        {blocked && <div className="alert pink">{t('xray.the_common_config_has_an')}</div>}
        {err && <div className="alert pink">{err}</div>}
        {out && (out.problems || []).length > 0 && <div className="alert amber" style={{ marginBottom: 10 }}>{(out.problems || []).map((p, i) => <div key={i}>{p}</div>)}</div>}
        {out && !blocked && <div className="editor-split origins-split">
          <ResultConfig config={out.config} />
          <Origins list={out.origins || []} />
        </div>}
      </Pane>
    </LinkProvider>
  )
}

type Origin = { path: string; kind: string; note?: string }

function ResultConfig({ config }: { config: unknown }) {
  const active = useActiveLink()
  return <CodeEditor lang="json" value={pretty(config)} readOnly highlight={active?.inbound} height="max(260px, calc(100vh - 460px))" />
}

// Everything the panel put around the admin's config, one line each; the lines light up in the config.
function Origins({ list }: { list: Origin[] }) {
  return (
    <div className="origins">
      <div className="label" style={{ marginBottom: 8 }}>{t('xray.origins')}<Info>{t('xray.origins_info')}</Info></div>
      {list.map((o) => (
        <Linked key={o.path} className="origin-row" link={{ inbound: [o.path] }}>
          <span className="mono small muted">{o.path}</span>
          <span className="small">{t(('xray.origin_' + o.kind) as Key, { note: o.note ?? '' })}</span>
        </Linked>
      ))}
    </div>
  )
}

// ---------------------------------------------------------------- import

function ImportModal({ onClose, onApply }: { onClose: () => void; onApply: (base: Record<string, any>) => void }) {
  const [text, setText] = useState('')
  const toast = useToast()
  const r = text.trim() ? splitConfig(text) : null
  const rep = r?.report
  return (
    <Modal title={<>{t('xray.import_title')}<Info>{t('xray.import_info')}</Info></>} wide onClose={onClose}
      footer={<>
        {rep && <span className="muted small grow">{t('xray.import_unsaved')}</span>}
        <button className="btn ghost" onClick={onClose}>{t('common.cancel')}</button>
        <button className="btn primary" disabled={!rep} onClick={() => rep && onApply(rep.base)}>{t('xray.import_apply')}</button>
      </>}>
      <CodeEditor lang="json" value={text} onChange={setText} height={240} />
      {!r && <div className="muted small" style={{ marginTop: 8 }}>{t('xray.import_paste')}</div>}
      {r?.error && <div className="small pink-text" style={{ marginTop: 8 }}>{t('xray.import_bad', { error: r.error })}</div>}
      {rep && (
        <ul className="list-plain small" style={{ marginTop: 10 }}>
          <li>{t('xray.import_kept', { keys: rep.kept.join(', ') || '—' })}</li>
          {rep.inbounds.length > 0 && (
            <li>
              {t('xray.import_inbounds', { n: rep.inbounds.length, list: rep.inbounds.map((i) => [i.tag, i.protocol, i.port].filter(Boolean).join(' ')).join('; ') })}{' '}
              <button type="button" className="btn ghost sm" onClick={() => copyText(JSON.stringify(rep.inboundsRaw, null, 2)).then(() => toast(t('ui.copied')))}>{t('xray.import_copy_inbounds')}</button>
            </li>
          )}
          {rep.panelKeys.length > 0 && <li className="muted">{t('xray.import_panel_keys', { keys: rep.panelKeys.join(', ') })}</li>}
          {rep.apiRules > 0 && <li className="muted">{t('xray.import_api_rules', { n: rep.apiRules })}</li>}
          {rep.noDirect && <li className="amber-text">{t('xray.import_no_direct', { tag: rep.noDirect })}</li>}
        </ul>
      )}
    </Modal>
  )
}
