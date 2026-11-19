import { useEffect, useState } from 'react'
import { get, Inbound, Node, post, put } from '../api'
import { CodeEditor, parseObject, pretty } from '../code'
import { ago, bps, bytes, nodeStateInfo, uptime } from '../format'
import { Badge, Bar, CopyField, Empty, Icon, Loading, Tabs, useAction, useLoad } from '../ui'
import { t } from '../i18n'

type NodeXrayBaseResp = {
  mode: 'inherit' | 'custom'
  nodeJson?: string
  globalJson: string
}

type NodeCompiledResp = {
  xrayConfig: string
  nginxConfig: string
  revision?: string
  appliedRevision?: string
  lastError?: string
}

export function NodeInspector({
  node,
  onBack,
  onAttach,
  onEditHost,
  onEditConfig,
  onReload,
}: {
  node: Node
  onBack: () => void
  onAttach: () => void
  onEditHost: (inb: Inbound) => void
  onEditConfig: (inb: Inbound) => void
  onReload: () => void
}) {
  const [tab, setTab] = useState<'inbounds' | 'base' | 'compiled'>('inbounds')
  const [compiledView, setCompiledView] = useState<'xray' | 'nginx'>('xray')
  const [showSecrets, setShowSecrets] = useState(false)
  const act = useAction()

  const st = nodeStateInfo[node.state] || { label: node.state, color: '' }
  const online = node.state === 'in sync' || node.state === 'syncing'
  const m = node.metrics

  // Per-node Xray base (GET /api/nodes/:id/xray-base with graceful fallback to global xray/base)
  const base = useLoad<NodeXrayBaseResp>(async () => {
    try {
      return await get<NodeXrayBaseResp>(`nodes/${node.id}/xray-base`)
    } catch {
      const g = await get<{ json: string }>('xray/base')
      return { mode: 'inherit', globalJson: g.json || '{}' }
    }
  }, [node.id])

  const [baseMode, setBaseMode] = useState<'inherit' | 'custom'>('inherit')
  const [baseText, setBaseText] = useState('')
  const [baseDirty, setBaseDirty] = useState(false)

  useEffect(() => {
    if (!base.data) return
    setBaseMode(base.data.mode)
    setBaseText(base.data.mode === 'custom' && base.data.nodeJson ? base.data.nodeJson : base.data.globalJson)
    setBaseDirty(false)
  }, [base.data])

  // Compiled Xray + Nginx (GET /api/nodes/:id/compiled with graceful fallback to xray/effective)
  const compiled = useLoad<NodeCompiledResp>(async () => {
    try {
      return await get<NodeCompiledResp>(`nodes/${node.id}/compiled?secrets=${showSecrets ? 1 : 0}`)
    } catch {
      const eff = await get<{ json: string }>(`xray/effective?nodeId=${node.id}`)
      return {
        xrayConfig: eff.json || '{}',
        nginxConfig: '# nginx.conf preview requires updated panel backend (GET /api/nodes/:id/compiled)',
      }
    }
  }, [node.id, showSecrets, tab === 'compiled'])

  const saveBase = async () => {
    if (baseMode === 'custom') {
      const parsed = parseObject(baseText)
      if ('error' in parsed) {
        await act(() => Promise.reject(new Error(parsed.error)))
        return
      }
    }
    const r = await act(
      () => put(`nodes/${node.id}/xray-base`, { mode: baseMode, json: baseMode === 'custom' ? baseText : '' }),
      t('common.saved'),
    )
    if (r !== undefined) {
      setBaseDirty(false)
      base.reload()
      onReload()
    }
  }

  return (
    <div>
      <div className="inspector-head">
        <div className="inspector-title">
          <button className="btn sm ghost" onClick={onBack}>← {t('common.nodes')}</button>
          <span className="flag">{node.flag || '🌐'}</span>
          <div>
            <div className="row" style={{ gap: 8 }}>
              <h2>{node.name}</h2>
              <span className="muted mono small">{node.code}</span>
              <Badge color={st.color} dot>{st.label}</Badge>
              {!node.enabled && <Badge color="amber">{t('nodes.node_turned_off')}</Badge>}
            </div>
            <div className="muted small">
              {node.domain || t('nodes.no_domain')} · {ago(node.lastSeenAt)}
              {m ? ` · uptime ${uptime(m.uptime)}` : ''}
            </div>
          </div>
        </div>
        <div className="row" style={{ gap: 8 }}>
          <button className="btn sm" disabled={!online} onClick={() => act(() => post(`nodes/${node.id}/action`, { action: 'resync' }), t('nodes.config_sent')).then(onReload)}>
            <Icon name="refresh" /> {t('nodes.resend_the_config')}
          </button>
          <button className="btn sm primary" onClick={onAttach}>
            <Icon name="plus" /> {t('common.add')}
          </button>
        </div>
      </div>

      {m && (
        <div className="stats" style={{ marginBottom: 20 }}>
          <div className="card stat">
            <div className="k">CPU</div>
            <div className="v">{m.cpu.toFixed(1)}%</div>
            <div style={{ marginTop: 8 }}><Bar value={m.cpu} max={100} /></div>
          </div>
          <div className="card stat">
            <div className="k">RAM</div>
            <div className="v">{m.memTotal ? `${Math.round((m.memUsed / m.memTotal) * 100)}%` : '—'}</div>
            <div className="s">{bytes(m.memUsed)} / {bytes(m.memTotal)}</div>
          </div>
          <div className="card stat">
            <div className="k">{t('common.traffic')}</div>
            <div className="v">{bps(m.rxBps + m.txBps)}</div>
            <div className="s">↓ {bps(m.rxBps)} · ↑ {bps(m.txBps)}</div>
          </div>
          <div className="card stat">
            <div className="k">Xray</div>
            <div className="v" style={{ fontSize: 20 }}>{node.xrayVersion || '—'}</div>
            <div className="s">{node.inbounds.length} inbounds</div>
          </div>
        </div>
      )}

      <Tabs
        value={tab}
        onChange={(v) => setTab(v as 'inbounds' | 'base' | 'compiled')}
        items={[
          { id: 'inbounds', label: 'Inbounds', count: node.inbounds.length },
          { id: 'base', label: 'Xray Base (Per-Node)' },
          { id: 'compiled', label: 'xray.json & nginx.conf' },
        ]}
      />

      {tab === 'inbounds' && (
        <div className="card">
          <div className="card-head">
            <div>
              <h3>Inbounds</h3>
              <div className="card-sub">{node.domain || node.addresses?.[0]?.ip || ''}</div>
            </div>
            <button className="btn sm" onClick={onAttach}><Icon name="plus" /> {t('common.add')}</button>
          </div>
          {node.inbounds.length === 0 ? (
            <Empty>{t('nodes.without_profiles')}</Empty>
          ) : (
            <div className="table-wrap">
              <table className="table">
                <thead>
                  <tr>
                    <th>Tag</th>
                    <th>{t('common.profiles')}</th>
                    <th>Port / Host</th>
                    <th style={{ width: 180 }} />
                  </tr>
                </thead>
                <tbody>
                  {node.inbounds.map((inb) => (
                    <tr key={inb.id}>
                      <td className="mono">{inb.tag}</td>
                      <td>
                        <div className="row" style={{ gap: 6 }}>
                          <span>{inb.profileName || `#${inb.profileId}`}</span>
                          {!inb.enabled && <Badge color="amber">off</Badge>}
                        </div>
                      </td>
                      <td className="mono small text-2">
                        {inb.port} {inb.host?.sni ? `· ${inb.host.sni}` : ''}
                      </td>
                      <td className="nowrap" style={{ textAlign: 'right' }}>
                        <div className="row" style={{ justifyContent: 'flex-end', gap: 6 }}>
                          <button className="btn sm ghost" onClick={() => onEditHost(inb)}>{t('nodes.connection_point')}</button>
                          <button className="btn sm" onClick={() => onEditConfig(inb)}>
                            <Icon name="code" /> JSON
                          </button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      {tab === 'base' && (
        <div className="card card-pad">
          {!base.data ? <Loading error={base.error} /> : (
            <>
              <div className="row between" style={{ marginBottom: 16 }}>
                <div className="seg sm">
                  <button type="button" className={baseMode === 'inherit' ? 'on' : ''} onClick={() => { setBaseMode('inherit'); setBaseText(base.data!.globalJson); setBaseDirty(base.data!.mode !== 'inherit') }}>
                    Inherit global Xray base
                  </button>
                  <button type="button" className={baseMode === 'custom' ? 'on' : ''} onClick={() => { setBaseMode('custom'); setBaseDirty(true) }}>
                    Custom per-node Xray base
                  </button>
                </div>
                <div className="row" style={{ gap: 8 }}>
                  {baseMode === 'custom' && (
                    <button className="btn sm ghost" onClick={() => {
                      const p = parseObject(baseText)
                      if (!('error' in p)) setBaseText(pretty(p.value))
                    }}>Format JSON</button>
                  )}
                  <button className="btn sm primary" disabled={!baseDirty} onClick={saveBase}>
                    {t('common.save')}
                  </button>
                </div>
              </div>
              <CodeEditor
                lang="json"
                value={baseText}
                onChange={(v) => { setBaseText(v); setBaseDirty(true) }}
                readOnly={baseMode === 'inherit'}
                height={460}
              />
            </>
          )}
        </div>
      )}

      {tab === 'compiled' && (
        <div className="card card-pad">
          {!compiled.data ? <Loading error={compiled.error} /> : (
            <>
              <div className="row between" style={{ marginBottom: 14 }}>
                <div className="seg sm">
                  <button type="button" className={compiledView === 'xray' ? 'on' : ''} onClick={() => setCompiledView('xray')}>xray/config.json</button>
                  <button type="button" className={compiledView === 'nginx' ? 'on' : ''} onClick={() => setCompiledView('nginx')}>nginx.conf</button>
                </div>
                <div className="row" style={{ gap: 12 }}>
                  {compiledView === 'xray' && (
                    <label className="check small">
                      <input type="checkbox" checked={showSecrets} onChange={(e) => setShowSecrets(e.target.checked)} />
                      <span>Show keys &amp; UUIDs</span>
                    </label>
                  )}
                  {compiled.data.revision && (
                    <span className="muted mono small">rev {compiled.data.revision.slice(0, 8)}</span>
                  )}
                </div>
              </div>
              {compiled.data.lastError && (
                <div className="alert pink" style={{ marginBottom: 12 }}>{compiled.data.lastError}</div>
              )}
              {compiledView === 'xray' ? (
                <CodeEditor lang="json" value={compiled.data.xrayConfig} readOnly height={500} />
              ) : (
                <pre className="code" style={{ maxHeight: 500, fontSize: 12.5, lineHeight: 1.55 }}>{compiled.data.nginxConfig}</pre>
              )}
            </>
          )}
        </div>
      )}

      {node.addresses?.length > 0 && (
        <div className="card card-pad section-gap">
          <div className="label" style={{ marginBottom: 8 }}>Address</div>
          <CopyField value={node.addresses[0].ip} />
        </div>
      )}
    </div>
  )
}
