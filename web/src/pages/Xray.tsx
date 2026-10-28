import { useEffect, useMemo, useRef, useState } from 'react'
import { get, post, put } from '../api'
import { CodeEditor, pretty } from '../code'
import { Badge, Icon, Info, Loading, PageInfo, useAction, useConfirm, useLoad } from '../ui'

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
  { id: 'base', title: 'Общий конфиг' },
  { id: 'outbounds', title: 'Outbounds' },
  { id: 'routing', title: 'Маршрутизация' },
  { id: 'dns', title: 'DNS' },
  { id: 'rest', title: 'Остальное' },
  { id: 'result', title: 'Итог по ноде' },
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
    // Отступы приводятся к одному виду: в базе лежит аккуратный JSON.
    const out = obj ? pretty(obj) : text
    const r = await act(() => put('xray/base', { json: out }), 'Сохранено, ноды получат конфиг')
    setBusy(false)
    if (r !== undefined) reload()
    else run() // что именно не так, видно в проверке
  }

  const reset = async () => {
    if (!(await confirm('Вернуть стандартный конфиг? Свои outbounds, правила маршрутов и DNS из редактора пропадут (сохранить нужно отдельно).', 'Вернуть'))) return
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
        <PageInfo>
          Общий конфиг — часть Xray на каждой ноде, кроме inbound’ов: исходящие подключения, правила маршрутов, DNS и всё остальное, что умеет Xray.
          Inbound’ы собираются из профилей, а разделы <span className="mono">api</span>, <span className="mono">stats</span>, <span className="mono">policy</span> и уровень логов панель задаёт сама.
          Разделы справа — те же данные, разрезанные по ключам: правка в любом месте меняет один и тот же конфиг.
        </PageInfo>
      </div>

      {tab === 'base' && (
        <Pane
          title="Общий конфиг Xray"
          hint={<>JSON как в документации Xray: <span className="mono">outbounds</span>, <span className="mono">routing</span>, <span className="mono">dns</span>, <span className="mono">observatory</span>, <span className="mono">reverse</span> и любые другие ключи верхнего уровня. Подсказка по формату — <a href="https://xtls.github.io/en/config/" target="_blank" rel="noreferrer">документация Xray</a>.</>}
        >
          <CodeEditor lang="json" value={text} onChange={edit} onValidity={setSyntax} height="max(260px, calc(100vh - 440px))" />
        </Pane>
      )}

      {(tab === 'outbounds' || tab === 'routing' || tab === 'dns') && (obj ? (
        <Section key={tab} obj={obj} id={tab} onCommit={(o) => edit(pretty(o))} />
      ) : <Broken />)}

      {tab === 'rest' && (obj ? <Section key="rest" obj={obj} id="rest" onCommit={(o) => edit(pretty(o))} /> : <Broken />)}

      {tab === 'result' && <Result info={info} text={text} blocked={blocked} />}

      {tab !== 'result' && (
        <div className="card" style={{ marginTop: 14 }}>
          <div className="card-pad xray-bar">
            <div className="grow">
              {check ? <CheckView c={check} tester={info.tester} /> : (
                <span className="small muted">
                  {dirty ? 'Есть несохранённые изменения. ' : info.customized ? 'Свой конфиг сохранён. ' : 'Стандартный конфиг. '}
                  {info.tester ? 'Проверка запускает Xray на этом сервере.' : 'Xray на этом сервере нет: проверяется структура, а Xray нод прочитает конфиг при применении.'}
                </span>
              )}
            </div>
            <div className="row" style={{ gap: 8 }}>
              {dirty && <Badge color="pink">не сохранено</Badge>}
              <button className="btn" disabled={busy || sameText(text, info.default)} onClick={reset} title="Подставить стандартный конфиг панели">Стандартный</button>
              <button className="btn" disabled={!dirty} onClick={() => { edit(info.json); setCheck(null) }}>Отменить</button>
              <button className="btn" disabled={busy || blocked} onClick={run}>Проверить</button>
              <button className="btn primary" disabled={busy || blocked || !dirty} onClick={save}><Icon name="check" /> Сохранить</button>
            </div>
          </div>
          {blocked && (
            <div className="card-pad small pink-text" style={{ paddingTop: 0 }}>
              {syntax ? `Сохранить нельзя: ${syntax}` : 'Сохранить нельзя: нужен JSON-объект { … }'}
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
      <div className="card-head"><h3>{title}</h3></div>
      <div className="card-pad">
        <div className="small text-2" style={{ marginBottom: 10 }}>{hint}</div>
        {children}
      </div>
    </div>
  )
}

function Broken() {
  return <div className="alert pink">В общем конфиге ошибка в JSON — раздел недоступен, пока её не исправить. Откройте вкладку «Общий конфиг»: ошибка подчёркнута, строка и столбец написаны под текстом.</div>
}

function CheckView({ c, tester }: { c: Check; tester: boolean }) {
  if (c.error) return <div className="small pink-text">✗ {c.error}</div>
  if (!c.ok) return <div className="small pink-text">{(c.problems || []).map((p, i) => <div key={i}>✗ {p}</div>)}</div>
  return (
    <div className="small code-ok">
      ✓ Подходит {c.nodes ? `всем нодам (${c.nodes})` : 'для сборки'}{c.tested ? ', Xray прочитал конфиг' : tester ? '' : ', Xray на этом сервере нет — структура проверена, нода проверит ещё раз при применении'}
    </div>
  )
}

// ---------------------------------------------------------------- разделы

const sectionInfo: Record<string, { title: string; hint: React.ReactNode; empty: any }> = {
  outbounds: {
    title: 'Outbounds',
    empty: [],
    hint: <>Куда уходит трафик: <span className="mono">freedom</span> напрямую, <span className="mono">blackhole</span> в никуда, свой прокси, WARP, другой сервер. Тег <span className="mono">DIRECT</span> нужен нодам с отдельным выходным IP, первый в списке — выход по умолчанию. Массив <span className="mono">[ … ]</span>.</>,
  },
  routing: {
    title: 'Маршрутизация',
    empty: {},
    hint: <>Правила в порядке сверху вниз: <span className="mono">rules</span>, <span className="mono">balancers</span>, <span className="mono">domainStrategy</span>. Правила каскадов и отдельных IP панель добавляет сама перед вашими.</>,
  },
  dns: {
    title: 'DNS',
    empty: {},
    hint: <>Серверы имён самого Xray: <span className="mono">servers</span>, <span className="mono">hosts</span>, <span className="mono">queryStrategy</span>. Пусто — DNS системы.</>,
  },
  rest: {
    title: 'Остальное',
    empty: {},
    hint: <>Все остальные ключи верхнего уровня: <span className="mono">observatory</span>, <span className="mono">burstObservatory</span>, <span className="mono">reverse</span>, <span className="mono">fakedns</span>, <span className="mono">metrics</span> и другие. Объект <span className="mono">{'{ … }'}</span>.</>,
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
      if (id === 'outbounds') return !Array.isArray(p) && 'нужен массив [ … ]'
      return (!p || typeof p !== 'object' || Array.isArray(p)) && 'нужен объект { … }'
    } catch {
      return false
    }
  })()

  return (
    <Pane title={meta.title} hint={meta.hint}>
      <CodeEditor lang="json" value={draft} onChange={change} onValidity={setSyntax} height="max(260px, calc(100vh - 440px))" />
      {wrongType && <div className="small pink-text" style={{ marginTop: 6 }}>{wrongType}: изменение пока не попало в общий конфиг</div>}
    </Pane>
  )
}

// ---------------------------------------------------------------- итог по ноде

function Result({ info, text, blocked }: { info: BaseInfo; text: string; blocked: boolean }) {
  const [node, setNode] = useState<number | null>(info.nodes[0]?.id ?? null)
  const [out, setOut] = useState<{ config: unknown; problems: string[] } | null>(null)
  const [err, setErr] = useState('')

  useEffect(() => {
    if (node === null || blocked) return
    let live = true
    setErr('')
    post<{ config: unknown; problems: string[] }>(`xray/nodes/${node}/config`, { json: text })
      .then((r) => { if (live) setOut(r) }, (e) => { if (live) { setOut(null); setErr(e.message) } })
    return () => { live = false }
  }, [node, text, blocked])

  if (info.nodes.length === 0) return <div className="alert">Нод ещё нет: итоговый конфиг появится, когда добавите ноду.</div>
  return (
    <Pane title="Итог по ноде" hint={<>Конфиг, который нода запустит с вашим общим конфигом, как он сейчас в редакторе (не сохранённым тоже). Inbound’ы из профилей, <span className="mono">api</span>/<span className="mono">stats</span>/<span className="mono">policy</span> панели и выходные правила на месте; пользователи и ключи Reality скрыты. <Info>Так выглядит файл, который получит Xray ноды.</Info></>}>
      <div className="row" style={{ marginBottom: 10 }}>
        <select className="input" style={{ maxWidth: 280 }} value={node ?? ''} onChange={(e) => setNode(+e.target.value)}>
          {info.nodes.map((n) => <option key={n.id} value={n.id}>{n.flag} {n.name} ({n.code})</option>)}
        </select>
      </div>
      {blocked && <div className="alert pink">В общем конфиге ошибка — итог показать нельзя.</div>}
      {err && <div className="alert pink">{err}</div>}
      {out && (out.problems || []).length > 0 && <div className="alert amber" style={{ marginBottom: 10 }}>{(out.problems || []).map((p, i) => <div key={i}>{p}</div>)}</div>}
      {out && !blocked && <CodeEditor lang="json" value={pretty(out.config)} readOnly height="max(260px, calc(100vh - 460px))" />}
    </Pane>
  )
}
