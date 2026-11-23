import { FormEvent, useState } from 'react'
import { del, GeoFileView, get, post } from '../api'
import { ago, bytes } from '../format'
import { Badge, Field, Icon, Loading, Modal, Switch, useAction, useConfirm, useLoad } from '../ui'
import { t } from '../i18n'
import { GeoExplorerModal } from '../components/GeoExplorerModal'

export function GeoFiles() {
  const files = useLoad(() => get<GeoFileView[]>('geo/files'), [], 15000, 'geo-files')
  const act = useAction()
  const confirm = useConfirm()

  const [addOpen, setAddOpen] = useState(false)
  const [url, setUrl] = useState('')
  const [name, setName] = useState('')
  const [title, setTitle] = useState('')
  const [autoUpdate, setAutoUpdate] = useState(true)
  const [replaceSystem, setReplaceSystem] = useState(false)
  const [submitting, setSubmitting] = useState(false)

  const [browseFile, setBrowseFile] = useState<string | null>(null)

  const list = files.data || []
  const missingDefaults = list.some((f) => f.isSystem && !f.available)

  const submitAdd = async (e: FormEvent) => {
    e.preventDefault()
    setSubmitting(true)
    const ok = await act(() => post('geo/files', {
      url: url.trim(),
      name: name.trim(),
      title: title.trim(),
      autoUpdate,
      replaceSystem,
    }), t('warp.geo_added'))
    setSubmitting(false)
    if (ok) {
      setAddOpen(false)
      setUrl('')
      setName('')
      setTitle('')
      setReplaceSystem(false)
      files.reload()
    }
  }

  return (
    <>
      <div className="toolbar" style={{ alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
        <div className="muted small" style={{ flex: 1, minWidth: 220 }}>{t('warp.geo_resources_desc')}</div>
        {missingDefaults && (
          <button
            type="button"
            className="btn"
            onClick={() => act(() => post('geo/files/defaults'), t('warp.geo_defaults_loaded')).then(files.reload)}
          >
            <Icon name="download" /> {t('warp.geo_load_defaults')}
          </button>
        )}
        <button type="button" className="btn primary" onClick={() => setAddOpen(true)}>
          <Icon name="plus" /> {t('warp.geo_add_url')}
        </button>
      </div>

      {!files.data ? <Loading error={files.error} /> : (
        <div className="cards">
          {list.map((f) => (
            <div key={f.name} className="card">
              <div className="card-head">
                <div>
                  <h3 className="row" style={{ gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
                    <span className="mono">{f.name}</span>
                    <Badge>{f.kind}</Badge>
                    {f.isSystemOverride ? (
                      <Badge color="amber">{t('warp.geo_override_badge')}</Badge>
                    ) : f.isSystem ? (
                      <Badge>{t('warp.geo_system_badge')}</Badge>
                    ) : null}
                    {!f.available && <Badge color="amber">{t('warp.geo_missing')}</Badge>}
                  </h3>
                  <div className="card-sub">{f.title}</div>
                </div>
                <div className="row" style={{ gap: 6 }}>
                  {f.available && (
                    <button type="button" className="btn sm" onClick={() => setBrowseFile(f.name)}>
                      <Icon name="search" /> {t('warp.geo_browse')}
                    </button>
                  )}
                  {f.id > 0 && (
                    <button
                      type="button"
                      className="btn sm"
                      onClick={() => act(() => post(`geo/files/${f.id}/refresh`), t('warp.geo_refreshed')).then(files.reload)}
                    >
                      <Icon name="refresh" /> {t('warp.geo_refresh')}
                    </button>
                  )}
                  {f.id > 0 && (
                    <button
                      type="button"
                      className="btn sm danger"
                      onClick={async () => {
                        const msg = f.isSystemOverride
                          ? t('warp.geo_override_reset_confirm', { name: f.name })
                          : t('warp.geo_delete_confirm', { name: f.name })
                        if (await confirm(msg, t('common.delete'))) {
                          act(() => del(`geo/files/${f.id}`), t('warp.geo_deleted')).then(files.reload)
                        }
                      }}
                    >
                      <Icon name="trash" />
                    </button>
                  )}
                </div>
              </div>

              <div className="row small muted" style={{ gap: 14, flexWrap: 'wrap', marginTop: 8, fontVariantNumeric: 'tabular-nums' }}>
                {f.available && <span>{bytes(f.sizeBytes)}</span>}
                {f.available && <span>{t('warp.geo_categories_count', { count: f.categoriesCount })}</span>}
                {f.available && <span>{t('warp.geo_entries_count', { count: f.entriesCount })}</span>}
                {f.fetchedAt > 0 && <span>{ago(f.fetchedAt)}</span>}
                {f.usedByNodes.length > 0 && <span>{t('warp.geo_used_by', { nodes: f.usedByNodes.join(', ') })}</span>}
              </div>

              {f.sourceUrl && (
                <div className="muted small mono" style={{ marginTop: 6, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {f.sourceUrl}
                </div>
              )}
              {f.lastError && (
                <div className="small" style={{ marginTop: 6, color: 'var(--red)' }}>{f.lastError}</div>
              )}
            </div>
          ))}
        </div>
      )}

      {addOpen && (
        <Modal
          title={t('warp.geo_add_url')}
          onClose={() => setAddOpen(false)}
          footer={
            <>
              <button type="button" className="btn" onClick={() => setAddOpen(false)}>{t('common.cancel')}</button>
              <button type="submit" form="add-geo-form" className="btn primary" disabled={submitting || !url.trim()}>
                <Icon name="download" /> {t('common.save')}
              </button>
            </>
          }
        >
          <form id="add-geo-form" onSubmit={submitAdd} style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <Field label={t('warp.geo_url')}>
              <input required value={url} onChange={(e) => setUrl(e.target.value)} placeholder={t('warp.geo_url_placeholder')} autoFocus />
            </Field>
            <div className="grid2">
              <Field label={t('warp.geo_name')}>
                <input value={name} onChange={(e) => setName(e.target.value)} placeholder={t('warp.geo_name_placeholder')} />
              </Field>
              <Field label={t('warp.geo_file_title')}>
                <input value={title} onChange={(e) => setTitle(e.target.value)} />
              </Field>
            </div>
            <label className="row small" style={{ gap: 8, alignItems: 'center', cursor: 'pointer' }}>
              <Switch checked={autoUpdate} onChange={setAutoUpdate} />
              <span>{t('warp.geo_auto_update')}</span>
            </label>
            <label className="row small" style={{ gap: 8, alignItems: 'center', cursor: 'pointer' }}>
              <Switch checked={replaceSystem} onChange={setReplaceSystem} />
              <span>{t('warp.geo_replace_system')}</span>
            </label>
          </form>
        </Modal>
      )}

      <GeoExplorerModal
        open={browseFile !== null}
        onClose={() => setBrowseFile(null)}
        initialTarget={browseFile ? { file: browseFile, code: '' } : null}
      />
    </>
  )
}
