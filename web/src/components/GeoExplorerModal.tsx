import { FormEvent, useCallback, useEffect, useState } from 'react'
import { GeoCategoryDetails, GeoFileView, GeoSearchHit, get, post } from '../api'
import { Badge, Field, Icon, Loading, Modal, Switch, useAction } from '../ui'
import { t } from '../i18n'

interface Props {
  open: boolean
  onClose: () => void
  /** Currently selected rules (for picker mode). When omitted, modal runs in browse-only mode. */
  selected?: string[]
  onToggleRule?: (rule: string) => void
  /** Pre-open a specific file and category (e.g. clicking a chip in RuleListInput). */
  initialTarget?: { file: string; code: string } | null
}

export function GeoExplorerModal({ open, onClose, selected, onToggleRule, initialTarget }: Props) {
  const pickerMode = !!onToggleRule
  const act = useAction()

  const [files, setFiles] = useState<GeoFileView[]>([])
  const [query, setQuery] = useState('')
  const [kind, setKind] = useState<'' | 'geosite' | 'geoip'>('')
  const [fileFilter, setFileFilter] = useState('')
  const [contentMode, setContentMode] = useState(false)
  const [hits, setHits] = useState<GeoSearchHit[]>([])
  const [searching, setSearching] = useState(false)

  const [active, setActive] = useState<{ file: string; code: string; rule: string; kind: 'geosite' | 'geoip' } | null>(null)
  const [details, setDetails] = useState<GeoCategoryDetails | null>(null)
  const [loadingDet, setLoadingDet] = useState(false)
  const [innerQuery, setInnerQuery] = useState('')
  const [innerAttr, setInnerAttr] = useState('')

  const [showAdd, setShowAdd] = useState(false)
  const [addUrl, setAddUrl] = useState('')
  const [addName, setAddName] = useState('')
  const [addAuto, setAddAuto] = useState(true)
  const [addReplace, setAddReplace] = useState(false)
  const [adding, setAdding] = useState(false)

  const loadFiles = useCallback(async () => {
    try {
      const list = await get<GeoFileView[]>('geo/files')
      setFiles(list || [])
    } catch { /* ignore */ }
  }, [])

  useEffect(() => {
    if (!open) return
    loadFiles()
    if (initialTarget) {
      const rule = initialTarget.file === 'geosite.dat'
        ? `geosite:${initialTarget.code}`
        : initialTarget.file === 'geoip.dat'
          ? `geoip:${initialTarget.code}`
          : `ext-domain:${initialTarget.file}:${initialTarget.code}`
      setActive({ file: initialTarget.file, code: initialTarget.code, rule, kind: initialTarget.file.includes('geoip') ? 'geoip' : 'geosite' })
      setInnerQuery('')
      setInnerAttr('')
    }
  }, [open, initialTarget, loadFiles])

  const runSearch = useCallback(async () => {
    if (!open) return
    setSearching(true)
    try {
      const params = new URLSearchParams()
      if (query.trim()) params.set('q', query.trim())
      if (kind) params.set('kind', kind)
      if (fileFilter) params.set('file', fileFilter)
      if (contentMode && query.trim()) params.set('mode', 'content')
      params.set('limit', '120')
      const res = await get<GeoSearchHit[]>(`geo/search?${params.toString()}`)
      setHits(res || [])
    } catch {
      setHits([])
    } finally {
      setSearching(false)
    }
  }, [open, query, kind, fileFilter, contentMode])

  useEffect(() => {
    if (!open) return
    const tm = setTimeout(runSearch, contentMode ? 260 : 120)
    return () => clearTimeout(tm)
  }, [open, runSearch, contentMode])

  useEffect(() => {
    if (!open || !active) { setDetails(null); return }
    let cancelled = false
    setLoadingDet(true)
    const params = new URLSearchParams()
    if (innerQuery.trim()) params.set('q', innerQuery.trim())
    if (innerAttr) params.set('attr', innerAttr)
    params.set('limit', '300')
    get<GeoCategoryDetails>(`geo/files/${encodeURIComponent(active.file)}/categories/${encodeURIComponent(active.code)}?${params.toString()}`)
      .then((d) => { if (!cancelled) setDetails(d) })
      .catch(() => { if (!cancelled) setDetails(null) })
      .finally(() => { if (!cancelled) setLoadingDet(false) })
    return () => { cancelled = true }
  }, [open, active, innerQuery, innerAttr])

  if (!open) return null

  const selectedSet = new Set((selected || []).map((s) => s.trim().toLowerCase()))
  const isSelected = (rule: string) => selectedSet.has(rule.toLowerCase())

  const submitAdd = async (e: FormEvent) => {
    e.preventDefault()
    setAdding(true)
    const ok = await act(() => post('geo/files', {
      url: addUrl.trim(),
      name: addName.trim(),
      autoUpdate: addAuto,
      replaceSystem: addReplace,
    }), t('warp.geo_added'))
    setAdding(false)
    if (ok) {
      setShowAdd(false)
      setAddUrl('')
      setAddName('')
      setAddReplace(false)
      await loadFiles()
      await runSearch()
    }
  }

  const anyAvailable = files.some((f) => f.available)

  return (
    <Modal title={t('warp.geo_title')} onClose={onClose} wide footer={
      <div className="row" style={{ width: '100%', justifyContent: 'space-between', alignItems: 'center' }}>
        <span className="muted small">
          {pickerMode ? t('warp.geo_selected_count', { count: selected?.length || 0 }) : ''}
        </span>
        <div className="row" style={{ gap: 8 }}>
          <button type="button" className="btn" onClick={() => setShowAdd((v) => !v)}>
            <Icon name="plus" /> {t('warp.geo_add_url')}
          </button>
          <button type="button" className="btn primary" onClick={onClose}>{t('warp.geo_done')}</button>
        </div>
      </div>
    }>
      {showAdd && (
        <form onSubmit={submitAdd} className="card" style={{ marginBottom: 12, padding: 14 }}>
          <Field label={t('warp.geo_url')}>
            <input required value={addUrl} onChange={(e) => setAddUrl(e.target.value)} placeholder={t('warp.geo_url_placeholder')} />
          </Field>
          <div className="grid2">
            <Field label={t('warp.geo_name')}>
              <input value={addName} onChange={(e) => setAddName(e.target.value)} placeholder={t('warp.geo_name_placeholder')} />
            </Field>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8, justifyContent: 'center' }}>
              <label className="row small" style={{ gap: 8, alignItems: 'center', cursor: 'pointer' }}>
                <Switch checked={addAuto} onChange={setAddAuto} />
                <span>{t('warp.geo_auto_update')}</span>
              </label>
              <label className="row small" style={{ gap: 8, alignItems: 'center', cursor: 'pointer' }}>
                <Switch checked={addReplace} onChange={setAddReplace} />
                <span>{t('warp.geo_replace_system')}</span>
              </label>
            </div>
          </div>
          <div className="row" style={{ gap: 8, justifyContent: 'flex-end', marginTop: 8 }}>
            <button type="button" className="btn sm" onClick={() => setShowAdd(false)}>{t('common.cancel')}</button>
            <button type="submit" className="btn primary sm" disabled={adding || !addUrl.trim()}>
              <Icon name="download" /> {t('common.save')}
            </button>
          </div>
        </form>
      )}

      {!anyAvailable && files.length > 0 && (
        <div className="row" style={{ marginBottom: 12, padding: '10px 12px', borderRadius: 10, border: '1px solid var(--border)', justifyContent: 'space-between', alignItems: 'center', gap: 10 }}>
          <span className="muted small">{t('warp.geo_missing')}</span>
          <button type="button" className="btn sm primary" onClick={async () => {
            const ok = await act(() => post('geo/files/defaults'), t('warp.geo_defaults_loaded'))
            if (ok) { await loadFiles(); await runSearch() }
          }}>
            <Icon name="download" /> {t('warp.geo_load_defaults')}
          </button>
        </div>
      )}

      <div style={{ display: 'grid', gridTemplateColumns: 'minmax(260px, 1fr) minmax(300px, 1.25fr)', gap: 12, minHeight: 420 }}>
        {/* Left column: search and category list */}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8, border: '1px solid var(--border)', borderRadius: 10, padding: 10, minWidth: 0 }}>
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t('warp.geo_search_placeholder')}
            autoFocus
          />
          <div className="row" style={{ gap: 6, flexWrap: 'wrap' }}>
            <select value={fileFilter} onChange={(e) => setFileFilter(e.target.value)} style={{ flex: 1, minWidth: 110 }}>
              <option value="">{t('warp.geo_all_files')}</option>
              {files.filter((f) => f.available).map((f) => (
                <option key={f.name} value={f.name}>{f.name}</option>
              ))}
            </select>
            <div className="row" style={{ gap: 4 }}>
              {(['', 'geosite', 'geoip'] as const).map((k) => (
                <button
                  key={k || 'all'}
                  type="button"
                  className={kind === k ? 'btn sm primary' : 'btn sm'}
                  onClick={() => setKind(k)}
                >
                  {k === '' ? t('warp.geo_kind_all') : k === 'geosite' ? t('warp.geo_kind_geosite') : t('warp.geo_kind_geoip')}
                </button>
              ))}
            </div>
          </div>
          <label className="row small" style={{ gap: 8, alignItems: 'center', cursor: 'pointer' }}>
            <Switch checked={contentMode} onChange={setContentMode} />
            <span>{t('warp.geo_search_content')}</span>
          </label>

          <div style={{ flex: 1, overflowY: 'auto', maxHeight: 340, display: 'flex', flexDirection: 'column', gap: 2 }}>
            {searching ? <Loading /> : hits.length === 0 ? (
              <div className="muted small" style={{ padding: 16, textAlign: 'center' }}>{t('warp.geo_no_results')}</div>
            ) : hits.map((h) => {
              const isAct = active?.file === h.file && active?.code === h.code
              const checked = isSelected(h.rule)
              return (
                <div
                  key={`${h.file}:${h.code}`}
                  className="row"
                  style={{
                    padding: '6px 8px',
                    borderRadius: 8,
                    gap: 8,
                    alignItems: 'center',
                    cursor: 'pointer',
                    background: isAct ? 'var(--surface-2, rgba(57,197,187,0.12))' : 'transparent',
                    border: isAct ? '1px solid var(--miku, #39c5bb)' : '1px solid transparent',
                  }}
                  onClick={() => { setActive({ file: h.file, code: h.code, rule: h.rule, kind: h.kind }); setInnerQuery(''); setInnerAttr('') }}
                >
                  {pickerMode && (
                    <input
                      type="checkbox"
                      checked={checked}
                      onChange={() => onToggleRule?.(h.rule)}
                      onClick={(e) => e.stopPropagation()}
                      style={{ width: 'auto', margin: 0 }}
                    />
                  )}
                  <span className="mono small" style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{h.rule}</span>
                  <span className="muted small" style={{ fontVariantNumeric: 'tabular-nums' }}>{h.count}</span>
                </div>
              )
            })}
          </div>
        </div>

        {/* Right column: category contents */}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8, border: '1px solid var(--border)', borderRadius: 10, padding: 10, minWidth: 0 }}>
          {!active ? (
            <div className="muted small" style={{ padding: 24, textAlign: 'center', margin: 'auto' }}>{t('warp.geo_select_category')}</div>
          ) : loadingDet ? <Loading /> : !details ? (
            <div className="muted small" style={{ padding: 24, textAlign: 'center' }}>{t('warp.geo_no_results')}</div>
          ) : (
            <>
              <div className="row" style={{ justifyContent: 'space-between', alignItems: 'center', gap: 8 }}>
                <div>
                  <div className="mono" style={{ fontWeight: 600 }}>{active.rule}</div>
                  <div className="muted small">{t('warp.geo_source', { file: details.file })} · {t('warp.geo_entries_count', { count: details.total })}</div>
                </div>
                {pickerMode && (
                  <button
                    type="button"
                    className={isSelected(innerAttr ? `${active.rule}@${innerAttr}` : active.rule) ? 'btn sm' : 'btn sm primary'}
                    onClick={() => onToggleRule?.(innerAttr ? `${active.rule}@${innerAttr}` : active.rule)}
                  >
                    <Icon name={isSelected(innerAttr ? `${active.rule}@${innerAttr}` : active.rule) ? 'check' : 'plus'} />{' '}
                    {innerAttr ? t('warp.geo_add_attr', { rule: `@${innerAttr}` }) : active.code}
                  </button>
                )}
              </div>

              {details.kind === 'geosite' && details.attributes && Object.keys(details.attributes).length > 0 && (
                <div className="row" style={{ gap: 4, flexWrap: 'wrap' }}>
                  <button type="button" className={innerAttr === '' ? 'btn sm primary' : 'btn sm'} onClick={() => setInnerAttr('')}>
                    {t('warp.geo_attr_all', { count: details.total })}
                  </button>
                  {Object.entries(details.attributes).sort(([a], [b]) => a.localeCompare(b)).slice(0, 12).map(([a, cnt]) => (
                    <button key={a} type="button" className={innerAttr === a ? 'btn sm primary' : 'btn sm'} onClick={() => setInnerAttr(a)}>
                      @{a} ({cnt})
                    </button>
                  ))}
                </div>
              )}

              <input
                value={innerQuery}
                onChange={(e) => setInnerQuery(e.target.value)}
                placeholder={t('warp.geo_filter_inside')}
              />

              <div style={{ flex: 1, overflowY: 'auto', maxHeight: 300, display: 'flex', flexDirection: 'column', gap: 3 }}>
                {details.kind === 'geosite' ? (
                  (details.domains || []).map((d, idx) => (
                    <div key={`${d.rule}-${idx}`} className="row small" style={{ gap: 8, padding: '3px 4px', alignItems: 'center' }}>
                      <Badge>{d.type}</Badge>
                      <span className="mono" style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{d.value}</span>
                      {d.attrs && d.attrs.length > 0 && (
                        <span className="muted">{d.attrs.map((a) => `@${a}`).join(' ')}</span>
                      )}
                    </div>
                  ))
                ) : (
                  (details.ips || []).map((ip, idx) => (
                    <div key={`${ip.cidr}-${idx}`} className="row small" style={{ gap: 8, padding: '3px 4px', alignItems: 'center' }}>
                      <Badge>IPv{ip.family}</Badge>
                      <span className="mono" style={{ fontVariantNumeric: 'tabular-nums' }}>{ip.cidr}</span>
                    </div>
                  ))
                )}
              </div>
            </>
          )}
        </div>
      </div>
    </Modal>
  )
}
