import { FormEvent, useEffect, useMemo, useState } from 'react'
import { del, get, Inbound, InboundDetails, JoinInfo, Node, NodeWizard, patch, post, Profile, ProfileTemplate, Provider, SelfHost, SourceMap, Variable } from '../api'
import { UpdateChip, UpdateLog, useRunUpdate, useUpdates } from '../updates'
import { CodeEditor, parseObject, pretty } from '../code'
import { VariableTable } from './Profiles'
import { ProviderIcon } from './Billing'
import { ago, bps, bytes, nodeStateInfo, uptime } from '../format'
import { Badge, Bar, CopyField, Empty, Field, Icon, Info, Loading, Menu, Modal, PageInfo, Switch, useAction, useConfirm, useLoad } from '../ui'
import { t, Trans, Key } from '../i18n'
import { useAdvanced } from '../mode'
import { LinkProvider, pathsWith, useActiveLink } from '../jsonlink'

export function Nodes() {
  const nodes = useLoad(() => get<Node[]>('nodes'), [], 10000)
  const selfHost = useLoad(() => get<SelfHost>('self-host'), [], 30000)
  const profiles = useLoad(() => get<Profile[]>('profiles'))
  const [adding, setAdding] = useState(false)
  const [join, setJoin] = useState<JoinInfo | null>(null)
  const [checkOf, setCheckOf] = useState<Node | null>(null)
  const [editNode, setEditNode] = useState<Node | null>(null)
  const [attachTo, setAttachTo] = useState<Node | null>(null)
  const [wizardOf, setWizardOf] = useState<Node | null>(null)
  const [hostOf, setHostOf] = useState<Inbound | null>(null)
  const [configOf, setConfigOf] = useState<Inbound | null>(null)
  const act = useAction()
  const confirm = useConfirm()
  const reload = nodes.reload
  const updates = useUpdates(15000)
  const [updLog, setUpdLog] = useState<{ id: number; name: string } | null>(null)
  const upd = useRunUpdate(updates.data, () => { updates.reload(); reload() })
  // У локальной ноды версия панели: она обновляется вместе с панелью.
  const updOf = (n: Node) => n.local ? updates.data?.panel : updates.data?.nodes.find((x) => x.id === n.id)

  // Действия с нодой через её живое соединение с панелью.
  const nodeAction = async (n: Node, action: string, done: string, warn?: React.ReactNode, ok?: string) => {
    if (warn && !(await confirm(warn, ok || t('common.yes')))) return
    await act(() => post(`nodes/${n.id}/action`, { action }), done)
    reload()
  }

  return (
    <>
      <div className="toolbar">
        <PageInfo>{t('nodes.nodes_are_servers_with_xray')}</PageInfo>
        {(updates.data?.count || 0) > 0 && <button className="btn" onClick={async () => {
          if (await confirm(<><Trans k="nodes.update_all_outdated_servers" v={{ latest: updates.data!.latest }} /></>, t('nodes.update_all'), false))
            upd.run('all')
        }}><Icon name="refresh" />{' '}<Trans k="nodes.update_all_2" v={{ count: updates.data!.count }} /></button>}
        <button className="btn primary" onClick={() => setAdding(true)}><Icon name="plus" />{' '}{t('nodes.add_a_server')}</button>
      </div>

      {selfHost.data && <SelfHostCard sh={selfHost.data} />}

      {!nodes.data ? <Loading error={nodes.error} /> : nodes.data.length === 0 ? <div className="card"><Empty icon="🛰">{t('nodes.no_nodes')}</Empty></div> : (
        <div className="cards">
          {nodes.data.map((n) => {
            const st = nodeStateInfo[n.state] || { label: n.state, color: '' }
            const online = n.state === 'in sync' || n.state === 'syncing'
            const m = n.metrics
            return (
              <div key={n.id} className="card">
                <div className="card-head">
                  <div className="node-head">
                    <span className="flag">{n.flag || '🌐'}</span>
                    <div>
                      <h3>{n.name} <span className="muted small">{n.code}</span> {n.local && <Badge>{t('nodes.on_the_panel_server')}</Badge>} {n.manual && <Badge>{t('nodes.manual_node')}</Badge>}</h3>
                      <div className="card-sub">{n.domain || t('nodes.no_domain')}{' '}<Trans k="nodes.contact" v={{ v: ago(n.lastSeenAt) }} /></div>
                      {n.provider && (
                        <div className="row small" style={{ gap: 8, marginTop: 4 }}>
                          <a className="provider-chip" href={n.provider.loginUrl || n.provider.url || '#/billing'} target={n.provider.loginUrl || n.provider.url ? '_blank' : undefined}
                            rel="noreferrer" title={n.provider.loginUrl ? t('nodes.sign_in_to_the_provider') : t('nodes.provider_s_site')}>
                            <ProviderIcon p={n.provider} size={16} />{n.provider.name}
                          </a>
                        </div>
                      )}
                    </div>
                  </div>
                  <div className="row" style={{ gap: 6 }}>
                    {updOf(n) && <UpdateChip n={updOf(n)!} latest={updates.data!.latest} onLog={() => setUpdLog({ id: n.local ? 0 : n.id, name: n.local ? t('common.panel') : n.name })} />}
                    {n.setupStep > 0 && <Badge color="amber" dot>{t('nodes.without_profiles')}</Badge>}
                    <Badge color={st.color} dot>{st.label}</Badge>
                    <Switch checked={n.enabled} onChange={(v) => act(() => patch(`nodes/${n.id}`, { enabled: v }), v ? t('nodes.node_turned_on') : t('nodes.node_turned_off')).then(reload)} />
                    <button className="btn ghost icon sm" title={t('nodes.check_connection_config_dns')} onClick={() => setCheckOf(n)}><Icon name="search" /></button>
                    <button className="btn ghost icon sm" title={t('common.edit')} onClick={() => setEditNode(n)}><Icon name="edit" /></button>
                    <Menu title={t('common.actions')} items={[
                      { label: t('nodes.restart_xray'), icon: 'refresh', disabled: !online, onClick: () => nodeAction(n, 'xray', t('nodes.xray_restarted')) },
                      ...(!n.manual ? [{ label: t('nodes.restart_the_web_server'), icon: 'refresh', disabled: !online, onClick: () => nodeAction(n, 'caddy', t('nodes.web_server_restarted')) }] : []),
                      { label: t('nodes.resend_the_config'), icon: 'nodes', disabled: !online, onClick: () => nodeAction(n, 'resync', t('nodes.config_sent')) },
                      'divider',
                      { label: updOf(n)?.available ? t('nodes.update_to', { latest: updates.data?.latest }) : t('nodes.update_vynel_xray_and_nginx'), icon: 'refresh', disabled: !online || !!updOf(n)?.updating,
                        onClick: async () => {
                          if (await confirm(n.local ? t('nodes.update_the_panel_and_the') : <><Trans k="nodes.update_the_node_it_will" v={{ name: n.name }} /></>, t('nodes.update'), false))
                            upd.run(n.local ? 'panel' : 'node', n.id)
                        } },
                      { label: t('nodes.update_log'), icon: 'audit', onClick: () => setUpdLog({ id: n.local ? 0 : n.id, name: n.local ? t('common.panel') : n.name }) },
                      { label: n.local ? t('nodes.restart_the_service_and_the') : t('nodes.restart_the_vynel_service'), icon: 'power', disabled: !online,
                        onClick: () => nodeAction(n, 'service', t('nodes.the_service_is_restarting'),
                          n.local ? t('nodes.restart_the_vynel_service_the') : t('nodes.restart_the_vynel_service_on'), t('nodes.restart')) },
                      { label: t('nodes.reboot_the_server'), icon: 'power', danger: true, disabled: !online,
                        onClick: () => nodeAction(n, 'reboot', t('nodes.the_server_is_rebooting'),
                          <><Trans k="nodes.reboot_the_server_2" v={{ name: n.name }} />{n.local ? t('nodes.this_is_the_panel_server') : t('nodes.the_vpn_on_it_goes')}</>, t('nodes.reboot')) },
                      ...(!n.local ? ['divider' as const,
                        { label: t('nodes.new_join_token'), icon: 'key', onClick: async () => {
                          if (await confirm(t('nodes.issue_a_new_token_the'), t('nodes.issue'))) {
                            const j = await act(() => post<JoinInfo>(`nodes/${n.id}/token`))
                            if (j) { setJoin(j); reload() }
                          }
                        } },
                        { label: t('nodes.delete_the_node'), icon: 'trash', danger: true, onClick: async () => {
                          if (await confirm(<><Trans k="nodes.delete_the_node_its_inbounds" v={{ name: n.name }} /></>, t('common.delete')))
                            act(() => del(`nodes/${n.id}`), t('nodes.node_deleted')).then(reload)
                        } }] : []),
                    ]} />
                  </div>
                </div>
                <div className="card-pad">
                  {n.manual && (
                    <div className="alert amber" style={{ marginBottom: 14 }}>{t('nodes.auto_setup_disabled')}</div>
                  )}
                  {n.problems.length > 0 && (
                    <div className="alert pink" style={{ marginBottom: 14 }}>
                      <ul className="list-plain" style={{ color: 'inherit' }}>{n.problems.map((p, i) => <li key={i}>{p}</li>)}</ul>
                    </div>
                  )}
                  {n.state === 'pending' && (
                    <div className="alert amber row between" style={{ marginBottom: 14 }}>
                      <span>{t('nodes.the_node_has_not_connected')}</span>
                      <button className="btn sm" onClick={() => setCheckOf(n)}>{t('nodes.checklist')}</button>
                    </div>
                  )}
                  {n.setupStep > 0 && (
                    <div className="alert amber row between" style={{ marginBottom: 14 }}>
                      <span><Trans k="nodes.setup_not_finished" v={{ n: 6 - n.setupStep }} /></span>
                      <button className="btn sm" onClick={() => setWizardOf(n)}><Icon name="edit" />{' '}{t('nodes.continue_setup')}</button>
                    </div>
                  )}
                  <div className="cards" style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(260px, 1fr))' }}>
                    <div>
                      {m ? (
                        <>
                          <div className="meter"><span>CPU</span><Bar value={m.cpu} max={100} /><span>{m.cpu.toFixed(0)}%</span></div>
                          <div className="meter"><span>RAM</span><Bar value={m.memUsed} max={m.memTotal} /><span>{m.memTotal ? Math.round((100 * m.memUsed) / m.memTotal) : 0}%</span></div>
                          <div className="kv small" style={{ marginTop: 10, gridTemplateColumns: '110px 1fr' }}>
                            <div className="k">{t('common.online')}</div><div>{m.online}</div>
                            <div className="k">{t('nodes.network')}</div><div>↓ {bps(m.rxBps)} · ↑ {bps(m.txBps)}</div>
                            <div className="k">{t('nodes.today')}</div><div>{bytes(n.todayBytes)}</div>
                            <div className="k">{t('nodes.uptime')}</div><div>{uptime(m.uptime)}</div>
                          </div>
                        </>
                      ) : <div className="muted small">{t('nodes.no_metrics_yet')}</div>}
                      <div className="muted small" style={{ marginTop: 10 }}><Trans k="nodes.xray_agent" v={{ v: n.xrayVersion || '—', v2: n.agentVersion || '—', v3: !n.manual && n.nginxVersion ? ` · nginx ${n.nginxVersion}` : !n.manual && n.caddyVersion && ` · Caddy ${n.caddyVersion}` }} /></div>
                      {n.addresses.length > 0 && (
                        <div className="row small" style={{ marginTop: 8, gap: 6 }}>
                          {n.addresses.map((a) => <span key={a.id} className="chip static">{a.ip}{a.primary && ' ★'}{!a.onInterface && t('nodes.not_on_the_interface')}</span>)}
                        </div>
                      )}
                    </div>
                    <div>
                      <div className="row between" style={{ marginBottom: 8 }}>
                        <span className="label">{t('nodes.inbounds')}</span>
                        <button className="btn sm" onClick={() => setAttachTo(n)}><Icon name="plus" />{' '}{t('common.profile')}</button>
                      </div>
                      {n.inbounds.length === 0 && <div className="muted small">{t('nodes.none_nothing_to_connect_to')}</div>}
                      {n.inbounds.map((i) => (
                        <div key={i.id} className="inb">
                          <div className="row between">
                            <div>
                              <b className="mono">{i.tag}</b> <span className="muted small">· {i.profileName}</span>
                              <div className="small text-2">{i.listen}:{i.port}{i.host && ` → ${i.host.address}:${i.host.port} · ${i.host.network}/${i.host.security}`}</div>
                              {i.host && <div className="small muted">«{i.host.remark}»{i.host.sni && ` · SNI ${i.host.sni}`}{i.host.hidden && t('nodes.hidden_from_subscriptions')}</div>}
                              {i.error && <div className="small pink-text">{i.error}</div>}
                            </div>
                            <Switch checked={i.enabled} onChange={(v) => act(() => patch(`inbounds/${i.id}`, { enabled: v }), v ? t('nodes.inbound_turned_on') : t('nodes.inbound_turned_off')).then(reload)} />
                          </div>
                          <div className="row" style={{ marginTop: 8, gap: 6 }}>
                            <button className="btn sm" onClick={() => setHostOf(i)}><Icon name="edit" />{' '}{t('nodes.connection_point')}</button>
                            <button className="btn sm primary" onClick={() => setConfigOf(i)}><Icon name="code" />{' '}{t('nodes.configure')}</button>
                            <button className="btn sm" onClick={async () => {
                              if (await confirm(t('nodes.generate_new_reality_keys_and'), t('nodes.generate')))
                                act(() => patch(`inbounds/${i.id}`, { regenerateKeys: true }), t('nodes.keys_updated')).then(reload)
                            }}><Icon name="key" />{' '}{t('nodes.new_keys')}</button>
                            <button className="btn sm danger" onClick={async () => {
                              if (await confirm(<><Trans k="nodes.remove_the_inbound_from_the" v={{ tag: i.tag }} /></>, t('common.remove')))
                                act(() => del(`inbounds/${i.id}`), t('nodes.inbound_removed')).then(reload)
                            }}><Icon name="trash" /></button>
                          </div>
                        </div>
                      ))}
                    </div>
                  </div>
                </div>
              </div>
            )
          })}
        </div>
      )}

      {adding && <AddNode profiles={profiles.data || []} onClose={() => setAdding(false)} onAdded={(j) => { setJoin(j); reload() }} />}
      {join && <NodeSetup j={join} onClose={() => { setJoin(null); reload() }} onDone={(n) => { setJoin(null); setWizardOf(n); reload() }} />}
      {wizardOf && <NodeWizardModal node={wizardOf} profiles={profiles.data || []} onClose={() => { setWizardOf(null); reload() }} />}
      {updLog && <UpdateLog id={updLog.id} name={updLog.name} onClose={() => { setUpdLog(null); updates.reload() }} />}
      {checkOf && <NodeSetup node={checkOf} onClose={() => { setCheckOf(null); reload() }} />}
      {editNode && <EditNode n={editNode} onClose={() => setEditNode(null)} onSaved={() => { setEditNode(null); reload() }} />}
      {attachTo && <Attach node={attachTo} profiles={profiles.data || []} onClose={() => setAttachTo(null)} onDone={() => { setAttachTo(null); reload() }} />}
      {hostOf && <HostForm i={hostOf} onClose={() => setHostOf(null)} onSaved={() => { setHostOf(null); reload() }} />}
      {configOf && <InboundEditor i={configOf} onClose={() => setConfigOf(null)} onSaved={reload} />}
    </>
  )
}

