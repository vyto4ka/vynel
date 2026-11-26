import { FormEvent, useEffect, useState } from 'react'
import { CertInfo, get, Inbound, Node, NodeInventoryView, NodeNetInterface, patch, post, Provider, put } from '../api'
import { CodeEditor, parseObject, pretty } from '../code'
import { NodeWarpPanel } from '../components/NodeWarpModal'
import { ago, bps, bytes, date, dateTime, fmtVersion, nodeStateInfo, uptime } from '../format'
import { t, Trans } from '../i18n'
import { useAdvanced } from '../mode'
import { ProviderIcon } from './Billing'
import { Badge, Bar, copyText, Empty, Field, Icon, Loading, Menu, MenuItem, Switch, Tabs, useAction, useConfirm, useEscape, useLoad, useToast } from '../ui'

export type NodeDetailTab = 'system' | 'interfaces' | 'warp' | 'settings' | 'certs' | 'inbounds' | 'base' | 'compiled'

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

export function certRoleLabel(role: string): string {
  if (role === 'panel') return t('nodes.cert_role_panel')
  if (role === 'sub') return t('nodes.cert_role_sub')
  if (role === 'node') return t('nodes.cert_role_node')
  return role
}

export function certStatusBadge(c: CertInfo): { color: string; label: string } {
  if (!c.ok) return { color: 'red', label: t('nodes.cert_missing') }
  if (c.placeholder) return { color: 'amber', label: t('nodes.panel_cert_placeholder') }
  if (c.daysLeft <= 7) return { color: 'red', label: t('nodes.cert_days_left', { n: Math.max(0, c.daysLeft) }) }
  if (c.daysLeft < 30) return { color: 'amber', label: t('nodes.cert_days_left', { n: c.daysLeft }) }
  return { color: 'green', label: t('nodes.cert_days_left', { n: c.daysLeft }) }
}

export function summarizeCerts(certs?: CertInfo[]): { color: string; label: string } | null {
  if (!certs || certs.length === 0) return null
  const bad = certs.find((c) => !c.ok || c.placeholder)
  if (bad) {
    return { color: bad.ok ? 'amber' : 'red', label: bad.ok ? t('nodes.cert_badge_pending') : t('nodes.cert_badge_missing') }
  }
  const minDays = Math.min(...certs.map((c) => c.daysLeft))
  const color = minDays <= 7 ? 'red' : minDays < 30 ? 'amber' : 'green'
  return { color, label: t('nodes.cert_badge_days', { n: Math.max(0, minDays) }) }
}

export function CertsList({ certs, canRenew, busyDomain, onRenew }: {
  certs: CertInfo[]
  canRenew: boolean
  busyDomain: string | null
  onRenew: (domain: string) => void
}) {
  if (certs.length === 0) {
    return <div className="muted small">{t('nodes.certs_empty')}</div>
  }
  return (
    <div className="cert-list">
      {certs.map((c) => {
        const st = certStatusBadge(c)
        const busy = busyDomain === c.domain || busyDomain === '*'
        return (
          <div key={c.domain} className="cert-row">
            <div className="grow" style={{ minWidth: 0 }}>
              <div className="row" style={{ gap: 6, flexWrap: 'wrap' }}>
                <b className="mono">{c.domain}</b>
                <Badge color={st.color} dot>{st.label}</Badge>
                {(c.roles || []).map((r) => <span key={r} className="chip static">{certRoleLabel(r)}</span>)}
              </div>
              <div className="muted small cert-meta">
                {c.ok && !c.placeholder && c.notAfter > 0 && (
                  <>
                    <Trans k="nodes.panel_cert_expires" v={{ v: new Date(c.notAfter * 1000).toLocaleDateString() }} />
                    {c.issuer ? ` · ${c.issuer}` : ''}
                  </>
                )}
                {c.ok && c.placeholder && t('nodes.cert_placeholder_hint')}
                {!c.ok && t('nodes.cert_missing_hint')}
              </div>
              {c.lastError && (
                <div className="small pink-text" style={{ marginTop: 4 }}>{c.lastError}</div>
              )}
            </div>
            {canRenew && (
              <button type="button" className="btn sm" disabled={busyDomain !== null} onClick={() => onRenew(c.domain)}>
                {busy ? <span className="spinner" /> : <Icon name="refresh" />} {t('nodes.cert_reissue')}
              </button>
            )}
          </div>
        )
      })}
    </div>
  )
}

