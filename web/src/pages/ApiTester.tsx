import { useEffect, useMemo, useState } from 'react'
import { ApiToken, Integrations, post } from '../api'
import { CodeEditor } from '../code'
import { Badge, copyText, Field, Info, Loading, useAction, useToast } from '../ui'
import { Icon } from '../ui'
import { t } from '../i18n'
import { snippet, snippetLangs, SnippetLang } from '../apiSnippets'

// The tester reads the same OpenAPI description integrators get, so every endpoint is here and the
// page cannot drift from the API.

interface Param { name: string; in: 'path' | 'query'; required: boolean; description?: string; enum?: string[]; def?: string }
interface Op {
  id: string
  method: string
  path: string
  summary: string
  description?: string
  tag: string
  scope: string
  params: Param[]
  body?: string // example JSON text; undefined = the endpoint takes no body
  bodyRequired: boolean
  idem: boolean
}

type Spec = { paths: Record<string, any>; components?: any }

function resolve(spec: Spec, v: any): any {
  const ref: string | undefined = v?.$ref
  if (!ref) return v
  return ref.replace(/^#\//, '').split('/').reduce((o: any, k) => o?.[k], spec)
}

// A body to start from: the example of the spec, else a skeleton of the schema.
function skeleton(spec: Spec, schema: any, depth = 0): any {
  schema = resolve(spec, schema)
  if (!schema || depth > 3) return null
  if (schema.example !== undefined) return schema.example
  if (schema.allOf) return Object.assign({}, ...schema.allOf.map((s: any) => skeleton(spec, s, depth + 1)))
  switch (schema.type) {
    case 'object': {
      const out: Record<string, any> = {}
      for (const k of schema.required ?? []) out[k] = skeleton(spec, schema.properties?.[k], depth + 1)
      return out
    }
    case 'array': return []
    case 'integer': case 'number': return 0
    case 'boolean': return false
    default: return schema.enum ? schema.enum[0] : ''
  }
}

const methodOrder = ['GET', 'POST', 'PATCH', 'PUT', 'DELETE']

// The operations in the order of the document: tags as listed, then by path and method.
function parseSpec(spec: Spec & { tags?: { name: string }[] }): Op[] {
  const ops: Op[] = []
  for (const [path, item] of Object.entries(spec.paths)) {
    for (const [method, op] of Object.entries<any>(item)) {
      if (method === 'parameters') continue
      const raw = [...(item.parameters ?? []), ...(op.parameters ?? [])].map((p: any) => resolve(spec, p))
      const params: Param[] = raw.filter((p: any) => p.in === 'path' || p.in === 'query').map((p: any) => ({
        name: p.name, in: p.in, required: !!p.required, description: p.description, enum: p.schema?.enum, def: p.schema?.default !== undefined ? String(p.schema.default) : undefined,
      }))
      const json = op.requestBody?.content?.['application/json']
      let body: string | undefined
      if (json) {
        const ex = json.example ?? skeleton(spec, json.schema)
        body = JSON.stringify(ex, null, 2)
      }
      ops.push({
        id: op.operationId, method: method.toUpperCase(), path, summary: op.summary, description: op.description, tag: op.tags?.[0] ?? '', scope: op['x-required-scope'] ?? '',
        params, body, bodyRequired: !!op.requestBody?.required, idem: raw.some((p: any) => p.name === 'Idempotency-Key'),
      })
    }
  }
  const order = (spec.tags ?? []).map((x) => x.name)
  return ops.sort((a, b) => order.indexOf(a.tag) - order.indexOf(b.tag) || a.path.localeCompare(b.path) || methodOrder.indexOf(a.method) - methodOrder.indexOf(b.method))
}

interface Reply { status: number; ms: number; headers: [string, string][]; body: string }

const methodColor: Record<string, string> = { GET: 'green', POST: 'amber', PATCH: 'amber', PUT: 'amber', DELETE: 'pink' }
const shownHeaders = ['x-request-id', 'x-ratelimit-limit', 'x-ratelimit-remaining', 'retry-after', 'idempotent-replayed']

export function Tester({ d, token, setToken, reloadTokens }: { d: Integrations; token: string; setToken: (v: string) => void; reloadTokens: () => void }) {
  const [ops, setOps] = useState<Op[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [sel, setSel] = useState('getMe')
  const [pathVals, setPathVals] = useState<Record<string, string>>({})
  const [queryVals, setQueryVals] = useState<Record<string, string>>({})
  const [body, setBody] = useState('')
  const [bodyErr, setBodyErr] = useState<string | null>(null)
  const [idemOn, setIdemOn] = useState(false)
  const [idemKey, setIdemKey] = useState(() => crypto.randomUUID())
  const [reply, setReply] = useState<Reply | null>(null)
  const [busy, setBusy] = useState(false)
  const [lastId, setLastId] = useState('')
  const [lang, setLang] = useState<SnippetLang>('curl')
  const act = useAction()
  const toast = useToast()

  useEffect(() => {
    fetch('api/v1/openapi.json').then((r) => r.json()).then((s: Spec) => setOps(parseSpec(s))).catch((e) => setError(String(e)))
  }, [])
  const op = useMemo(() => ops?.find((o) => o.id === sel), [ops, sel])

  // A new request starts from its own example and from the id of the last user seen.
  useEffect(() => {
    if (!op) return
    setBody(op.body ?? '')
    setPathVals(Object.fromEntries(op.params.filter((p) => p.in === 'path').map((p) => [p.name, p.name === 'id' ? lastId : ''])))
    setQueryVals(Object.fromEntries(op.params.filter((p) => p.in === 'query').map((p) => [p.name, p.def ?? ''])))
    setReply(null)
    setIdemKey(crypto.randomUUID())
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [op?.id])

  if (!ops) return <Loading error={error} />
  if (!op) return null

  const groups = ops.reduce<Record<string, Op[]>>((m, o) => ((m[o.tag] ??= []).push(o), m), {})
  const needsToken = op.scope !== '-'
  const query = new URLSearchParams(Object.entries(queryVals).filter(([, v]) => v !== '')).toString()
  const path = op.path.replace(/\{(\w+)\}/g, (_, k) => encodeURIComponent(pathVals[k] || `{${k}}`)) + (query ? '?' + query : '')
  const missing = op.params.filter((p) => p.in === 'path' && !pathVals[p.name])
  const hasBody = op.body !== undefined

  const send = async () => {
    setBusy(true)
    const started = performance.now()
    try {
      const headers: Record<string, string> = {}
      if (needsToken && token) headers.Authorization = 'Bearer ' + token
      if (hasBody) headers['Content-Type'] = 'application/json'
      if (op.idem && idemOn) headers['Idempotency-Key'] = idemKey
      const res = await fetch('api/v1' + path, { method: op.method, headers, body: hasBody ? body : undefined, credentials: 'omit' })
      const text = await res.text()
      let shown = text
      try {
        const j = JSON.parse(text)
        shown = JSON.stringify(j, null, 2)
        // Remember a user id, so the next request about "the user" is ready.
        const id = op.path.startsWith('/users') ? (j?.id ?? j?.items?.[0]?.id) : undefined
        if (typeof id === 'number' && !lastId) setLastId(String(id))
        if (typeof id === 'number' && op.method === 'POST' && op.path === '/users') setLastId(String(id))
      } catch { /* не JSON: показываем как есть */ }
      setReply({
        status: res.status, ms: Math.round(performance.now() - started), body: shown,
        headers: shownHeaders.flatMap((h) => (res.headers.get(h) ? [[h, res.headers.get(h)!] as [string, string]] : [])),
      })
    } catch (e: any) {
      setReply({ status: 0, ms: Math.round(performance.now() - started), body: String(e?.message ?? e), headers: [] })
    } finally {
      setBusy(false)
    }
  }

  const temp = async () => {
    const stamp = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
    const r = await act(() => post<{ token: ApiToken; secret: string }>('integrations/tokens', {
      name: t('integrations.t_temp_name', { time: stamp }), scopes: d.scopes.filter((s) => s !== 'users:delete'), expiresAt: Math.floor(Date.now() / 1000) + 3600,
    }))
    if (r) { setToken(r.secret); reloadTokens() }
  }

  const code = snippet(lang, { method: op.method, url: d.baseUrl + path, body: hasBody ? body : undefined, idempotencyKey: op.idem && idemOn ? idemKey : undefined })
  const canSend = !busy && !bodyErr && missing.length === 0 && (!needsToken || !!token)

  return (
    <div className="api-tester">
      <div className="card" style={{ marginBottom: 16 }}>
        <div className="card-pad">
          <Field label={t('integrations.t_token')} help={t('integrations.t_token_help')}>
            <div className="row" style={{ flexWrap: 'nowrap' }}>
              <input className="input mono grow" type="password" autoComplete="off" value={token} placeholder="vyn_…" onChange={(e) => setToken(e.target.value.trim())} />
              <button className="btn" onClick={temp}><Icon name="key" />{' '}{t('integrations.t_temp')}</button>
              <Info>{t('integrations.t_temp_info')}</Info>
            </div>
          </Field>
        </div>
      </div>

      <div className="api-tester-grid">
        <div className="card api-ops">
          {Object.entries(groups).map(([tag, list]) => (
            <div key={tag}>
              <div className="nav-section" style={{ padding: '10px 16px 4px' }}>{tag}</div>
              {list.map((o) => (
                <button key={o.id} className={'api-op' + (o.id === sel ? ' on' : '')} onClick={() => setSel(o.id)}>
                  <span className={'api-method ' + (methodColor[o.method] ?? '')}>{o.method}</span>
                  <span className="mono small" style={{ wordBreak: 'break-all' }}>{o.path}</span>
                </button>
              ))}
            </div>
          ))}
        </div>

        <div className="cards">
          <div className="card">
            <div className="card-head">
              <div>
                <h3><span className={'api-method ' + (methodColor[op.method] ?? '')}>{op.method}</span>{' '}<span className="mono">{op.path}</span></h3>
                <div className="card-sub">{op.summary}{op.description && <Info>{op.description}</Info>}</div>
              </div>
              <Badge color={op.scope === '-' ? undefined : 'green'}>{op.scope === '-' ? t('integrations.t_no_token_needed') : op.scope === '' ? t('integrations.t_any_token') : op.scope}</Badge>
            </div>
            <div className="card-pad">
              {op.params.filter((p) => p.in === 'path').length > 0 && (
                <div className="grid2">
                  {op.params.filter((p) => p.in === 'path').map((p) => (
                    <Field key={p.name} label={<span className="mono">{p.name}</span>} help={p.name === 'id' && lastId && pathVals.id === lastId ? t('integrations.t_filled') : p.description}>
                      <input className="input mono" value={pathVals[p.name] ?? ''} onChange={(e) => setPathVals({ ...pathVals, [p.name]: e.target.value })} />
                    </Field>
                  ))}
                </div>
              )}
              {op.params.filter((p) => p.in === 'query').length > 0 && (
                <div className="grid2">
                  {op.params.filter((p) => p.in === 'query').map((p) => (
                    <Field key={p.name} label={<span className="mono">{p.name}</span>} help={p.description}>
                      {p.enum ? (
                        <select className="input" value={queryVals[p.name] ?? ''} onChange={(e) => setQueryVals({ ...queryVals, [p.name]: e.target.value })}>
                          <option value="" />
                          {p.enum.map((v) => <option key={v} value={v}>{v}</option>)}
                        </select>
                      ) : <input className="input mono" value={queryVals[p.name] ?? ''} onChange={(e) => setQueryVals({ ...queryVals, [p.name]: e.target.value })} />}
                    </Field>
                  ))}
                </div>
              )}
              {hasBody && (
                <Field label={t('integrations.t_body')}>
                  <CodeEditor lang="json" value={body} onChange={setBody} height={Math.min(320, 60 + body.split('\n').length * 20)} onValidity={setBodyErr} />
                </Field>
              )}
              <div className="row" style={{ marginTop: 6 }}>
                <button className="btn primary" disabled={!canSend} onClick={send}><Icon name="external" />{' '}{t('integrations.t_send')}</button>
                {op.idem && (
                  <label className="check">
                    <input type="checkbox" checked={idemOn} onChange={(e) => setIdemOn(e.target.checked)} />
                    <span>{t('integrations.t_idem')}</span><Info>{t('integrations.t_idem_info')}</Info>
                  </label>
                )}
                <span className="grow" />
                {needsToken && !token && <span className="small muted">{t('integrations.t_no_token')}</span>}
                {bodyErr && <span className="small pink-text">{t('integrations.t_bad_body', { error: bodyErr })}</span>}
              </div>
              <div className="small muted" style={{ marginTop: 8 }}>{t('integrations.t_real')}</div>
            </div>
          </div>

          {reply && (
            <div className="card">
              <div className="card-head">
                <h3>{t('integrations.t_response')}{' '}
                  <Badge color={reply.status >= 200 && reply.status < 300 ? 'green' : reply.status >= 400 || reply.status === 0 ? 'pink' : 'amber'} dot>{reply.status || 'error'}</Badge>
                  {' '}<span className="muted small">{reply.ms} ms</span></h3>
              </div>
              <div className="card-pad">
                {reply.headers.length > 0 && (
                  <div className="small mono muted" style={{ marginBottom: 8 }}>{reply.headers.map(([k, v]) => <div key={k}>{k}: {v}</div>)}</div>
                )}
                {reply.body && <CodeEditor lang="json" value={reply.body} readOnly height={Math.min(420, 40 + reply.body.split('\n').length * 20)} />}
              </div>
            </div>
          )}

          <div className="card">
            <div className="card-head">
              <h3>{t('integrations.t_code')}</h3>
              <div className="seg sm">
                {snippetLangs.map((l) => <button key={l.id} className={lang === l.id ? 'on' : ''} onClick={() => setLang(l.id)}>{l.label}</button>)}
              </div>
            </div>
            <div className="card-pad">
              <pre className="snippet" style={{ marginTop: 0 }}>{code}</pre>
              <button className="btn sm" onClick={() => copyText(code).then(() => toast(t('integrations.copy_secret_done')))}><Icon name="copy" />{' '}{t('ui.copy')}</button>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