// Карточка панели-хоста: этот сервер в списке нод, но без действий ноды.
function SelfHostCard({ sh }: { sh: SelfHost }) {
  const certOf = (d: string) => sh.certs.find((c) => c.domain === d)
  return (
    <div className="card">
      <div className="card-head">
        <div className="node-head">
          <span className="flag">🎛</span>
          <div>
            <h3>{t('nodes.panel_this_server')}</h3>
            <div className="card-sub">
              {sh.domains.length > 0 ? sh.domains.join(' · ') : t('nodes.no_domain')}
            </div>
          </div>
        </div>
        <div className="row" style={{ gap: 6 }}>
          {sh.withNode ? <Badge color="green" dot>{t('nodes.panel_with_node')}</Badge> : <Badge dot>{t('nodes.panel_without_node')}</Badge>}
        </div>
      </div>
      <div className="card-pad">
        <div className="kv small" style={{ gridTemplateColumns: '110px 1fr' }}>
          <div className="k">{t('nodes.panel_version')}</div><div>v{sh.version}</div>
          <div className="k">{t('nodes.uptime')}</div><div>{uptime(sh.uptime)}</div>
          {sh.domains.map((d) => {
            const c = certOf(d)
            return (
              <div key={d} style={{ display: 'contents' }}>
                <div className="k">{t('nodes.panel_certificate')}</div>
                <div>
                  {!c || !c.ok ? <span className="muted">{t('nodes.panel_cert_placeholder')}</span> :
                    c.placeholder ? <>{d} — <span className="amber-text">{t('nodes.panel_cert_placeholder')}</span></> :
                      <Trans k="nodes.panel_cert_expires" v={{ v: new Date(c.notAfter * 1000).toLocaleDateString() }} />}{c?.ok && !c.placeholder && c.issuer ? ` · ${c.issuer}` : ''}
                </div>
              </div>
            )
          })}
        </div>
        <div className="muted small" style={{ marginTop: 10 }}>{t('nodes.panel_hosts_the_web')}</div>
      </div>
    </div>
  )
}

