import { useEffect, useRef, useState } from 'react'
import { get, Network, NetRole, post, put, Setting, UpdateInfo } from '../api'
import { getCacheEntry, setCacheEntry } from '../cache'
import { fmtVersion } from '../format'
import { useEngineer } from '../mode'
import { LogTarget, UpdateChip, UpdateLog, useCheckUpdates, useRunUpdate, useUpdates } from '../updates'
import { Badge, Field, Icon, Info, Loading, Modal, Switch, useAction, useConfirm, useLoad, useToast } from '../ui'
import { t, Trans } from '../i18n'
import { applyTheme, loadTheme, themes } from '../theme'

const REPO_URL = 'https://github.com/vyto4ka/vynel'

export function Settings() {
  const settings = useLoad(() => get<Setting[]>('settings'), [], 0, { cacheKey: 'settings', staleTimeMs: 30000 })
  const network = useLoad(() => get<Network>('network'), [], 0, { cacheKey: 'network', staleTimeMs: 5 * 60 * 1000 })
  if (!settings.data) return <Loading error={settings.error} />
  const data = settings.data
  const sections = [...new Set(data.map((s) => s.section))]
  const leftSections = sections.filter((_, i) => i % 2 === 0)
  const rightSections = sections.filter((_, i) => i % 2 === 1)
  const hasLocalNode = !!network.data?.nodes?.some((n) => n.local)
  const renderSection = (sec: string) => (
    <div key={sec} className="card">
      <div className="card-head"><h3>{sec}</h3></div>
      <div className="card-pad">
        {data.filter((s) => s.section === sec).map((s) => <SettingRow key={s.key + (s.value ?? '')} s={s} hasLocalNode={hasLocalNode} onSaved={settings.reload} />)}
      </div>
    </div>
  )
  return (
    <div className="settings-grid">
      <div className="settings-col">
        <ThemeCard />
        <Stealth settings={data} />
        {leftSections.map(renderSection)}
      </div>
      <div className="settings-col">
        <NetworkCard data={network.data} error={network.error} reload={network.reload} />
        {rightSections.map(renderSection)}
      </div>
      <div className="settings-full">
        <AboutCard />
      </div>
    </div>
  )
}

const value = (all: Setting[], key: string) => {
  const s = all.find((x) => x.key === key)
  return s ? (s.set ? s.value ?? '' : s.default ?? '') : ''
}

// ThemeCard — the panel palette. Меняется сразу и без перезагрузки; сохраняется в браузере,
// как режим интерфейса и язык. Catppuccin — https://github.com/catppuccin/catppuccin (MIT).
function ThemeCard() {
  const [theme, setTheme] = useState(loadTheme)
  return (
    <div className="card">
      <div className="card-head">
        <div><h3>{t('settings.theme')}</h3><div className="card-sub">{t('settings.theme_sub')}</div></div>
      </div>
      <div className="card-pad">
        <div className="seg" role="group" aria-label={t('settings.theme')}>
          {themes.map((x) => (
            <button key={x.id} className={theme === x.id ? 'on' : ''} onClick={() => { setTheme(x.id); applyTheme(x.id) }}>{x.name}</button>
          ))}
        </div>
      </div>
    </div>
  )
}

