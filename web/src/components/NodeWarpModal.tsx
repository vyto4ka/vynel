import { FormEvent, useEffect, useState } from 'react'
import { get, Node, NodeWarpView, post, put, WarpCheckResult, WarpTransport } from '../api'
import { Badge, Field, Icon, Loading, Modal, Switch, useAction, useConfirm, useToast } from '../ui'
import { t } from '../i18n'
import { RuleListInput } from './RuleListInput'

interface Props {
  node: Node
  onClose: () => void
  onSaved: () => void
}

const PRESETS: { id: string; labelKey: string; rules: string[] }[] = [
  { id: 'ai', labelKey: 'warp.preset_ai', rules: ['geosite:openai', 'geosite:anthropic', 'geosite:google-gemini'] },
  { id: 'google', labelKey: 'warp.preset_google', rules: ['geosite:youtube', 'geosite:google'] },
  { id: 'meta', labelKey: 'warp.preset_meta', rules: ['geosite:meta', 'geosite:instagram'] },
  { id: 'stream', labelKey: 'warp.preset_stream', rules: ['geosite:netflix', 'geosite:spotify', 'geosite:reddit', 'geosite:discord'] },
]

const MASQUE_ENDPOINTS = [
  '162.159.198.2:443',
  '162.159.198.1:443',
  '162.159.198.2:8443',
  '162.159.198.2:4500',
]

const WG_ENDPOINTS = [
  'engage.cloudflareclient.com:2408',
  'engage.cloudflareclient.com:4500',
  'engage.cloudflareclient.com:1701',
  'engage.cloudflareclient.com:500',
  '162.159.192.1:2408',
  '162.159.193.1:2408',
]

function transportShortLabel(tr: WarpTransport): string {
  if (tr === 'masque-h2') return 'MASQUE H2'
  if (tr === 'wireguard') return 'WireGuard'
  return 'MASQUE H3'
}