// Мастер добавления ноды, шаг 1: токен/подключение. Нода создаётся сразу по кнопке —
// дальше настройка продолжается шагами (NodeWizardModal).
function AddNode({ profiles, onClose, onAdded }: { profiles: Profile[]; onClose: () => void; onAdded: (j: JoinInfo) => void }) {
  const [name, setName] = useState('')
  const [country, setCountry] = useState('')
  const [domain, setDomain] = useState('')
  const [manual, setManual] = useState(false)
  const act = useAction()
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const j = await act(() => post<JoinInfo>('nodes', { name, country, domain, manual }), t('nodes.node_created'))
    if (j) onAdded(j)
  }
  return (
    <Modal title={t('nodes.add_a_server_step_1')} onClose={onClose}
      footer={<><button className="btn ghost" onClick={onClose}>{t('common.cancel')}</button><button className="btn primary" form="add-node">{t('nodes.next_install_command')}</button></>}>
      <WizardSteps at={0} />
      <form id="add-node" onSubmit={submit}>
        <Field label={t('nodes.node_domain')} help={t('nodes.an_a_record_to_the')} info={t('nodes.reality_self_steal_poses_as')}>
          <input className="input" autoFocus value={domain} onChange={(e) => setDomain(e.target.value)} placeholder="de.example.com" />
        </Field>
        <div className="grid2">
          <Field label={t('nodes.name_shown_in_apps')}><input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder={t('nodes.detected_during_installation')} /></Field>
          <Field label={t('nodes.country_code')} help={t('nodes.empty_detected_from_the_server')}><input className="input" maxLength={2} value={country} onChange={(e) => setCountry(e.target.value.toUpperCase())} placeholder={t('nodes.auto')} /></Field>
        </div>
        <label className="check"><input type="checkbox" checked={manual} onChange={(e) => setManual(e.target.checked)} />{' '}{t('nodes.manual_node_help')}</label>
        <p className="muted small" style={{ marginBottom: 0 }}><Trans k="nodes.wizard_after_install" v={{ count: profiles.length }} /></p>
      </form>
    </Modal>
  )
}

interface Step { key: string; title: string; state: 'ok' | 'wait' | 'fail' | 'skip'; detail?: string }

function WizardSteps({ at, all }: { at: number; all?: boolean }) {
  const names = all
    ? [t('nodes.server'), t('nodes.installation'), t('nodes.profiles'), t('nodes.node_vars'), t('nodes.checks')]
    : [t('nodes.server'), t('nodes.installation_and_check')]
  return (
    <div className="wizard-steps">
      {names.map((n, i) => <div key={n} className={i < at ? 'done' : i === at ? 'on' : ''}><span>{i < at ? '✓' : i + 1}</span>{n}</div>)}
    </div>
  )
}

const stepIcon = { ok: '✓', wait: '', fail: '!', skip: '–' }