// Сводка скрытия (docs/STEALTH.md §1) и быстрый переезд панели на случайный порт и путь.
function Stealth({ settings }: { settings: Setting[] }) {
  const act = useAction()
  const confirm = useConfirm()
  const webPort = value(settings, 'web.port')
  const subPort = value(settings, 'sub.port') || '443'
  const ownPort = webPort !== '' && webPort !== subPort
  const pageOn = value(settings, 'sub.page_enabled') !== 'false'
  const pwOn = value(settings, 'web.password_login') !== 'false'
  const items: [boolean, string, string][] = [
    [true, t('settings.secret_path'), t('settings.everything_outside_the_path')],
    [ownPort, t('settings.own_panel_port'), ownPort ? `:${webPort}` : t('settings.on_the_shared', { subPort })],
    [!pageOn, t('settings.subscription_page_hidden'), pageOn ? t('settings.a_browser_sees_the_subscription') : t('settings.a_browser_sees_the_decoy')],
    [!pwOn, t('settings.sign_in_only_through_telegram'), pwOn ? t('settings.login_form_is_on') : t('settings.login_form_is_off')],
  ]
  const run = async () => {
    if (!(await confirm(t('settings.the_panel_will_move_to'), t('settings.move'), false))) return
    const r = await act(() => post<{ webUrl: string }>('settings/stealth', { port: true, path: true }), t('settings.the_panel_has_moved'))
    if (r?.webUrl) location.href = r.webUrl + '#/settings'
  }
  return (
    <div className="card">
      <div className="card-head">
        <div><h3>{t('settings.hiding')}</h3><div className="card-sub">{t('settings.what_a_stranger_who_found')}</div></div>
        <button className="btn sm" onClick={run}>{t('settings.random_port_and_new_path')}</button>
      </div>
      <div className="card-pad stealth-grid">
        {items.map(([on, title, sub]) => (
          <div key={title} className={'stealth-item' + (on ? ' on' : '')}>
            <span className="dot" />
            <div><div>{title}</div><div className="muted small">{sub}</div></div>
          </div>
        ))}
      </div>
    </div>
  )
}

const roleNames: Record<NetRole['kind'], string> = { panel: t('common.panel'), sub: t('settings.subscriptions'), gateway: t('settings.node_gateway'), inbound: t('settings.inbound'), egress: t('settings.outbound') }
const roleColors: Record<NetRole['kind'], string> = { panel: 'pink', sub: 'pink', gateway: 'amber', inbound: 'green', egress: '' }
const purposeNames = { sub: t('settings.subscriptions'), panel: t('common.panel'), node: t('common.node_2') }

function RoleChips({ roles }: { roles: NetRole[] }) {
  if (roles.length === 0) return <span className="muted small">{t('common.not_used')}</span>
  return <span className="badges">{roles.map((r, i) => <Badge key={i} color={roleColors[r.kind]}>{roleNames[r.kind]}{r.tag ? ` ${r.tag}` : ''}</Badge>)}</span>
}