function fmtOSAge(installedAt?: number): string {
  if (!installedAt || installedAt <= 0) return '—'
  const sec = Math.max(0, Math.floor(Date.now() / 1000 - installedAt))
  const days = Math.floor(sec / 86400)
  if (days >= 365) {
    const y = Math.floor(days / 365)
    const d = days % 365
    return t('nodes.inv_os_age_years_days', { y, d })
  }
  if (days >= 1) return t('nodes.inv_os_age_days', { n: days })
  return t('nodes.inv_os_age_hours', { n: Math.max(1, Math.floor(sec / 3600)) })
}

function fmtLinkSpeed(mbps?: number): string {
  if (!mbps || mbps <= 0) return ''
  if (mbps >= 1000) {
    const g = mbps / 1000
    return `${Number.isInteger(g) ? g : g.toFixed(1)} Gbps`
  }
  return `${mbps} Mbps`
}

export function NodeInspector({
  node,
  initialTab = 'system',
  actionsMenu,
  onBack,
  onAttach,
  onEditHost,
  onEditConfig,
  onReload,
}: {
  node: Node
  initialTab?: NodeDetailTab
  actionsMenu?: MenuItem[]
  onBack: () => void
  onAttach: () => void
  onEditHost: (inb: Inbound) => void
  onEditConfig: (inb: Inbound) => void
  onReload: () => void
}) {
  const engineer = useAdvanced()
  const act = useAction()
  const confirm = useConfirm()
  const toast = useToast()

  const normalizeTab = (want: NodeDetailTab): NodeDetailTab => {
    if (want === 'certs' && node.manual) return 'settings'
    if ((want === 'inbounds' || want === 'base' || want === 'compiled') && !engineer) return 'system'
    return want
  }

  const [tab, setTab] = useState<NodeDetailTab>(() => normalizeTab(initialTab))
  useEffect(() => {
    setTab(normalizeTab(initialTab))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialTab, node.id])

  useEffect(() => {
    if (!engineer && (tab === 'inbounds' || tab === 'base' || tab === 'compiled')) {
      setTab('system')
    }
  }, [engineer, tab])

  useEscape(true, onBack)

  const st = nodeStateInfo[node.state] || { label: node.state, color: '' }
  const online = node.state === 'in sync' || node.state === 'syncing'
  const m = node.metrics

  // Inventory (OS, hardware, disks, network interfaces)
  const inv = useLoad<NodeInventoryView>(
    () => get<NodeInventoryView>(`nodes/${node.id}/inventory`),
    [node.id],
    0,
    { cacheKey: `node-inv:${node.id}`, staleTimeMs: 15000 },
  )
  const [refreshingInv, setRefreshingInv] = useState(false)
  const refreshInventory = async () => {
    setRefreshingInv(true)
    const fresh = await act(() => get<NodeInventoryView>(`nodes/${node.id}/inventory?live=1`))
    setRefreshingInv(false)
    if (fresh) inv.setData(fresh)
    onReload()
  }

  const snap = inv.data?.snapshot
  const os = snap?.os
  const hw = snap?.hardware
  const disks = snap?.disks || []
  const rootDisk = disks.find((d) => d.mount === '/') || disks[0]
  const allIfaces = snap?.interfaces || []
  const hasVirtualIfaces = allIfaces.some((x) => x.kind !== 'physical')
  const [showVirtual, setShowVirtual] = useState(true)
  const visibleIfaces = showVirtual ? allIfaces : allIfaces.filter((x) => x.kind === 'physical' || x.primary)

  // Node settings form state
  const [name, setName] = useState(node.name)
  const [country, setCountry] = useState(node.country)
  const [domain, setDomain] = useState(node.domain)
  const [decoyPath, setDecoyPath] = useState(node.decoyPath || '')
  const [enabled, setEnabled] = useState(node.enabled)
  const [providerId, setProviderId] = useState(node.provider?.id || 0)
  const providers = useLoad(() => get<{ providers: Provider[] }>('billing'), [tab === 'settings'], 0, { cacheKey: 'billing', staleTimeMs: 20000, enabled: tab === 'settings' })

  useEffect(() => {
    setName(node.name)
    setCountry(node.country)
    setDomain(node.domain)
    setDecoyPath(node.decoyPath || '')
    setEnabled(node.enabled)
    setProviderId(node.provider?.id || 0)
  }, [node.id, node.name, node.country, node.domain, node.decoyPath, node.enabled, node.provider?.id])

  const saveSettings = async (e: FormEvent) => {
    e.preventDefault()
    const r = await act(async () => {
      await patch(`nodes/${node.id}`, {
        name,
        country,
        domain,
        enabled,
        ...(!node.manual ? { decoyPath } : {}),
      })
      if (providerId !== (node.provider?.id || 0)) {
        await patch(`nodes/${node.id}/billing`, { providerId })
      }
      return true
    }, t('common.saved'))
    if (r !== undefined) onReload()
  }

  // Certificates tab state
  const [busyDomain, setBusyDomain] = useState<string | null>(null)
  const [liveCerts, setLiveCerts] = useState<CertInfo[] | null>(null)
  useEffect(() => { setLiveCerts(null) }, [node.id])
  const certs = liveCerts ?? node.certs ?? []
  const canRenew = !node.manual && !!node.certsSupported && (node.local || online)

  const renewCerts = async (targetDomain: string) => {
    const label = targetDomain || certs.map((c) => c.domain).join(', ')
    if (!(await confirm(t('nodes.cert_reissue_confirm', { v: label }), t('nodes.cert_reissue'), false))) return
    setBusyDomain(targetDomain || '*')
    const r = await act(() => post<{ detail?: string; certs?: CertInfo[] }>(`nodes/${node.id}/certs/renew`, { domain: targetDomain }), t('nodes.cert_reissued'))
    setBusyDomain(null)
    if (r) {
      if (r.certs) setLiveCerts(r.certs)
      onReload()
    }
  }

  // Engineer tabs: per-node Xray base & compiled Xray + Nginx
  const [compiledView, setCompiledView] = useState<'xray' | 'nginx'>('xray')
  const [showSecrets, setShowSecrets] = useState(false)
  const base = useLoad<NodeXrayBaseResp>(async () => {
    try {
      return await get<NodeXrayBaseResp>(`nodes/${node.id}/xray-base`)
    } catch {
      const g = await get<{ json: string }>('xray/base')
      return { mode: 'inherit', globalJson: g.json || '{}' }
    }
  }, [node.id, tab === 'base'], 0, { enabled: tab === 'base' })

  const [baseMode, setBaseMode] = useState<'inherit' | 'custom'>('inherit')
  const [baseText, setBaseText] = useState('')
  const [baseDirty, setBaseDirty] = useState(false)

  useEffect(() => {
    if (!base.data) return
    setBaseMode(base.data.mode)
    setBaseText(base.data.mode === 'custom' && base.data.nodeJson ? base.data.nodeJson : base.data.globalJson)
    setBaseDirty(false)
  }, [base.data])

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
  }, [node.id, showSecrets, tab === 'compiled'], 0, { enabled: tab === 'compiled' })

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

  const memTotal = hw?.memTotal || m?.memTotal || 0
  const memUsed = m?.memUsed ?? hw?.memUsed ?? 0
  const uptimeSec = m?.uptime ?? os?.uptime ?? 0
  const ifaceCount = allIfaces.length || node.addresses.length || undefined

  return (
    <div>
      <div className="inspector-head">
        <div className="inspector-title">
          <button type="button" className="btn sm ghost" onClick={onBack}>← {t('common.nodes')}</button>
          <span className="flag">{node.flag || '🌐'}</span>
          <div style={{ minWidth: 0 }}>
            <div className="row" style={{ gap: 8 }}>
              <h2>{node.name}</h2>
              <span className="muted mono small">{node.code}</span>
              <Badge color={st.color} dot>{st.label}</Badge>
              {!node.enabled && <Badge color="amber">{t('nodes.node_turned_off')}</Badge>}
              {node.local && <Badge>{t('nodes.on_the_panel_server')}</Badge>}
              {node.manual && <Badge>{t('nodes.manual_node')}</Badge>}
              {node.warp?.enabled && (
                <Badge color="green" dot>
                  {node.warp.ruleMode === 'all' ? t('warp.badge_all') : t('warp.badge_rules', { count: node.warp.rulesCount })}
                </Badge>
              )}
            </div>
            <div className="muted small">
              {node.domain || t('nodes.no_domain')}
              {' · '}<Trans k="nodes.contact" v={{ v: ago(node.lastSeenAt) }} />
              {uptimeSec > 0 ? ` · ${t('nodes.uptime').toLowerCase()} ${uptime(uptimeSec)}` : ''}
            </div>
          </div>
        </div>
        <div className="row" style={{ gap: 8 }}>
          <button
            type="button"
            className="btn sm"
            disabled={refreshingInv}
            onClick={refreshInventory}
            title={snap ? (inv.data?.live ? t('nodes.inv_collected_live') : t('nodes.inv_collected_cached', { v: ago(snap.collectedAt) })) : undefined}
          >
            {refreshingInv ? <span className="spinner" /> : <Icon name="refresh" />} {t('nodes.inv_refresh')}
          </button>
          {actionsMenu && <Menu title={t('common.actions')} items={actionsMenu} />}
        </div>
      </div>

      {node.problems.length > 0 && (
        <div className="alert pink" style={{ marginBottom: 16 }}>
          <ul className="list-plain" style={{ color: 'inherit' }}>{node.problems.map((p, i) => <li key={i}>{p}</li>)}</ul>
        </div>
      )}

      <Tabs
        value={tab}
        onChange={(v) => setTab(v as NodeDetailTab)}
        items={[
          { id: 'system', label: t('nodes.tab_system') },
          { id: 'interfaces', label: t('nodes.tab_interfaces'), count: ifaceCount },
          { id: 'warp', label: t('nodes.tab_warp') },
          { id: 'settings', label: t('nodes.tab_settings') },
          ...(!node.manual ? [{ id: 'certs', label: t('nodes.certs_title'), count: certs.length }] : []),
          ...(engineer ? [
            { id: 'inbounds', label: t('nodes.tab_inbounds'), count: node.inbounds.length },
            { id: 'base', label: t('nodes.tab_xray_base') },
            { id: 'compiled', label: t('nodes.tab_compiled') },
          ] : []),
        ]}
      />

      {tab === 'system' && (
        <div className="cards">
          <div className="stats">
            <div className="card stat">
              <div className="k">{t('nodes.inv_cpu')}</div>
              <div className="v">{m ? `${m.cpu.toFixed(1)}%` : hw ? t('nodes.inv_cpu_cores', { n: hw.cpuCores }) : '—'}</div>
              {m && <div style={{ marginTop: 8 }}><Bar value={m.cpu} max={100} /></div>}
              <div className="s ellipsis" title={hw?.cpuModel || ''}>
                {hw ? `${t('nodes.inv_cpu_cores', { n: hw.cpuCores })} · ${hw.cpuModel}` : (m ? `load ${m.load1.toFixed(2)}` : '—')}
              </div>
            </div>

            <div className="card stat">
              <div className="k">{t('nodes.inv_ram')}</div>
              <div className="v">{memTotal ? `${Math.round((memUsed / memTotal) * 100)}%` : '—'}</div>
              {memTotal > 0 && <div style={{ marginTop: 8 }}><Bar value={memUsed} max={memTotal} /></div>}
              <div className="s">
                {memTotal ? `${bytes(memUsed)} / ${bytes(memTotal)}` : '—'}
                {hw && hw.swapTotal > 0 ? ` · Swap ${bytes(hw.swapUsed)}/${bytes(hw.swapTotal)}` : ''}
              </div>
            </div>

            <div className="card stat">
              <div className="k">{t('nodes.inv_disk_root', { mount: rootDisk?.mount || '/' })}</div>
              <div className="v">
                {rootDisk && rootDisk.totalBytes > 0 ? `${Math.round((rootDisk.usedBytes / rootDisk.totalBytes) * 100)}%` : '—'}
              </div>
              {rootDisk && rootDisk.totalBytes > 0 && (
                <div style={{ marginTop: 8 }}><Bar value={rootDisk.usedBytes} max={rootDisk.totalBytes} /></div>
              )}
              <div className="s">
                {rootDisk
                  ? `${bytes(rootDisk.usedBytes)} / ${bytes(rootDisk.totalBytes)} · ${t('nodes.inv_disk_free', { v: bytes(rootDisk.freeBytes) })}`
                  : '—'}
              </div>
            </div>

            <div className="card stat">
              <div className="k">{t('nodes.inv_os_age')}</div>
              <div className="v">{fmtOSAge(os?.installedAt)}</div>
              <div className="s ellipsis" title={os?.distro || ''}>
                {os?.installedAt ? `${t('nodes.inv_os_installed', { v: date(os.installedAt) })}` : (os?.distro || '—')}
              </div>
              {os?.distro && os?.installedAt ? (
                <div className="muted small ellipsis" style={{ marginTop: 2 }}>{os.distro}</div>
              ) : null}
            </div>
          </div>

          {inv.loading && !snap ? (
            <div className="card card-pad"><Loading error={inv.error} /></div>
          ) : !snap && inv.data && !inv.data.supported ? (
            <div className="alert amber">{t('nodes.inv_update_hint')}</div>
          ) : null}

          <div className="cols-2">
            <div className="card">
              <div className="card-head">
                <h3>{t('nodes.inv_os_card')}</h3>
                {snap && (
                  <span className="muted small">
                    {inv.data?.live ? t('nodes.inv_collected_live') : t('nodes.inv_collected_cached', { v: ago(snap.collectedAt) })}
                  </span>
                )}
              </div>
              <div className="card-pad">
                <div className="kv inv-kv">
                  <div className="k">{t('nodes.inv_os_distro')}</div>
                  <div><b>{os?.distro || '—'}</b></div>

                  <div className="k">{t('nodes.inv_os_kernel')}</div>
                  <div className="mono">{os?.kernel || '—'}</div>

                  <div className="k">{t('nodes.inv_os_arch')}</div>
                  <div className="mono">{os?.arch || '—'}</div>

                  <div className="k">{t('nodes.inv_os_age')}</div>
                  <div className="num">
                    {os?.installedAt ? (
                      <>
                        <b>{fmtOSAge(os.installedAt)}</b>
                        <span className="muted small"> · {t('nodes.inv_os_installed', { v: date(os.installedAt) })}</span>
                      </>
                    ) : '—'}
                  </div>

                  <div className="k">{t('nodes.uptime')}</div>
                  <div className="num">
                    {uptimeSec > 0 ? uptime(uptimeSec) : '—'}
                    {os?.bootedAt ? <span className="muted small"> · {t('nodes.inv_os_booted', { v: dateTime(os.bootedAt) })}</span> : null}
                  </div>

                  <div className="k">{t('nodes.inv_os_virt')}</div>
                  <div>{os?.virtualization ? <Badge>{os.virtualization}</Badge> : '—'}</div>

                  <div className="k">{t('nodes.inv_os_hostname')}</div>
                  <div className="mono">{os?.hostname || '—'}</div>

                  <div className="k">{t('nodes.inv_os_tz_ntp')}</div>
                  <div className="row" style={{ gap: 8 }}>
                    {os?.timezone && <span className="mono small">{os.timezone}</span>}
                    {os?.ntpSync !== undefined && (
                      <Badge color={os.ntpSync ? 'green' : 'amber'} dot>
                        {os.ntpSync ? t('nodes.inv_ntp_ok') : t('nodes.inv_ntp_no')}
                      </Badge>
                    )}
                    {!os?.timezone && os?.ntpSync === undefined && '—'}
                  </div>

                  <div className="k">{t('nodes.inv_sysctl')}</div>
                  <div className="mono small">
                    {os?.congestionControl || os?.qdisc ? (
                      <>
                        {os.congestionControl ? `cc=${os.congestionControl}` : ''}
                        {os.qdisc ? ` · qdisc=${os.qdisc}` : ''}
                        {os.tcpFastopen ? ` · tfo=${os.tcpFastopen}` : ''}
                      </>
                    ) : '—'}
                  </div>
                </div>
              </div>
            </div>

            <div className="card">
              <div className="card-head">
                <h3>{t('nodes.inv_hw_card')}</h3>
                {m && <span className="muted small num">↓ {bps(m.rxBps)} · ↑ {bps(m.txBps)}</span>}
              </div>
              <div className="card-pad">
                <div className="kv inv-kv">
                  <div className="k">{t('nodes.inv_cpu')}</div>
                  <div>
                    <div><b>{hw?.cpuModel || '—'}</b></div>
                    {hw && (
                      <div className="muted small num">
                        {t('nodes.inv_cpu_cores', { n: hw.cpuCores })}
                        {hw.cpuPhysical ? ` (${t('nodes.inv_cpu_phys', { n: hw.cpuPhysical })})` : ''}
                        {hw.cpuMhz ? ` · ${Math.round(hw.cpuMhz)} MHz` : ''}
                        {hw.cpuCache ? ` · L3 ${hw.cpuCache}` : ''}
                      </div>
                    )}
                  </div>

                  <div className="k">{t('nodes.inv_cpu_aes')}</div>
                  <div>
                    {hw ? (
                      <Badge color={hw.aesHardware ? 'green' : undefined}>
                        {hw.aesHardware ? t('nodes.inv_aes_yes') : t('nodes.inv_aes_no')}
                      </Badge>
                    ) : '—'}
                  </div>

                  <div className="k">{t('nodes.inv_load')}</div>
                  <div className="mono num">
                    {hw ? `${hw.load1.toFixed(2)} · ${hw.load5.toFixed(2)} · ${hw.load15.toFixed(2)}` : (m ? m.load1.toFixed(2) : '—')}
                  </div>

                  <div className="k">{t('nodes.inv_ram')}</div>
                  <div className="num">
                    {memTotal ? `${bytes(memUsed)} / ${bytes(memTotal)}` : '—'}
                  </div>

                  <div className="k">{t('nodes.inv_swap')}</div>
                  <div className="num">
                    {hw ? (hw.swapTotal > 0 ? `${bytes(hw.swapUsed)} / ${bytes(hw.swapTotal)}` : <span className="muted">{t('nodes.inv_swap_off')}</span>) : '—'}
                  </div>

                  {(hw?.vendor || hw?.product || node.provider) && (
                    <>
                      <div className="k">{t('nodes.inv_platform')}</div>
                      <div className="row" style={{ gap: 8 }}>
                        {(hw?.vendor || hw?.product) && (
                          <span>{[hw?.vendor, hw?.product].filter(Boolean).join(' · ')}</span>
                        )}
                        {node.provider && (
                          <a
                            className="provider-chip small"
                            href={node.provider.loginUrl || node.provider.url || '#/billing'}
                            target={node.provider.loginUrl || node.provider.url ? '_blank' : undefined}
                            rel="noreferrer"
                          >
                            <ProviderIcon p={node.provider} size={15} />
                            {node.provider.name}
                          </a>
                        )}
                      </div>
                    </>
                  )}

                  <div className="k">{t('nodes.inv_services')}</div>
                  <div className="mono small">
                    vynel {fmtVersion(node.agentVersion)} · Xray {node.xrayVersion || '—'}
                    {!node.manual && node.nginxVersion ? ` · nginx ${node.nginxVersion}` : !node.manual && node.caddyVersion ? ` · Caddy ${node.caddyVersion}` : ''}
                  </div>

                  <div className="k">{t('nodes.today')}</div>
                  <div className="num">
                    {bytes(node.todayBytes)}
                    {m ? <span className="muted small"> · {t('common.online').toLowerCase()}: {m.online}</span> : null}
                  </div>
                </div>
              </div>
            </div>
          </div>

          {disks.length > 0 && (
            <div className="card">
              <div className="card-head">
                <h3>{t('nodes.inv_disks_card')}</h3>
              </div>
              <div className="table-wrap">
                <table className="table">
                  <thead>
                    <tr>
                      <th>Mount</th>
                      <th>Device / FS</th>
                      <th style={{ minWidth: 180 }}>{t('common.traffic')} / {t('nodes.inv_disks_card')}</th>
                      <th style={{ textAlign: 'right' }}>{t('nodes.inv_disk_free', { v: '' }).trim()}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {disks.map((d) => {
                      const pct = d.totalBytes ? Math.round((d.usedBytes / d.totalBytes) * 100) : 0
                      return (
                        <tr key={d.mount}>
                          <td className="mono">
                            <b>{d.mount}</b>
                            {d.kind && <> <Badge>{d.kind}</Badge></>}
                          </td>
                          <td className="mono small text-2">
                            {d.device || '—'}{d.fsType ? ` · ${d.fsType}` : ''}
                          </td>
                          <td>
                            <div className="row num" style={{ gap: 10, flexWrap: 'nowrap' }}>
                              <div style={{ flex: 1, minWidth: 90 }}><Bar value={d.usedBytes} max={d.totalBytes} /></div>
                              <span className="small text-2 nowrap">{bytes(d.usedBytes)} / {bytes(d.totalBytes)} ({pct}%)</span>
                            </div>
                          </td>
                          <td className="num nowrap" style={{ textAlign: 'right' }}>
                            {bytes(d.freeBytes)}
                          </td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </div>
            </div>
          )}
        </div>
      )}

      {tab === 'interfaces' && (
        <div className="cards">
          {allIfaces.length > 0 ? (
            <div className="card">
              <div className="card-head">
                <div>
                  <h3>{t('nodes.ifaces_title')}</h3>
                  <div className="card-sub">{t('nodes.ifaces_sub')}</div>
                </div>
                {hasVirtualIfaces && (
                  <label className="check small">
                    <input type="checkbox" checked={showVirtual} onChange={(e) => setShowVirtual(e.target.checked)} />
                    <span>{t('nodes.ifaces_show_virtual')}</span>
                  </label>
                )}
              </div>
              <div className="table-wrap">
                <table className="table">
                  <thead>
                    <tr>
                      <th>{t('nodes.ifaces_col_name')}</th>
                      <th>{t('nodes.ifaces_col_addrs')}</th>
                      <th>{t('nodes.ifaces_col_link')}</th>
                      <th style={{ textAlign: 'right' }}>{t('nodes.ifaces_col_traffic')}</th>
                      <th style={{ textAlign: 'right' }}>{t('nodes.ifaces_col_errors')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {visibleIfaces.map((ifc: NodeNetInterface) => {
                      const errs = ifc.rxErrors + ifc.txErrors
                      const drops = ifc.rxDrops + ifc.txDrops
                      const speed = fmtLinkSpeed(ifc.speedMbps)
                      return (
                        <tr key={ifc.name}>
                          <td className="nowrap">
                            <div className="row" style={{ gap: 6 }}>
                              <b className="mono">{ifc.name}</b>
                              <Badge color={ifc.up ? 'green' : undefined} dot>{ifc.up ? 'UP' : 'DOWN'}</Badge>
                              {ifc.primary && <Badge color="green">{t('nodes.iface_primary')}</Badge>}
                              {ifc.kind === 'virtual' && <Badge>{t('nodes.iface_kind_virtual')}</Badge>}
                              {ifc.kind === 'loopback' && <Badge>{t('nodes.iface_kind_loopback')}</Badge>}
                            </div>
                          </td>
                          <td>
                            {ifc.addresses.length === 0 ? (
                              <span className="muted small">—</span>
                            ) : (
                              <div className="row" style={{ gap: 6 }}>
                                {ifc.addresses.map((addr) => {
                                  const plainIp = addr.split('/')[0]
                                  return (
                                    <button
                                      key={addr}
                                      type="button"
                                      className="chip static mono iface-addr-btn"
                                      title={t('ui.copy')}
                                      onClick={() => copyText(plainIp).then(() => toast(t('ui.copied')))}
                                    >
                                      {addr}
                                    </button>
                                  )
                                })}
                              </div>
                            )}
                            {ifc.defaultGateway && (
                              <div className="muted small mono" style={{ marginTop: 4 }}>
                                {t('nodes.iface_gateway', { v: ifc.defaultGateway })}
                              </div>
                            )}
                          </td>
                          <td className="small">
                            <div className="num">
                              MTU {ifc.mtu}
                              {speed ? ` · ${speed}` : ''}
                            </div>
                            {ifc.mac && <div className="muted mono">{ifc.mac}</div>}
                          </td>
                          <td className="num nowrap" style={{ textAlign: 'right' }}>
                            <div>↓ {bytes(ifc.rxBytes)} · ↑ {bytes(ifc.txBytes)}</div>
                            <div className="muted small">
                              {t('nodes.iface_packets', { rx: ifc.rxPackets.toLocaleString(), tx: ifc.txPackets.toLocaleString() })}
                            </div>
                          </td>
                          <td className="num nowrap" style={{ textAlign: 'right' }}>
                            {errs + drops > 0 ? (
                              <Badge color="amber">err {errs} · drop {drops}</Badge>
                            ) : (
                              <span className="muted">0</span>
                            )}
                          </td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </div>
            </div>
          ) : inv.loading ? (
            <div className="card card-pad"><Loading error={inv.error} /></div>
          ) : !inv.data?.supported ? (
            <div className="alert amber">{t('nodes.inv_update_hint')}</div>
          ) : null}

          <div className="card">
            <div className="card-head">
              <div>
                <h3>{t('nodes.panel_addrs_title')}</h3>
                <div className="card-sub">{t('nodes.panel_addrs_sub')}</div>
              </div>
            </div>
            <div className="card-pad">
              {node.addresses.length === 0 ? (
                <div className="muted small">{t('settings.the_node_has_not_reported')}</div>
              ) : (
                <div className="row" style={{ gap: 8 }}>
                  {node.addresses.map((a) => (
                    <button
                      key={a.id}
                      type="button"
                      className="chip static mono iface-addr-btn"
                      title={t('ui.copy')}
                      onClick={() => copyText(a.ip).then(() => toast(t('ui.copied')))}
                    >
                      <span>{a.ip}</span>
                      {a.interface && <span className="muted">· {a.interface}</span>}
                      {a.primary && <span className="miku-text">★</span>}
                      {!a.onInterface && <span className="amber-text">{t('nodes.not_on_the_interface')}</span>}
                    </button>
                  ))}
                </div>
              )}
            </div>
          </div>
        </div>
      )}

      {tab === 'warp' && (
        <div className="card card-pad">
          <NodeWarpPanel node={node} onSaved={onReload} />
        </div>
      )}

      {tab === 'settings' && (
        <div className="card card-pad">
          <form onSubmit={saveSettings}>
            <div className="row" style={{ justifyContent: 'space-between', padding: '12px 14px', marginBottom: 16, borderRadius: 'var(--shape-md)', background: 'var(--container-low)', border: '1px solid var(--outline-variant)' }}>
              <label className="row" style={{ gap: 10, cursor: 'pointer', fontWeight: 500 }}>
                <Switch checked={enabled} onChange={setEnabled} />
                <span>{t('nodes.settings_enabled_label')}</span>
              </label>
              <Badge color={enabled ? 'green' : 'amber'}>{enabled ? t('nodes.node_turned_on') : t('nodes.node_turned_off')}</Badge>
            </div>

            <div className="grid2">
              <Field label={t('common.name')}>
                <input className="input" value={name} onChange={(e) => setName(e.target.value)} />
              </Field>
              <Field label={t('nodes.country_code')}>
                <input className="input" maxLength={2} value={country} onChange={(e) => setCountry(e.target.value.toUpperCase())} />
              </Field>
            </div>

            <Field label={t('nodes.domain')} info={t('nodes.changing_the_domain_changes')}>
              <input className="input" value={domain} onChange={(e) => setDomain(e.target.value)} />
            </Field>

            {!node.manual && (
              <Field
                label={t('nodes.decoy_path')}
                info={node.local ? t('nodes.decoy_path_local_info') : t('nodes.decoy_path_info')}
                help={t('nodes.decoy_path_help')}
              >
                <input className="input mono" value={decoyPath} onChange={(e) => setDecoyPath(e.target.value)} placeholder="/var/www/site" />
              </Field>
            )}

            <Field
              label={t('common.provider')}
              help={<><Trans k="nodes.price_payment_and_traffic_limit" v={{ v: <a href="#/billing">{t('nodes.billing')}</a> }} /></>}
            >
              <select className="input" value={providerId} onChange={(e) => setProviderId(Number(e.target.value))}>
                <option value={0}>{t('common.not_set')}</option>
                {(providers.data?.providers || []).map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
              </select>
            </Field>

            <div className="row" style={{ justifyContent: 'flex-end', paddingTop: 4 }}>
              <button type="submit" className="btn primary">
                <Icon name="check" /> {t('common.save')}
              </button>
            </div>
          </form>
        </div>
      )}

      {tab === 'certs' && !node.manual && (
        <div className="card">
          <div className="card-head">
            <div>
              <h3>{t('nodes.certs_title')}</h3>
              {certs.length > 0 && <div className="card-sub">{t('nodes.certs_le_rate_hint')}</div>}
            </div>
            {canRenew && certs.length > 1 && (
              <button type="button" className="btn sm" disabled={busyDomain !== null} onClick={() => renewCerts('')}>
                {busyDomain === '*' ? <span className="spinner" /> : <Icon name="refresh" />} {t('nodes.cert_reissue_all')}
              </button>
            )}
          </div>
          <div className="card-pad">
            {!node.certsSupported && (
              <div className="alert amber" style={{ marginBottom: 14 }}>{t('nodes.certs_update_node_hint')}</div>
            )}
            {node.certsSupported && !node.local && !online && (
              <div className="alert amber" style={{ marginBottom: 14 }}>{t('nodes.certs_offline_hint')}</div>
            )}
            <CertsList certs={certs} canRenew={canRenew} busyDomain={busyDomain} onRenew={renewCerts} />
          </div>
        </div>
      )}

      {tab === 'inbounds' && (
        <div className="card">
          <div className="card-head">
            <div>
              <h3>{t('nodes.tab_inbounds')}</h3>
              <div className="card-sub">{node.domain || node.addresses?.[0]?.ip || ''}</div>
            </div>
            <button className="btn sm primary" onClick={onAttach}><Icon name="plus" /> {t('common.profile')}</button>
          </div>
          {node.inbounds.length === 0 ? (
            <Empty>{t('nodes.without_profiles')}</Empty>
          ) : (
            <div className="table-wrap">
              <table className="table">
                <thead>
                  <tr>
                    <th>{t('common.tag')}</th>
                    <th>{t('common.profiles')}</th>
                    <th>{t('nodes.port')} / SNI</th>
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
    </div>
  )
}