// Мастер донастройки ноды: шаги 2–5 (профили → переменные → проверки → применение). Шаг
// хранится не отдельно, а выводится из данных ноды: нет профилей = шаг 2, пустые переменные
// ноды = шаг 3, требования не подтверждены = шаг 4. Закрыл диалог — открыл снова через
// «Продолжить настройку» на карточке: попадаешь на тот же шаг.
function NodeWizardModal({ node: initial, profiles, onClose }: { node: Node; profiles: Profile[]; onClose: () => void }) {
  const [node, setNode] = useState(initial)
  const [at, setAt] = useState(() => Math.max(2, initial.setupStep > 0 ? initial.setupStep : 2))
  const { data: wizard, error } = useLoad(() => get<NodeWizard>(`nodes/${node.id}/wizard`), [node.id, at], at >= 4 ? 5000 : 0)
  const tpls = useLoad(() => get<ProfileTemplate[]>('profile-templates'))
  const nodes = useLoad(() => get<Node[]>('nodes'), [node.id, at], 6000)
  const act = useAction()

  const cur = nodes.data?.find((n) => n.id === node.id) || node
  // The step derived from the node's data (backend's number): follow it forward, keep the
  // admin's place when he stepped back.
  const derived = cur.setupStep > 0 ? cur.setupStep : 0
  const step = Math.min(at, Math.max(derived, 2))
  useEffect(() => { if (derived > at) setAt(derived) }, [derived, at])

  const back = () => setAt(Math.max(2, step - 1))
  const next = () => setAt(step + 1)
  const toNode = (n?: Node): Node | undefined => {
    if (n) setNode(n)
    return n
  }

  return (
    <Modal title={t('nodes.node', { code: node.code })} onClose={onClose} wide
      footer={<>
        <span className="muted small grow">{t('nodes.wizard_saved_on_each_step')}</span>
        {step > 2 && <button className="btn ghost" onClick={back}>{t('common.back')}</button>}
        {step < 5 && <button className="btn primary" onClick={next}>{t('common.next')}</button>}
        {step === 5 && <button className="btn primary" onClick={onClose}>{t('common.done')}</button>}
      </>}>
      <WizardSteps at={step - 1} all />
      {!nodes.data ? <Loading error={nodes.error} /> : step === 2 ? <StepProfiles node={cur} profiles={profiles} onDone={(n) => { toNode(n); next() }} /> :
        step === 3 ? <StepVars node={cur} profiles={profiles} tpls={tpls.data || []} onDone={(n) => { toNode(n); next() }} onBack={back} /> :
          step === 4 ? <StepChecks node={cur} wizard={wizard} error={error} onDone={(n) => { toNode(n); next() }} onBack={back} /> :
            <StepApply node={cur} act={act} onBack={back} onDone={onClose} />}
    </Modal>
  )
}

// Шаг 2: выбор профилей галочками. Привязка (AttachProfile) сразу по кнопке.
function StepProfiles({ node, profiles, onDone }: { node: Node; profiles: Profile[]; onDone: (n?: Node) => void }) {
  const act = useAction()
  const [busy, setBusy] = useState(false)
  const toggle = async (p: Profile, on: boolean) => {
    setBusy(true)
    if (on) {
      await act(() => post('inbounds', { nodeId: node.id, profileId: p.id }), t('nodes.profile_added_to_the_node'))
    } else {
      const i = node.inbounds.find((x) => x.profileId === p.id)
      if (i) await act(() => del(`inbounds/${i.id}`), t('nodes.inbound_removed'))
    }
    setBusy(false)
  }
  return (
    <div>
      <p className="text-2" style={{ marginTop: 0 }}>{t('nodes.pick_profiles_for_node')}</p>
      {profiles.length === 0 ? <div className="alert amber">{t('nodes.first_create_a_profile_on')}</div> : (
        <>
          {node.manual && <div className="alert amber">{t('nodes.manual_node_nginx_note')}</div>}
          {profiles.map((p) => (
            <label key={p.id} className="check" style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '8px 0' }}>
              <input type="checkbox" disabled={busy} checked={node.inbounds.some((i) => i.profileId === p.id)} onChange={(e) => toggle(p, e.target.checked)} />
              <span><b>{p.name}</b> <span className="muted small">· {p.templateTitle}</span></span>
            </label>
          ))}
          <button className="btn primary" style={{ marginTop: 14 }} disabled={node.inbounds.length === 0} onClick={() => onDone()}>
            {node.inbounds.length === 0 ? t('nodes.skip_for_now') : t('nodes.next_variables')}{' →'}
          </button>
        </>
      )}
    </div>
  )
}

// Шаг 3: переменные выбранных профилей (scope=node, source=input) + «сгенерировать» для
// generate-переменных (регенерация — существующий механизм regenerateKeys).
function StepVars({ node, profiles, tpls, onDone, onBack }: { node: Node; profiles: Profile[]; tpls: ProfileTemplate[]; onDone: (n?: Node) => void; onBack: () => void }) {
  const act = useAction()
  const [values, setValues] = useState<Record<string, number | string>>(() => {
    const out: Record<string, number | string> = {}
    for (const i of node.inbounds) for (const [k, v] of Object.entries(i.values || {})) out[k] = v == null ? '' : (typeof v === 'number' ? v : String(v))
    return out
  })
  const [regen, setRegen] = useState(false)
  const varsOf = (i: Inbound): Variable[] => {
    const tid = profiles.find((p) => p.id === i.profileId)?.templateId
    const p = tpls.find((x) => x.id === tid)
    return (p?.variables || []).filter((v) => v.scope === 'node' && (v.source === 'input' || v.source === 'generate'))
  }
  const save = async () => {
    for (const i of node.inbounds) {
      const mine: Record<string, number | string> = {}
      for (const v of varsOf(i)) if (v.source === 'input' && values[v.name] !== undefined && values[v.name] !== '') mine[v.name] = values[v.name]
      const r = await act(() => patch(`inbounds/${i.id}`, { values: mine, ...(regen ? { regenerateKeys: true } : {}) }), t('nodes.inbound_saved_the_node_applies'))
      if (r === undefined) return
    }
    onDone()
  }
  const hasGen = node.inbounds.some((i) => varsOf(i).some((v) => v.source === 'generate'))
  return (
    <div>
      <p className="text-2" style={{ marginTop: 0 }}>{t('nodes.node_variables_intro')}</p>
      {node.inbounds.map((i) => {
        const vars = varsOf(i)
        const tid = profiles.find((p) => p.id === i.profileId)?.templateId
        const tpl = tpls.find((x) => x.id === tid)
        return (
          <div key={i.id} style={{ marginBottom: 16 }}>
            <div className="label" style={{ margin: '6px 0 8px' }}>{tpl?.title || i.profileName} <span className="muted small mono">{i.tag}</span></div>
            <VariableTable vars={vars} values={values as unknown as Record<string, string>} onChange={(v) => setValues({ ...values, ...v } as Record<string, number | string>)} empty={t('nodes.the_template_has_no_node')} />
          </div>
        )
      })}
      {hasGen && (
        <label className="check" style={{ margin: '8px 0 14px' }}>
          <input type="checkbox" checked={regen} onChange={(e) => setRegen(e.target.checked)} />{' '}{t('nodes.generate_keys_and_other_values')}</label>
      )}
      <div className="row" style={{ gap: 8 }}>
        <button className="btn ghost" onClick={onBack}>{t('common.back')}</button>
        <button className="btn primary" onClick={save}>{t('nodes.next_checks')}</button>
      </div>
    </div>
  )
}