export function NodeWarpModal({ node, onClose, onSaved }: Props) {
  const act = useAction()
  const confirm = useConfirm()
  const toast = useToast()

  const [loading, setLoading] = useState(true)
  const [data, setData] = useState<NodeWarpView | null>(null)
  const [enabled, setEnabled] = useState(false)
  const [transport, setTransport] = useState<WarpTransport>('masque-h3')
  const [ruleMode, setRuleMode] = useState<'rules' | 'all'>('rules')
  const [rulesText, setRulesText] = useState('')
  const [licenseKey, setLicenseKey] = useState('')
  const [endpoint, setEndpoint] = useState(MASQUE_ENDPOINTS[0])
  const [domainStrategy, setDomainStrategy] = useState('ForceIPv4v6')
  const [mtu, setMtu] = useState(1280)

  const [saving, setSaving] = useState(false)
  const [checking, setChecking] = useState(false)
  const [showImport, setShowImport] = useState(false)
  const [wgConf, setWgConf] = useState('')

  const syncFromView = (v: NodeWarpView) => {
    const tr: WarpTransport = v.transport === 'masque-h2' || v.transport === 'wireguard' ? v.transport : 'masque-h3'
    setData(v)
    setEnabled(v.enabled)
    setTransport(tr)
    setRuleMode(v.ruleMode === 'all' ? 'all' : 'rules')
    setRulesText((v.rules || []).join('\n'))
    setEndpoint(v.endpoint || (tr === 'wireguard' ? WG_ENDPOINTS[0] : MASQUE_ENDPOINTS[0]))
    setDomainStrategy(v.domainStrategy || 'ForceIPv4v6')
    setMtu(v.mtu || 1280)
  }

  const handleTransportChange = (next: WarpTransport) => {
    setTransport(next)
    const ep = endpoint.trim().toLowerCase()
    if (next === 'wireguard') {
      if (!ep || ep.startsWith('162.159.198.') || ep.startsWith('consumer-masque.')) {
        setEndpoint(WG_ENDPOINTS[0])
      }
    } else if (!ep || ep.startsWith('engage.cloudflareclient.com') || ep.startsWith('162.159.192.') || ep.startsWith('162.159.193.')) {
      setEndpoint(MASQUE_ENDPOINTS[0])
    }
  }

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    get<NodeWarpView>(`nodes/${node.id}/warp`)
      .then((v) => { if (!cancelled) syncFromView(v) })
      .catch((err) => { if (!cancelled) toast(String(err), true) })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [node.id, toast])

  const appendPreset = (presetRules: string[]) => {
    const cur = rulesText.split('\n').map((s) => s.trim()).filter(Boolean)
    const set = new Set(cur.map((s) => s.toLowerCase()))
    const next = [...cur]
    for (const r of presetRules) {
      if (!set.has(r.toLowerCase())) {
        next.push(r)
        set.add(r.toLowerCase())
      }
    }
    setRuleMode('rules')
    setRulesText(next.join('\n'))
  }

  const save = async (e: FormEvent) => {
    e.preventDefault()
    setSaving(true)
    const rules = rulesText.split('\n').map((s) => s.trim()).filter(Boolean)
    const res = await act(() => put<NodeWarpView>(`nodes/${node.id}/warp`, {
      enabled,
      transport,
      ruleMode,
      rules,
      licenseKey: licenseKey.trim(),
      endpoint: endpoint.trim(),
      domainStrategy,
      mtu: Number(mtu) || 1280,
    }), t('warp.saved'))
    setSaving(false)
    if (res) {
      syncFromView(res)
      setLicenseKey('')
      onSaved()
    }
  }

  const runCheck = async () => {
    setChecking(true)
    const res = await act(() => post<WarpCheckResult>(`nodes/${node.id}/warp/check`))
    setChecking(false)
    if (res) {
      if (res.ok) {
        toast(t('warp.checked_ok', { ip: res.ip || '', colo: res.colo || 'CF', ms: res.rttMs ?? 0 }))
      } else {
        toast(t('warp.checked_fail', { err: res.error || 'unknown' }), true)
      }
      const fresh = await get<NodeWarpView>(`nodes/${node.id}/warp`).catch(() => null)
      if (fresh) syncFromView(fresh)
      onSaved()
    }
  }

  const reissue = async () => {
    if (!(await confirm(t('warp.reissue_confirm'), t('warp.reissue_btn')))) return
    const reqTransport = transport === 'wireguard' && data && !data.lastCheckOk ? '' : transport
    const res = await act(() => post<NodeWarpView>(`nodes/${node.id}/warp/provision`, {
      forceNew: true,
      transport: reqTransport,
      licenseKey: licenseKey.trim(),
    }), t('warp.provisioned'))
    if (res) {
      syncFromView(res)
      onSaved()
    }
  }

  const importConf = async () => {
    if (!wgConf.trim()) return
    const res = await act(() => post<NodeWarpView>(`nodes/${node.id}/warp/provision`, {
      wireguardConf: wgConf,
    }), t('warp.provisioned'))
    if (res) {
      syncFromView(res)
      setShowImport(false)
      setWgConf('')
      onSaved()
    }
  }

  const endpointOptions = transport === 'wireguard' ? WG_ENDPOINTS : MASQUE_ENDPOINTS

  return (
    <Modal
      title={t('warp.modal_title', { name: `${node.flag ? node.flag + ' ' : ''}${node.name}` })}
      onClose={onClose}
      wide
      footer={
        <>
          <button type="button" className="btn" onClick={onClose}>{t('common.cancel')}</button>
          <button type="submit" form="node-warp-form" className="btn primary" disabled={saving || loading}>
            <Icon name="check" /> {t('common.save')}
          </button>
        </>
      }
    >
      {loading || !data ? <Loading /> : (
        <form id="node-warp-form" onSubmit={save} style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          {/* Enable bar + status + check */}
          <div style={{ padding: 12, borderRadius: 10, border: '1px solid var(--border)', display: 'flex', flexDirection: 'column', gap: 10 }}>
            <div className="row" style={{ justifyContent: 'space-between', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
              <label className="row" style={{ gap: 10, alignItems: 'center', cursor: 'pointer', fontWeight: 500 }}>
                <Switch checked={enabled} onChange={setEnabled} />
                <span>{t('warp.enable_label')}</span>
              </label>
              <div className="row" style={{ gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
                <Badge>{transportShortLabel(transport)}</Badge>
                {data.hasCredentials ? (
                  <Badge color={data.lastCheckOk ? 'green' : undefined} dot>
                    {data.lastCheckOk && data.lastCheckColo
                      ? `${data.lastCheckIp} · ${data.lastCheckColo} · ${data.lastCheckMs} ms`
                      : t('warp.status_ready', { type: data.accountType || 'free' })}
                  </Badge>
                ) : (
                  <span className="muted small">{t('warp.status_auto_on_save')}</span>
                )}
                {data.hasCredentials && (
                  <button
                    type="button"
                    className="btn sm"
                    disabled={checking || !data.connected || !data.canWarp}
                    onClick={runCheck}
                  >
                    <Icon name="refresh" /> {checking ? t('warp.checking') : t('warp.check_btn')}
                  </button>
                )}
              </div>
            </div>
            <div className="muted small">{t('warp.enable_hint')}</div>
            {data.lastCheckAt > 0 && !data.lastCheckOk && data.lastCheckErr && (
              <div className="small" style={{ color: 'var(--red)' }}>{data.lastCheckErr}</div>
            )}
            {data.connected && !data.canWarp && (
              <div className="small" style={{ color: 'var(--amber)' }}>{t('warp.agent_old_warp')}</div>
            )}
            {data.connected && !data.canGeo && (
              <div className="small" style={{ color: 'var(--amber)' }}>{t('warp.agent_old_geo')}</div>
            )}
          </div>

          {/* Rule mode */}
          <div className="row" style={{ gap: 8 }}>
            <button
              type="button"
              className={ruleMode === 'rules' ? 'btn sm primary' : 'btn sm'}
              onClick={() => setRuleMode('rules')}
            >
              {t('warp.mode_rules')}
            </button>
            <button
              type="button"
              className={ruleMode === 'all' ? 'btn sm primary' : 'btn sm'}
              onClick={() => setRuleMode('all')}
            >
              {t('warp.mode_all')}
            </button>
          </div>

          {ruleMode === 'rules' && (
            <>
              <div>
                <div className="label" style={{ marginBottom: 6 }}>{t('warp.presets')}</div>
                <div className="row" style={{ gap: 6, flexWrap: 'wrap' }}>
                  {PRESETS.map((p) => (
                    <button key={p.id} type="button" className="btn sm" onClick={() => appendPreset(p.rules)}>
                      <Icon name="plus" size={13} /> {t(p.labelKey as any)}
                    </button>
                  ))}
                </div>
              </div>

              <Field label={t('warp.rules_label')} help={t('warp.rules_hint')}>
                <RuleListInput value={rulesText} onChange={setRulesText} rows={7} />
              </Field>
            </>
          )}

          {/* Advanced tunnel settings */}
          <details style={{ border: '1px solid var(--border)', borderRadius: 10, padding: '10px 12px' }}>
            <summary style={{ cursor: 'pointer', fontWeight: 500 }}>{t('warp.advanced_title')}</summary>
            <div style={{ marginTop: 12, display: 'flex', flexDirection: 'column', gap: 10 }}>
              <Field label={t('warp.transport_label')}>
                <select value={transport} onChange={(e) => handleTransportChange(e.target.value as WarpTransport)}>
                  <option value="masque-h3">{t('warp.transport_masque_h3')}</option>
                  <option value="masque-h2">{t('warp.transport_masque_h2')}</option>
                  <option value="wireguard">{t('warp.transport_wireguard')}</option>
                </select>
              </Field>
              <div className="grid2">
                <Field label={t('warp.endpoint')}>
                  <input value={endpoint} onChange={(e) => setEndpoint(e.target.value)} list="warp-endpoints" className="mono" />
                  <datalist id="warp-endpoints">
                    {endpointOptions.map((ep) => <option key={ep} value={ep} />)}
                  </datalist>
                </Field>
                <Field label={t('warp.domain_strategy')}>
                  <select value={domainStrategy} onChange={(e) => setDomainStrategy(e.target.value)}>
                    <option value="ForceIPv4v6">ForceIPv4v6 (IPv4 + IPv6)</option>
                    <option value="ForceIPv4">ForceIPv4</option>
                    <option value="ForceIPv6">ForceIPv6</option>
                    <option value="ForceIPv6v4">ForceIPv6v4</option>
                  </select>
                </Field>
              </div>
              <div className="grid2">
                <Field label={t('warp.license_key')}>
                  <input value={licenseKey} onChange={(e) => setLicenseKey(e.target.value)} placeholder={t('warp.license_placeholder')} className="mono" />
                </Field>
                <Field label={t('warp.mtu')}>
                  <input type="number" min={576} max={9000} value={mtu} onChange={(e) => setMtu(Number(e.target.value))} />
                </Field>
              </div>
              {data.hasCredentials && (
                <div className="muted small mono">
                  {data.addrV4}{data.addrV6 ? ` · ${data.addrV6}` : ''}{data.reserved?.length === 3 ? ` · reserved [${data.reserved.join(', ')}]` : ''}
                </div>
              )}
              <div className="row" style={{ gap: 8, flexWrap: 'wrap' }}>
                <button type="button" className="btn sm" onClick={reissue}>
                  <Icon name="refresh" /> {t('warp.reissue_btn')}
                </button>
                <button type="button" className="btn sm" onClick={() => setShowImport((v) => !v)}>
                  <Icon name="upload" /> {t('warp.import_conf_btn')}
                </button>
              </div>

              {showImport && (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                  <textarea
                    className="mono"
                    rows={6}
                    value={wgConf}
                    onChange={(e) => setWgConf(e.target.value)}
                    placeholder={t('warp.import_conf_placeholder')}
                  />
                  <div className="row" style={{ justifyContent: 'flex-end', gap: 8 }}>
                    <button type="button" className="btn sm" onClick={() => setShowImport(false)}>{t('common.cancel')}</button>
                    <button type="button" className="btn sm primary" onClick={importConf} disabled={!wgConf.trim()}>
                      {t('warp.import_conf_apply')}
                    </button>
                  </div>
                </div>
              )}
            </div>
          </details>
        </form>
      )}
    </Modal>
  )
}