function NetworkCard({ data, error, reload }: { data: Network | null; error: string | null; reload: () => void }) {
  return (
    <div className="card">
      <div className="card-head">
        <div><h3>{t('settings.addresses_and_roles')}</h3><div className="card-sub">{t('settings.which_ip_serves_what_and')}</div></div>
        <button className="btn ghost sm" onClick={reload}>{t('settings.check_dns_again')}</button>
      </div>
      {!data ? <div className="card-pad net-skeleton"><Loading error={error} /></div> : (
        <div className="card-pad">
          {data.domains.length > 0 && (
            <div className="dns-list">
              {data.domains.map((d) => (
                <div key={d.name} className="dns-row">
                  <Badge color={d.ok ? 'green' : 'red'} dot>{d.ok ? t('settings.dns_is_fine') : d.error || t('common.error')}</Badge>
                  <span className="mono">{d.name}</span>
                  <span className="muted small">{purposeNames[d.purpose]}{d.purpose === 'node' ? ` ${d.node}` : ''}</span>
                  <span className="small text-2 grow" style={{ textAlign: 'right' }}>
                    {d.resolved.length > 0 ? <>→ <span className="mono">{d.resolved.join(', ')}</span></> : null}
                    {!d.ok && d.expected.length > 0 && <>{' '}<Trans k="settings.needs" v={{ join: d.expected.join(t('settings.or')) }} /></>}
                  </span>
                </div>
              ))}
              <div className="muted small" style={{ marginTop: 6 }}>{t('settings.domain_behind_a_cdn')}{' '}<Info>{t('settings.a_domain_behind_a_cdn')}</Info></div>
            </div>
          )}
          {data.nodes.map((n) => (
            <div key={n.id} className="net-node">
              <div className="net-node-head"><b>{n.name}</b> <span className="muted mono small">{n.code}</span> {n.local && <Badge>{t('settings.panel_server')}</Badge>}</div>
              {n.allRoles.length > 0 && <div className="small text-2" style={{ margin: '4px 0 8px' }}>{t('settings.on_all_addresses')}{' '}<RoleChips roles={n.allRoles} /></div>}
              {n.addresses.length === 0 ? <div className="muted small">{t('settings.the_node_has_not_reported')}</div> : (
                <table className="table compact">
                  <tbody>
                    {n.addresses.map((a) => (
                      <tr key={a.id}>
                        <td className="mono nowrap" style={{ width: 170 }}>{a.ip}{a.primary && <span className="muted">{' '}{t('settings.primary')}</span>}</td>
                        <td className="small muted nowrap" style={{ width: 90 }}>{a.interface || '—'}{!a.onInterface && <div className="pink-text">{t('settings.not_on_the_interface')}</div>}</td>
                        <td><div className="row" style={{ gap: 4 }}><RoleChips roles={a.roles} /></div></td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          ))}
          <div className="muted small">{t('settings.where_addresses_are_chosen')}{' '}<Info>{t('settings.the_panel_and_subscriptions')}</Info></div>
        </div>
      )}
    </div>
  )
}

function SettingRow({ s, hasLocalNode, onSaved }: { s: Setting; hasLocalNode: boolean; onSaved: () => void }) {
  const initial = s.value ?? ''
  const [val, setValue] = useState(initial)
  const act = useAction()
  const confirm = useConfirm()
  const dirty = val !== initial
  const effective = s.set ? initial : s.default ?? ''
  const ip = s.key === 'web.address' || s.key === 'sub.address'
  const decoyLink = s.key === 'web.decoy' || s.key === 'sub.decoy'

  const save = async (v: string) => {
    if (s.movesPanel && !(await confirm(t('settings.the_panel_will_move_to_2'), t('common.change'), false))) return
    const r = await act(() => put<{ webUrl: string }>('settings', { key: s.key, value: v }), t('common.saved'))
    if (r === undefined) return
    if (s.movesPanel && r.webUrl && r.webUrl !== location.origin + location.pathname) {
      location.href = r.webUrl + '#/settings'
      return
    }
    onSaved()
  }

  if (s.type === 'bool') {
    const on = effective === 'true'
    return (
      <div className="field">
        <div className="row between" style={{ flexWrap: 'nowrap' }}>
          <div>
            <div>{s.title}{s.help && <Info>{s.help}</Info>}</div>
            {s.hint && <div className="help muted small">{s.hint}</div>}
          </div>
          <Switch checked={on} onChange={(v) => save(v ? 'true' : 'false')} />
        </div>
      </div>
    )
  }
  const defaultLabel = ip ? (s.key === 'web.address' ? t('settings.same_as_subscriptions') : t('common.all_addresses')) : t('common.default_2', { v: s.default ? ` (${s.default})` : '' })
  const label = s.title === s.key
    ? <span className="mono">{s.key}</span>
    : <><span>{s.title}</span><span className="muted mono" style={{ fontWeight: 400 }}>{s.key}</span></>
  const help = decoyLink ? (
    <>
      {s.hint ? <>{s.hint}{' '}</> : null}
      <Trans
        k={hasLocalNode ? 'settings.custom_decoy_in_panel_node' : 'settings.custom_decoy_in_nodes'}
        v={{ link: <a href={hasLocalNode ? '#/nodes/edit-local' : '#/nodes'}>{t(hasLocalNode ? 'settings.panel_node_settings' : 'common.nodes')}</a> }}
      />
    </>
  ) : s.hint
  return (
    <Field label={label} help={help} info={s.help}>
      <div className="row" style={{ flexWrap: 'nowrap' }}>
        {s.type === 'select' ? (
          <select className="input" value={val} onChange={(e) => setValue(e.target.value)}>
            <option value="">{defaultLabel}</option>
            {s.options?.map((o) => <option key={o} value={o}>{o}</option>)}
          </select>
        ) : (
          <input className="input" value={val} inputMode={s.type === 'int' ? 'numeric' : undefined}
            onChange={(e) => setValue(e.target.value)} placeholder={s.default ? t('common.default_3', { default: s.default }) : ''}
            onKeyDown={(e) => e.key === 'Enter' && dirty && save(val)} />
        )}
        {dirty && <button className="btn primary" onClick={() => save(val)}>{t('common.save')}</button>}
        {dirty && <button className="btn ghost" onClick={() => setValue(initial)}>{t('common.cancel')}</button>}
      </div>
    </Field>
  )
}

// Сведения о сборке панели, ссылки на исходный код и переключение канала обновлений (stable / dev).
function AboutCard() {
  const updates = useUpdates(60000)
  const checkUpd = useCheckUpdates()
  const [logTargets, setLogTargets] = useState<LogTarget[] | null>(null)
  const upd = useRunUpdate(updates.data, updates.reload, (targets) => setLogTargets(targets))
  const engineer = useEngineer()
  const act = useAction()
  const confirm = useConfirm()
  const toast = useToast()

  const sess = getCacheEntry<{ version?: string; commit?: string }>('session')?.data
  const info = updates.data
  const version = info?.panel.version || sess?.version || ''
  const commit = info?.commit || sess?.commit || ''
  const curChannel: 'stable' | 'dev' = info?.channel || (info?.release?.startsWith('edge-') ? 'dev' : 'stable')
  const curRef = info?.ref || (info?.release?.startsWith('edge-') ? info.release.slice(5) : 'main')
  const isCustomBranch = curChannel === 'dev' && curRef !== 'main'

  const [customMode, setCustomMode] = useState(isCustomBranch)
  const [branch, setBranch] = useState(isCustomBranch ? curRef : '')
  const [switching, setSwitching] = useState(false)
  const [modalOpen, setModalOpen] = useState(false)
  const taps = useRef(0)
  const tapTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)

  useEffect(() => {
    if (curChannel === 'dev' && curRef !== 'main') {
      setCustomMode(true)
      setBranch(curRef)
    } else if (curChannel === 'stable' || curRef === 'main') {
      setCustomMode(false)
    }
  }, [curChannel, curRef])

  useEffect(() => () => {
    if (tapTimer.current) clearTimeout(tapTimer.current)
  }, [])

  const onVersionClick = () => {
    if (tapTimer.current) clearTimeout(tapTimer.current)
    taps.current += 1
    if (taps.current >= 5) {
      taps.current = 0
      setModalOpen(true)
      return
    }
    tapTimer.current = setTimeout(() => { taps.current = 0 }, 2000)
  }

  const switchChannel = async (channel: 'stable' | 'dev', ref: string) => {
    if (switching) return
    if (channel === curChannel && (channel === 'stable' || ref === curRef)) {
      if (channel === 'stable' || ref === 'main') setCustomMode(false)
      return
    }
    setSwitching(true)
    try {
      const next = await act(() => post<UpdateInfo>('updates/channel', { channel, ref }))
      if (!next) return
      setCacheEntry('updates', next)
      toast(channel === 'stable'
        ? t('settings.channel_switched_stable')
        : t('settings.channel_switched_dev', { ref: next.ref || ref }))
      if (next.checkError) toast(next.checkError, true)
    } finally {
      setSwitching(false)
    }
  }

  const runUpdateAll = async () => {
    if (!info) return
    const nodes = info.nodes.filter((n) => n.available)
    const what = [info.panel.available && t('common.panel'), nodes.length > 0 && t('common.nodes_count', { n: nodes.length })].filter(Boolean).join(t('updates.and'))
    if (await confirm(<><Trans k="updates.update_now_everything_updates" v={{ what }} /></>, t('updates.update_all'), false)) {
      upd.run('all')
    }
  }

  const panelUpd = info?.panel
  const showBranchOpt = engineer || isCustomBranch || customMode
  const branchClean = branch.trim()
  const branchDirty = customMode && branchClean.length > 0 && (curChannel !== 'dev' || branchClean !== curRef)

  return (
    <>
      <div className="card">
        <div className="card-head">
          <div>
            <h3>{t('settings.about_title')}</h3>
            <div className="card-sub">{t('settings.about_sub')}</div>
          </div>
          <div className="row" style={{ gap: 8 }}>
            <button type="button" className="btn ghost sm" disabled={checkUpd.checking || switching} onClick={checkUpd.check}>
              {checkUpd.checking ? <span className="spinner" /> : <Icon name="refresh" size={15} />} {t('settings.check_updates')}
            </button>
            {(info?.count || 0) > 0 && !upd.waiting && (
              <button type="button" className="btn primary sm" onClick={runUpdateAll}>
                {t('updates.update_all')}
              </button>
            )}
          </div>
        </div>
        <div className="card-pad about-split">
          <div className="about-meta">
            <div className="selfhost-kv">
              <span className="k">{t('settings.about_version')}</span>
              <span className="mono" style={{ userSelect: 'none' }} onClick={onVersionClick}>{fmtVersion(version)}</span>
              {panelUpd?.state ? (
                <UpdateChip n={panelUpd} latest={info?.latest || ''} onLog={() => setLogTargets([{ id: 0, name: panelUpd.name || t('common.panel'), initial: panelUpd }])} />
              ) : panelUpd?.available && info?.latest ? (
                <Badge color="amber" dot>{t('settings.about_update_available', { latest: fmtVersion(info.latest) })}</Badge>
              ) : info?.latest ? (
                <Badge color="green" dot>{t('settings.about_up_to_date')}</Badge>
              ) : null}
            </div>
            {commit && (
              <div className="selfhost-kv">
                <span className="k">{t('settings.about_commit')}</span>
                <a className="about-link mono" href={`${REPO_URL}/commit/${encodeURIComponent(commit)}`} target="_blank" rel="noreferrer">
                  {commit.slice(0, 7)} <Icon name="external" size={13} />
                </a>
              </div>
            )}
            <div className="selfhost-kv">
              <span className="k">{t('settings.about_repo')}</span>
              <a className="about-link mono" href={REPO_URL} target="_blank" rel="noreferrer">
                vyto4ka/vynel <Icon name="external" size={13} />
              </a>
            </div>
          </div>

          <div className="about-channel">
            <div className="about-branch-row">
              <span className="muted small">{t('settings.update_channel')}</span>
              <div className="seg sm" role="group" aria-label={t('settings.update_channel')}>
                <button
                  type="button"
                  disabled={switching}
                  className={!customMode && curChannel === 'stable' ? 'on' : ''}
                  onClick={() => { setCustomMode(false); switchChannel('stable', '') }}
                >
                  {t('settings.channel_stable')}
                </button>
                <button
                  type="button"
                  disabled={switching}
                  className={!customMode && curChannel === 'dev' && curRef === 'main' ? 'on' : ''}
                  onClick={() => { setCustomMode(false); switchChannel('dev', 'main') }}
                >
                  {t('settings.channel_main')}
                </button>
                {showBranchOpt && (
                  <button
                    type="button"
                    disabled={switching}
                    className={customMode ? 'on' : ''}
                    onClick={() => { setCustomMode(true); if (!branch) setBranch(isCustomBranch ? curRef : '') }}
                  >
                    {isCustomBranch && !customMode ? curRef : t('settings.channel_branch')}
                  </button>
                )}
              </div>
              {customMode && (
                <>
                  <input
                    className="input mono"
                    value={branch}
                    placeholder={t('settings.branch_placeholder')}
                    onChange={(e) => setBranch(e.target.value)}
                    onKeyDown={(e) => e.key === 'Enter' && branchDirty && switchChannel('dev', branchClean)}
                  />
                  {branchDirty && (
                    <button type="button" className="btn primary sm" disabled={switching} onClick={() => switchChannel('dev', branchClean)}>
                      {t('common.save')}
                    </button>
                  )}
                </>
              )}
            </div>
            <div className="muted small">
              {curChannel === 'stable'
                ? t('settings.channel_stable_hint')
                : t('settings.channel_dev_hint', { ref: curRef })}
            </div>
          </div>
        </div>
      </div>
      {logTargets && logTargets.length > 0 && (
        <UpdateLog id={logTargets[0].id} name={logTargets[0].name} targets={logTargets} onClose={() => { setLogTargets(null); updates.reload() }} />
      )}
      {modalOpen && (
        <Modal title="Чикенбурбе" onClose={() => setModalOpen(false)}>
          <img src="Чикенбурбе.png" alt="Чикенбурбе" style={{ width: '100%', display: 'block', borderRadius: 10 }} />
        </Modal>
      )}
    </>
  )
}