// Шаг 4: требования node_requirements выбранных профилей: ✓/✗ от панели (DNS уже проверяет
// панель), для manual-нод — «проверьте сами».
function StepChecks({ node, wizard, error, onDone, onBack }: { node: Node; wizard: NodeWizard | null; error?: unknown; onDone: (n?: Node) => void; onBack: () => void }) {
  const act = useAction()
  const [confirmed, setConfirmed] = useState(false)
  const hasFail = !!wizard?.steps.some((x) => x.state === 'fail')
  const confirm = async () => {
    const r = await act(() => post(`nodes/${node.id}/confirm-requirements`, { confirm: true }), t('common.saved'))
    if (r !== undefined) { setConfirmed(true); onDone() }
  }
  return (
    <div>
      <p className="text-2" style={{ marginTop: 0 }}>{node.manual ? t('nodes.checks_manual_intro') : t('nodes.checks_intro')}</p>
      {!wizard ? <Loading error={error as string | undefined} /> : (
        <div className="checklist">
          {wizard.steps.map((s) => (
            <div key={s.key} className={'check-step ' + s.state}>
              <span className="check-icon">{stepIcon[s.state]}</span>
              <div>
                <div>{s.title}</div>
                {s.detail && <div className={'small ' + (s.state === 'fail' ? 'pink-text' : 'muted')}>{s.detail}</div>}
              </div>
            </div>
          ))}
        </div>
      )}
      {node.manual && <div className="alert amber" style={{ marginTop: 12 }}>{t('nodes.manual_node_check_note')}</div>}
      <div className="row" style={{ gap: 8, marginTop: 14 }}>
        <button className="btn ghost" onClick={onBack}>{t('common.back')}</button>
        <button className="btn primary" onClick={confirm} disabled={!node.manual && hasFail && !confirmed}>
          {node.manual ? t('nodes.confirm_checked') : hasFail ? t('nodes.confirm_anyway') : t('nodes.confirm_checks')}
        </button>
      </div>
    </div>
  )
}

// Шаг 5: применить — толчок reconciler'у («Отправить конфиг»), тот же action resync.
function StepApply({ node, act, onBack, onDone }: { node: Node; act: ReturnType<typeof useAction>; onBack: () => void; onDone: () => void }) {
  const online = node.state === 'in sync' || node.state === 'syncing'
  return (
    <div>
      <p className="text-2" style={{ marginTop: 0 }}>{t('nodes.apply_intro')}</p>
      <div className="alert green" style={{ marginTop: 0 }}>{t('nodes.setup_finished')}</div>
      <div className="row" style={{ gap: 8, marginTop: 14 }}>
        <button className="btn ghost" onClick={onBack}>{t('common.back')}</button>
        <button className="btn primary" disabled={!online} onClick={async () => {
          await act(() => post(`nodes/${node.id}/action`, { action: 'resync' }), t('nodes.config_sent'))
          onDone()
        }}>{t('nodes.send_config')}</button>
        <button className="btn ghost" onClick={onDone}>{t('nodes.finish_setup')}</button>
      </div>
    </div>
  )
}

// Мастер, шаг 2: команда установки и живой чеклист, пока нода не заработает.
function NodeSetup({ j, node, onClose, onDone }: { j?: JoinInfo; node?: Node; onClose: () => void; onDone?: (n: Node) => void }) {
  const id = j?.id ?? node!.id
  const title = j ? t('nodes.add_a_server_step_2') : t('nodes.checking_the_node', { name: node!.name })
  const { data, error } = useLoad(() => get<Step[]>(`nodes/${id}/checklist`), [id], 4000)
  const nodes = useLoad(() => get<Node[]>('nodes'), [id], 5000)
  const cur = nodes.data?.find((n) => n.id === id)
  const done = data?.every((s) => s.state === 'ok' || s.state === 'skip')
  const firstOpen = data?.findIndex((s) => s.state !== 'ok' && s.state !== 'skip') ?? -1
  return (
    <Modal title={title} onClose={onClose} wide footer={done && onDone && cur
      ? <><button className="btn ghost" onClick={onClose}>{t('nodes.finish_later')}</button><button className="btn primary" onClick={() => onDone(cur)}>{t('nodes.next_step_setup')}</button></>
      : <button className="btn primary" onClick={onClose}>{done ? t('common.done') : t('common.close')}</button>}>
      {j && <WizardSteps at={1} all />}
      {j?.warnings && j.warnings.length > 0 && <div className="alert amber" style={{ marginBottom: 14 }}>{j.warnings.join('; ')}</div>}
      {j && (j.error ? <div className="alert pink">{j.error}</div> : (
        <>
          <p className="text-2" style={{ marginTop: 0 }}>{t('nodes.on_the_new_server_ubuntu')}</p>
          <CopyField value={j.command || ''} wrap />
          <p className="muted small"><Trans k="nodes.the_command_is_single_use" v={{ ttl: j.ttl }} /></p>
        </>
      ))}
      <div className="checklist">
        {!data ? <Loading error={error} /> : data.map((s, i) => (
          <div key={s.key} className={'check-step ' + s.state + (i === firstOpen && s.state === 'wait' ? ' current' : '')}>
            <span className="check-icon">{i === firstOpen && s.state === 'wait' ? <span className="spinner" /> : stepIcon[s.state]}</span>
            <div>
              <div>{s.title}</div>
              {s.detail && <div className={'small ' + (s.state === 'fail' ? 'pink-text' : 'muted')}>{s.detail}</div>}
            </div>
          </div>
        ))}
      </div>
      {done && <div className="alert green" style={{ marginTop: 12 }}>{t('nodes.the_node_works_users_get')}</div>}
    </Modal>
  )
}

function EditNode({ n, onClose, onSaved }: { n: Node; onClose: () => void; onSaved: () => void }) {
  const [name, setName] = useState(n.name)
  const [country, setCountry] = useState(n.country)
  const [domain, setDomain] = useState(n.domain)
  const [providerId, setProviderId] = useState(n.provider?.id || 0)
  const providers = useLoad(() => get<{ providers: Provider[] }>('billing'))
  const act = useAction()
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const r = await act(async () => {
      await patch(`nodes/${n.id}`, { name, country, domain })
      if (providerId !== (n.provider?.id || 0)) await patch(`nodes/${n.id}/billing`, { providerId })
      return true
    }, t('common.saved'))
    if (r !== undefined) onSaved()
  }
  return (
    <Modal title={t('nodes.node', { code: n.code })} onClose={onClose}
      footer={<><button className="btn ghost" onClick={onClose}>{t('common.cancel')}</button><button className="btn primary" form="edit-node">{t('common.save')}</button></>}>
      <form id="edit-node" onSubmit={submit}>
        <div className="grid2">
          <Field label={t('common.name')}><input className="input" value={name} onChange={(e) => setName(e.target.value)} /></Field>
          <Field label={t('nodes.country_code')}><input className="input" maxLength={2} value={country} onChange={(e) => setCountry(e.target.value.toUpperCase())} /></Field>
        </div>
        <Field label={t('nodes.domain')} info={t('nodes.changing_the_domain_changes')}>
          <input className="input" value={domain} onChange={(e) => setDomain(e.target.value)} />
        </Field>
        <Field label={t('common.provider')} help={<><Trans k="nodes.price_payment_and_traffic_limit" v={{ v: <a href="#/billing" onClick={onClose}>{t('nodes.billing')}</a> }} /></>}>
          <select className="input" value={providerId} onChange={(e) => setProviderId(Number(e.target.value))}>
            <option value={0}>{t('common.not_set')}</option>
            {(providers.data?.providers || []).map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
          </select>
        </Field>
      </form>
    </Modal>
  )
}

function Attach({ node, profiles, onClose, onDone }: { node: Node; profiles: Profile[]; onClose: () => void; onDone: () => void }) {
  const free = profiles.filter((p) => !node.inbounds.some((i) => i.profileId === p.id))
  const [profileId, setProfileId] = useState(free[0]?.id || profiles[0]?.id || 0)
  const [port, setPort] = useState('')
  const [values, setValues] = useState<Record<string, string>>({})
  const tpls = useLoad(() => get<ProfileTemplate[]>('profile-templates'))
  const act = useAction()
  const p = profiles.find((x) => x.id === profileId)
  const inputs = (tpls.data?.find((t) => t.id === p?.templateId)?.variables || []).filter((v) => v.scope === 'node' && v.source === 'input')
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const r = await act(() => post('inbounds', { nodeId: node.id, profileId, port: Number(port) || 0, values }), t('nodes.profile_added_to_the_node'))
    if (r !== undefined) onDone()
  }
  return (
    <Modal title={t('nodes.profile_on_the_node', { code: node.code })} onClose={onClose}
      footer={<><button className="btn ghost" onClick={onClose}>{t('common.cancel')}</button><button className="btn primary" form="attach" disabled={!profileId}>{t('common.add')}</button></>}>
      {profiles.length === 0 ? <div className="muted">{t('nodes.first_create_a_profile_on')}</div> : (
        <form id="attach" onSubmit={submit}>
          <Field label={t('common.profile')} info={t('nodes.the_tag_reality_keys_and')}>
            <select className="input" value={profileId} onChange={(e) => { setProfileId(Number(e.target.value)); setValues({}) }}>
              {profiles.map((x) => <option key={x.id} value={x.id}>{x.name} — {x.templateTitle}</option>)}
            </select>
          </Field>
          {inputs.map((v) => (
            <Field key={v.name} label={<>{v.name}{v.optional ? '' : <span className="pink-text"> *</span>}</>} info={v.description || undefined}>
              <input className="input" value={values[v.name] || ''} onChange={(e) => setValues({ ...values, [v.name]: e.target.value })}
                placeholder={v.default != null ? String(v.default) : v.default_from ? t('nodes.by_default_from', { default_from: v.default_from }) : ''} />
            </Field>
          ))}
          <Field label={t('nodes.port')} help={t('nodes.inbound_port_help')}><input className="input" inputMode="numeric" value={port} onChange={(e) => setPort(e.target.value.replace(/\D/g, ''))} /></Field>
        </form>
      )}
    </Modal>
  )
}

// Фрагментация ClientHello в виде finalmask (у Xray пауза называется delay).
const fragmentMask = { tcp: [{ type: 'fragment', settings: { packets: 'tlshello', length: '10-20', delay: '10-20' } }] }

function HostForm({ i, onClose, onSaved }: { i: Inbound; onClose: () => void; onSaved: () => void }) {
  const o = i.hostOverride
  const [remark, setRemark] = useState(String(o.remark ?? ''))
  const [address, setAddress] = useState(String(o.address ?? ''))
  const [port, setPort] = useState(o.port != null ? String(o.port) : '')
  const [sni, setSni] = useState(String(o.sni ?? ''))
  const [fp, setFp] = useState(String(o.fingerprint ?? ''))
  const [hidden, setHidden] = useState(!!o.hidden)
  const [maskText, setMaskText] = useState(o.finalmask ? pretty(o.finalmask) : '')
  const mask = parseObject(maskText)
  const adv = useAdvanced()
  const act = useAction()
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (mask.error) return
    const v = (s: string) => (s.trim() === '' ? null : s.trim())
    const host = { remark: v(remark), address: v(address), port: port ? Number(port) : null, sni: v(sni), fingerprint: v(fp), hidden: hidden || null,
      finalmask: mask.value && Object.keys(mask.value).length ? mask.value : null }
    const r = await act(() => patch(`inbounds/${i.id}`, { host }), t('nodes.connection_point_saved'))
    if (r !== undefined) onSaved()
  }
  const h = i.host
  return (
    <Modal title={t('nodes.connection_point_2', { tag: i.tag })} onClose={onClose}
      footer={<><button className="btn ghost" onClick={onClose}>{t('common.cancel')}</button><button className="btn primary" form="host-form">{t('common.save')}</button></>}>
      <form id="host-form" onSubmit={submit}>
        <Field label={t('nodes.name_in_the_app')} info={t('nodes.point_info')}><input className="input" value={remark} onChange={(e) => setRemark(e.target.value)} placeholder={h?.remark} /></Field>
        <div className="grid2">
          <Field label={t('nodes.address')} help={t('nodes.subscription_address_help')}><input className="input" value={address} onChange={(e) => setAddress(e.target.value)} placeholder={h?.address} /></Field>
          <Field label={t('nodes.port')} help={t('nodes.subscription_port_help')}><input className="input" inputMode="numeric" value={port} onChange={(e) => setPort(e.target.value.replace(/\D/g, ''))} placeholder={String(h?.port ?? '')} /></Field>
          <Field label="SNI"><input className="input" value={sni} onChange={(e) => setSni(e.target.value)} placeholder={h?.sni} /></Field>
          <Field label="Fingerprint"><input className="input" value={fp} onChange={(e) => setFp(e.target.value)} placeholder={h?.fingerprint} /></Field>
        </div>
        <label className="check"><input type="checkbox" checked={hidden} onChange={(e) => setHidden(e.target.checked)} />{' '}{t('nodes.hide_from_subscriptions')}</label>
        {(adv || maskText) && <>
          <Field label="Final Mask" info={t('nodes.streamsettings_finalmask_for')}
            help={mask.error ? <span className="pink-text">{mask.error}</span> : t('nodes.a_json_object_tcp_udp')}>
            <CodeEditor lang="json" value={maskText} onChange={setMaskText} height={140} />
          </Field>
          <button type="button" className="btn ghost sm" onClick={() => setMaskText(pretty(fragmentMask))}>{t('nodes.insert_clienthello_fragmentation')}</button>
        </>}
      </form>
    </Modal>
  )
}

function InboundEditor({ i, onClose, onSaved }: { i: Inbound; onClose: () => void; onSaved: () => void }) {
  const loaded = useLoad(() => get<InboundDetails>(`inbounds/${i.id}`), [i.id])
  const [tag, setTag] = useState(i.tag)
  const [port, setPort] = useState('')
  const [listen, setListen] = useState(0)
  const [egress, setEgress] = useState(0)
  const [values, setValues] = useState<Record<string, string>>({})
  const [overrideText, setOverrideText] = useState('')
  const [overrideObj, setOverrideObj] = useState<Record<string, unknown>>({})
  const [mode, setMode] = useState<'tree' | 'json'>('tree')
  const [regenerate, setRegenerate] = useState(false)
  const [preview, setPreview] = useState<{ inbound?: Record<string, unknown>; error?: string } | null>(null)
  const act = useAction()
  const confirm = useConfirm()
  const adv = useAdvanced()

  useEffect(() => {
    const d = loaded.data
    if (!d) return
    setTag(d.tag)
    setPort(d.port ? String(d.port) : '')
    setListen(d.listenAddressId || 0)
    setEgress(d.egressAddressId || 0)
    setValues(Object.fromEntries(Object.entries(d.values).map(([k, v]) => [k, v == null ? '' : typeof v === 'object' ? JSON.stringify(v) : String(v)])))
    setOverrideText(Object.keys(d.override).length ? pretty(d.override) : '')
    setOverrideObj(d.override || {})
  }, [loaded.data])

  const parsedJson = parseObject(overrideText)
  const overrideValue: Record<string, unknown> = mode === 'json' ? (parsedJson.value ?? {}) : overrideObj
  const overrideError = mode === 'json' ? parsedJson.error : undefined
  const vars = loaded.data?.variables || []
  const body = useMemo(() => {
    const vals: Record<string, string> = {}
    for (const v of vars) if (v.scope === 'node' && v.source !== 'derived' && values[v.name] !== undefined) vals[v.name] = values[v.name]
    return {
      tag, port: Number(port) || 0, values: vals, override: overrideValue, regenerateKeys: regenerate,
      // Адреса задаются целиком: сначала сбросить, потом выставить выбранные ("как у входа" = IP входа).
      clearAddresses: true,
      ...(listen ? { listenAddressId: listen } : {}), ...(egress || listen ? { egressAddressId: egress || listen } : {}),
    }
  }, [tag, port, values, overrideValue, regenerate, listen, egress, vars])

  useEffect(() => {
    if (!loaded.data || overrideError) return
    const t = setTimeout(() => {
      post<{ inbound?: Record<string, unknown>; error?: string }>(`inbounds/${i.id}/preview`, body).then(setPreview).catch((e) => setPreview({ error: e.message }))
    }, 500)
    return () => clearTimeout(t)
  }, [JSON.stringify(body), loaded.data])

  if (!loaded.data) return <Modal title={i.tag} onClose={onClose}><Loading error={loaded.error} /></Modal>
  const d = loaded.data

  const tree = preview?.inbound ?? d.levels?.node ?? d.rendered ?? {}
  const setAt = (path: (string | number)[], v: unknown) => setOverrideObj((o) => writeOverride(o, path, v, tree))
  const resetAt = (path: (string | number)[]) => setOverrideObj((o) => clearOverride(o, path))

  const save = async () => {
    if (regenerate && !(await confirm(t('nodes.new_reality_keys_and_shortid'), t('nodes.generate')))) return
    const r = await act(() => patch(`inbounds/${i.id}`, body), t('nodes.inbound_saved_the_node_applies'))
    if (r !== undefined) {
      onSaved()
      onClose()
    }
  }

  return (
    <Modal title={<><Trans k="nodes.inbound" v={{ tag: d.tag, v: <span className="muted small" style={{ fontWeight: 400 }}><Trans k="nodes.profile" v={{ name: d.profile.name }} /></span> }} /></>} xl={adv} onClose={onClose}
      footer={<>
        {preview?.error ? <span className="pink-text small grow">{t('nodes.there_is_an_error_saving')}</span> : <span className="muted small grow"><Trans k="nodes.common_settings_are_in_the" v={{ name: d.profile.name }} /></span>}
        <button className="btn ghost" onClick={onClose}>{t('common.cancel')}</button>
        <button className="btn primary" disabled={!!overrideError || !!preview?.error} onClick={save}>{t('common.save')}</button>
      </>}>
      <LinkProvider>
      <div className={adv ? 'editor-split' : ''}>
        <div>
          <div className="grid2">
            <Field label={t('common.tag')} link={{ inbound: ['tag'] }}><input className="input mono" value={tag} onChange={(e) => setTag(e.target.value)} /></Field>
            <Field label={t('nodes.port')} help={t('nodes.inbound_port_help')} link={{ inbound: ['port'] }}><input className="input" inputMode="numeric" value={port} onChange={(e) => setPort(e.target.value.replace(/\D/g, ''))} /></Field>
            <Field label={t('nodes.listen_ip')} help={t('nodes.all_on_all_the_server')} link={{ inbound: ['listen'] }}>
              <select className="input" value={listen} onChange={(e) => setListen(Number(e.target.value))}>
                <option value={0}>{t('common.all_addresses')}</option>
                {d.addresses.map((a) => <option key={a.id} value={a.id}>{a.ip}{a.primary ? t('nodes.primary') : ''}</option>)}
              </select>
            </Field>
            <Field label={t('nodes.exit_ip')} info={t('nodes.the_server_address_users_traffic')}>
              <select className="input" value={egress} onChange={(e) => setEgress(Number(e.target.value))}>
                <option value={0}>{listen ? t('nodes.same_as_the_entry') : t('common.default')}</option>
                {d.addresses.map((a) => <option key={a.id} value={a.id}>{a.ip}</option>)}
              </select>
            </Field>
          </div>
          <div className="label" style={{ margin: '6px 0 8px' }}>{t('nodes.this_node_s_parameters')}</div>
          <VariableTable vars={vars.filter((v) => v.scope === 'node')} values={values} onChange={setValues} empty={t('nodes.the_template_has_no_node')}
            link={(name) => ({ inbound: pathsWith(d.inboundSource, name) })} />
          {vars.some((v) => v.scope === 'node' && v.source === 'generate') && (
            <label className="check" style={{ margin: '8px 0 14px' }}>
              <input type="checkbox" checked={regenerate} onChange={(e) => setRegenerate(e.target.checked)} />{' '}{t('nodes.generate_keys_and_other_values')}</label>
          )}
          <div className="label" style={{ margin: '14px 0 6px', display: 'flex', alignItems: 'center', gap: 10 }}>
            {t('nodes.final_inbound_config')}
            <span className="grow" />
            <div className="seg sm">
              <button type="button" className={mode === 'tree' ? 'on' : ''} onClick={() => { setMode('tree') }}>{t('nodes.as_a_tree')}</button>
              <button type="button" className={mode === 'json' ? 'on' : ''} onClick={() => { setOverrideText(Object.keys(overrideValue).length ? pretty(overrideValue) : ''); setMode('json') }}>{t('nodes.as_json')}</button>
            </div>
          </div>
          <div className="muted small" style={{ margin: '0 0 8px' }}>
            <Badge color="green">{t('nodes.from_the_template')}</Badge>{' '}
            <Badge color="pink">{t('nodes.from_the_profile')}</Badge>{' '}
            <Badge color="amber">{t('nodes.from_this_node')}</Badge>
          </div>
          {mode === 'tree' ? (
            <div className="json-tree">
              {Object.keys(tree).length === 0 && <div className="muted small">{preview?.error || t('nodes.nothing_to_show')}</div>}
              <SourceTree
                value={tree}
                sources={d.sources}
                override={overrideObj}
                preview={preview}
                onSet={setAt}
                onReset={resetAt}
              />
            </div>
          ) : (
            <Field label={t('nodes.manual_json_edit_for_this')} help={t('nodes.null_removes_the_field_empty')} info={t('nodes.the_json_is_laid_over')}>
              <CodeEditor lang="json" value={overrideText} onChange={setOverrideText} height={160} />
              {overrideError && <div className="small pink-text" style={{ marginTop: 6 }}>{overrideError}</div>}
            </Field>
          )}
          {!adv && preview?.error && <div className="alert pink">{preview.error}</div>}
        </div>
        {adv && <InboundPreview preview={preview} />}
      </div>
      </LinkProvider>
    </Modal>
  )
}

// The inbound as it lands in the node's Xray config; the lines of the field under the pointer light up.
function InboundPreview({ preview }: { preview: { inbound?: Record<string, unknown>; error?: string } | null }) {
  const active = useActiveLink()
  return (
    <div className="preview-pane">
      <div className="label" style={{ marginBottom: 8 }}>{t('nodes.the_inbound_in_the_node')}</div>
      {!preview ? <Loading /> : preview.error ? <div className="alert pink">{preview.error}</div> :
        <CodeEditor lang="json" value={pretty(preview.inbound)} readOnly highlight={active?.inbound} height="calc(100vh - 300px)" />}
      <div className="muted small" style={{ marginTop: 6 }}>{t('nodes.without_users_the_private_key')}</div>
    </div>
  )
}

// ---------- the final inbound config as a tree with inheritance badges ----------

// A source badge color per level (docs/INBOUNDS.md §1.4: template ⊕ profile ⊕ node).
const SOURCE_COLORS: Record<string, string> = { template: 'green', profile: 'pink', node: 'amber' }
const SOURCE_KEYS: Record<string, Key> = {
  template: 'nodes.from_the_template',
  profile: 'nodes.from_the_profile',
  node: 'nodes.from_this_node',
}

// atPath walks a decoded JSON tree along path.
function atPath(v: unknown, path: (string | number)[]): unknown {
  let cur: unknown = v
  for (const k of path) {
    if (cur == null) return undefined
    cur = (cur as Record<string, unknown>)[k as string]
  }
  return cur
}

// hasOverrideKey reports whether the node's override carries a value at path (a whole-object
// override covers every key below it).
function hasOverrideKey(o: Record<string, unknown>, path: (string | number)[]): boolean {
  let cur: Record<string, unknown> = o
  for (let i = 0; i < path.length; i++) {
    if (cur == null) return false
    const v = cur[path[i] as string]
    if (v === undefined) return false
    if (i === path.length - 1) return true
    if (v == null || typeof v !== 'object' || Array.isArray(v)) return true // a scalar above covers this key
    cur = v as Record<string, unknown>
  }
  return false
}

// toScalar renders a leaf for the input; JSON round-trip keeps arrays compact.
function toScalar(v: unknown): string {
  if (Array.isArray(v)) return JSON.stringify(v)
  return String(v)
}

// fromScalar parses an input value back into the JSON value it names (numbers, bools, null…).
function fromScalar(text: string, cur: unknown): unknown {
  const s = text.trim()
  if (Array.isArray(cur) || (s.startsWith('[') && s.endsWith(']'))) {
    try { return JSON.parse(s) } catch { return text }
  }
  if (s === '') return ''
  if (s === 'true' || s === 'false') return s === 'true'
  if (s === 'null') return null
  if (/^-?\d+(\.\d+)?$/.test(s)) return Number(s)
  return text
}

// writeOverride stores v at path in a copy of the node's override; the value stays a JSON
// string until it parses cleanly ("" clears the key, matching RFC 7386 null).
function writeOverride(o: Record<string, unknown>, path: (string | number)[], v: unknown, full: unknown): Record<string, unknown> {
  const out = structuredCopy(o) as Record<string, unknown>
  let cur: Record<string, unknown> = out
  for (let i = 0; i < path.length - 1; i++) {
    const k = path[i] as string
    if (cur[k] == null || typeof cur[k] !== 'object' || Array.isArray(cur[k])) cur[k] = {}
    cur = cur[k] as Record<string, unknown>
  }
  const key = path[path.length - 1] as string
  if (v === '' || v === null || v === undefined) delete cur[key]
  else if (typeof v === 'string') cur[key] = fromScalar(v, atPath(full, path))
  else cur[key] = v
  return out
}

// clearOverride removes path from a copy of the node's override ("reset to profile/inherit").
function clearOverride(o: Record<string, unknown>, path: (string | number)[]): Record<string, unknown> {
  const out = structuredCopy(o) as Record<string, unknown>
  let cur: Record<string, unknown> = out
  for (let i = 0; i < path.length - 1; i++) {
    const k = path[i] as string
    if (cur[k] == null || typeof cur[k] !== 'object' || Array.isArray(cur[k])) return out
    cur = cur[k] as Record<string, unknown>
  }
  delete cur[path[path.length - 1] as string]
  return out
}

// structuredCopy deep-copies a decoded JSON tree.
function structuredCopy<T>(v: T): T {
  return v == null || typeof v !== 'object' ? v : JSON.parse(JSON.stringify(v)) as T
}

// SourceTree renders the final inbound config: keys with an inheritance badge, overridden
// leaves as inputs (edits go into the node's override), everything else read-only.
function SourceTree({ value, sources, override, preview, onSet, onReset, path = [] }: {
  value: unknown
  sources?: SourceMap
  override: Record<string, unknown>
  preview: { inbound?: Record<string, unknown>; error?: string } | null
  onSet: (path: (string | number)[], v: unknown) => void
  onReset: (path: (string | number)[]) => void
  path?: (string | number)[]
}) {
  if (value == null || typeof value !== 'object' || Array.isArray(value)) {
    return <div className="json-leaf mono">{preview?.error ? '—' : pretty(value)}</div>
  }
  const entries = Object.entries(value as Record<string, unknown>)
  return (
    <div className="json-node">
      {entries.map(([k, v]) => {
        const childPath = [...path, k]
        const label = sources?.[k]
        const childSources = typeof label === 'string' ? undefined : label
        const isOverridden = hasOverrideKey(override, childPath)
        return (
          <div key={k} className="json-row">
            <span className="json-key mono">{k}:</span>
            {v != null && typeof v === 'object' && !Array.isArray(v) ? (
              <SourceTree value={v} sources={childSources} override={override} preview={preview}
                onSet={onSet} onReset={onReset} path={childPath} />
            ) : isOverridden ? (
              <span className="json-edit">
                <input className="input mono sm" value={toScalar(v)} onChange={(e) => onSet(childPath, e.target.value)} />
                <button type="button" className="btn sm ghost" title={t('nodes.reset_to_profile')}
                  onClick={() => onReset(childPath)}>↺</button>
              </span>
            ) : (
              <span className="json-leaf mono" title={k}>{preview?.error ? '—' : pretty(v)}</span>
            )}
            {typeof label === 'string' && <Badge color={SOURCE_COLORS[label] || ''}>{t(SOURCE_KEYS[label] || 'nodes.from_the_template')}</Badge>}
          </div>
        )
      })}
    </div>
  )
}
