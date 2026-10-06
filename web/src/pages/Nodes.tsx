import { FormEvent, useState } from 'react'
import { api, del, get, Inbound, JoinInfo, Node, patch, post, Profile, ProfileTemplate } from '../api'
import { ago, bps, bytes, nodeStateInfo, uptime } from '../format'
import { Badge, Bar, CopyField, Empty, Field, Icon, Loading, Modal, Switch, useAction, useConfirm, useLoad } from '../ui'

export function Nodes() {
  const nodes = useLoad(() => get<Node[]>('nodes'), [], 10000)
  const profiles = useLoad(() => get<Profile[]>('profiles'))
  const [adding, setAdding] = useState(false)
  const [join, setJoin] = useState<JoinInfo | null>(null)
  const [editNode, setEditNode] = useState<Node | null>(null)
  const [attachTo, setAttachTo] = useState<Node | null>(null)
  const [hostOf, setHostOf] = useState<Inbound | null>(null)
  const [configOf, setConfigOf] = useState<Inbound | null>(null)
  const act = useAction()
  const confirm = useConfirm()
  const reload = nodes.reload

  return (
    <>
      <div className="toolbar">
        <div className="text-2 grow small">Ноды — серверы с Xray. На каждой свой VLESS-инбаунд со своими ключами; общие настройки берутся из профиля.</div>
        <button className="btn primary" onClick={() => setAdding(true)}><Icon name="plus" /> Нода</button>
      </div>

      {!nodes.data ? <Loading error={nodes.error} /> : nodes.data.length === 0 ? <div className="card"><Empty icon="🛰">Нод нет</Empty></div> : (
        <div className="cards">
          {nodes.data.map((n) => {
            const st = nodeStateInfo[n.state] || { label: n.state, color: '' }
            const m = n.metrics
            return (
              <div key={n.id} className="card">
                <div className="card-head">
                  <div className="node-head">
                    <span className="flag">{n.flag || '🌐'}</span>
                    <div>
                      <h3>{n.name} <span className="muted small">{n.code}</span> {n.local && <Badge>на сервере панели</Badge>}</h3>
                      <div className="card-sub">{n.domain || 'без домена'} · связь {ago(n.lastSeenAt)}</div>
                    </div>
                  </div>
                  <div className="row" style={{ gap: 6 }}>
                    <Badge color={st.color} dot>{st.label}</Badge>
                    <Switch checked={n.enabled} onChange={(v) => act(() => patch(`nodes/${n.id}`, { enabled: v }), v ? 'Нода включена' : 'Нода выключена').then(reload)} />
                    <button className="btn ghost icon sm" title="Изменить" onClick={() => setEditNode(n)}><Icon name="edit" /></button>
                    {!n.local && <button className="btn ghost icon sm" title="Новый токен подключения" onClick={async () => {
                      if (await confirm('Выпустить новый токен? Текущий сертификат ноды будет отозван — ноду нужно будет подключить заново с новым токеном.', 'Выпустить')) {
                        const j = await act(() => post<JoinInfo>(`nodes/${n.id}/token`))
                        if (j) { setJoin(j); reload() }
                      }
                    }}><Icon name="key" /></button>}
                    {!n.local && <button className="btn ghost icon sm" title="Удалить" onClick={async () => {
                      if (await confirm(<>Удалить ноду <b>{n.name}</b>? Её инбаунды пропадут из подписок.</>, 'Удалить'))
                        act(() => del(`nodes/${n.id}`), 'Нода удалена').then(reload)
                    }}><Icon name="trash" /></button>}
                  </div>
                </div>
                <div className="card-pad">
                  {n.problems.length > 0 && (
                    <div className="alert pink" style={{ marginBottom: 14 }}>
                      <ul className="list-plain" style={{ color: 'inherit' }}>{n.problems.map((p, i) => <li key={i}>{p}</li>)}</ul>
                    </div>
                  )}
                  {n.state === 'pending' && <div className="alert amber" style={{ marginBottom: 14 }}>Нода ещё не подключалась. Запустите её на сервере с токеном (кнопка с ключом выпустит новый).</div>}
                  <div className="cards" style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(260px, 1fr))' }}>
                    <div>
                      {m ? (
                        <>
                          <div className="meter"><span>CPU</span><Bar value={m.cpu} max={100} /><span>{m.cpu.toFixed(0)}%</span></div>
                          <div className="meter"><span>RAM</span><Bar value={m.memUsed} max={m.memTotal} /><span>{m.memTotal ? Math.round((100 * m.memUsed) / m.memTotal) : 0}%</span></div>
                          <div className="kv small" style={{ marginTop: 10, gridTemplateColumns: '110px 1fr' }}>
                            <div className="k">Онлайн</div><div>{m.online}</div>
                            <div className="k">Сеть</div><div>↓ {bps(m.rxBps)} · ↑ {bps(m.txBps)}</div>
                            <div className="k">Сегодня</div><div>{bytes(n.todayBytes)}</div>
                            <div className="k">Аптайм</div><div>{uptime(m.uptime)}</div>
                          </div>
                        </>
                      ) : <div className="muted small">Метрик пока нет</div>}
                      <div className="muted small" style={{ marginTop: 10 }}>
                        Xray {n.xrayVersion || '—'} · агент {n.agentVersion || '—'}{n.caddyVersion && ` · Caddy ${n.caddyVersion}`}
                      </div>
                      {n.addresses.length > 0 && (
                        <div className="row small" style={{ marginTop: 8, gap: 6 }}>
                          {n.addresses.map((a) => <span key={a.id} className="chip static">{a.ip}{a.primary && ' ★'}{!a.onInterface && ' (нет на интерфейсе)'}</span>)}
                        </div>
                      )}
                    </div>
                    <div>
                      <div className="row between" style={{ marginBottom: 8 }}>
                        <span className="label">Инбаунды</span>
                        <button className="btn sm" onClick={() => setAttachTo(n)}><Icon name="plus" /> Профиль</button>
                      </div>
                      {n.inbounds.length === 0 && <div className="muted small">Нет — на ноде нечего подключать. Добавьте профиль.</div>}
                      {n.inbounds.map((i) => (
                        <div key={i.id} className="inb">
                          <div className="row between">
                            <div>
                              <b className="mono">{i.tag}</b> <span className="muted small">· {i.profileName}</span>
                              <div className="small text-2">{i.listen}:{i.port}{i.host && ` → ${i.host.address}:${i.host.port} · ${i.host.network}/${i.host.security}`}</div>
                              {i.host && <div className="small muted">«{i.host.remark}»{i.host.sni && ` · SNI ${i.host.sni}`}{i.host.hidden && ' · скрыт из подписок'}</div>}
                              {i.error && <div className="small pink-text">{i.error}</div>}
                            </div>
                            <Switch checked={i.enabled} onChange={(v) => act(() => patch(`inbounds/${i.id}`, { enabled: v }), v ? 'Инбаунд включён' : 'Инбаунд выключен').then(reload)} />
                          </div>
                          <div className="row" style={{ marginTop: 8, gap: 6 }}>
                            <button className="btn sm" onClick={() => setHostOf(i)}><Icon name="edit" /> Точка подключения</button>
                            <button className="btn sm" onClick={() => setConfigOf(i)}><Icon name="code" /> Конфиг</button>
                            <button className="btn sm" onClick={async () => {
                              if (await confirm('Сгенерировать новые ключи Reality и shortId? Клиенты получат их при следующем обновлении подписки, до этого не подключатся.', 'Сгенерировать'))
                                act(() => patch(`inbounds/${i.id}`, { regenerateKeys: true }), 'Ключи обновлены').then(reload)
                            }}><Icon name="key" /> Новые ключи</button>
                            <button className="btn sm danger" onClick={async () => {
                              if (await confirm(<>Убрать инбаунд <b>{i.tag}</b> с ноды?</>, 'Убрать'))
                                act(() => del(`inbounds/${i.id}`), 'Инбаунд убран').then(reload)
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

      {adding && <AddNode profiles={profiles.data || []} onClose={() => setAdding(false)} onAdded={(j) => { setAdding(false); setJoin(j); reload() }} />}
      {join && <JoinModal j={join} onClose={() => setJoin(null)} />}
      {editNode && <EditNode n={editNode} onClose={() => setEditNode(null)} onSaved={() => { setEditNode(null); reload() }} />}
      {attachTo && <Attach node={attachTo} profiles={profiles.data || []} onClose={() => setAttachTo(null)} onDone={() => { setAttachTo(null); reload() }} />}
      {hostOf && <HostForm i={hostOf} onClose={() => setHostOf(null)} onSaved={() => { setHostOf(null); reload() }} />}
      {configOf && <ConfigModal i={configOf} onClose={() => setConfigOf(null)} />}
    </>
  )
}

function AddNode({ profiles, onClose, onAdded }: { profiles: Profile[]; onClose: () => void; onAdded: (j: JoinInfo) => void }) {
  const [name, setName] = useState('')
  const [country, setCountry] = useState('')
  const [domain, setDomain] = useState('')
  const [profileIds, setProfileIds] = useState<number[]>(profiles.filter((p) => p.templateId === 'vless-reality-selfsteal').map((p) => p.id))
  const act = useAction()
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const j = await act(() => post<JoinInfo>('nodes', { name, country, domain, profileIds }), 'Нода создана')
    if (j) onAdded(j)
  }
  return (
    <Modal title="Новая нода" onClose={onClose}
      footer={<><button className="btn ghost" onClick={onClose}>Отмена</button><button className="btn primary" form="add-node" disabled={!name.trim()}>Создать и получить токен</button></>}>
      <form id="add-node" onSubmit={submit}>
        <div className="grid2">
          <Field label="Название (видно в приложениях)"><input className="input" autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="Германия" /></Field>
          <Field label="Код страны" help="Для флага: DE, NL, FI…"><input className="input" maxLength={2} value={country} onChange={(e) => setCountry(e.target.value.toUpperCase())} placeholder="DE" /></Field>
        </div>
        <Field label="Домен ноды" help="A-запись на IP нового сервера (серое облако в Cloudflare). Нужен для Reality self-steal.">
          <input className="input" value={domain} onChange={(e) => setDomain(e.target.value)} placeholder="de.example.com" />
        </Field>
        <Field label="Сразу добавить профили">
          <div className="row">
            {profiles.map((p) => (
              <label key={p.id} className="check">
                <input type="checkbox" checked={profileIds.includes(p.id)} onChange={(e) => setProfileIds(e.target.checked ? [...profileIds, p.id] : profileIds.filter((x) => x !== p.id))} />
                {p.name}
              </label>
            ))}
          </div>
        </Field>
      </form>
    </Modal>
  )
}

function JoinModal({ j, onClose }: { j: JoinInfo; onClose: () => void }) {
  return (
    <Modal title={`Подключение ноды ${j.code}`} onClose={onClose} wide footer={<button className="btn primary" onClick={onClose}>Готово</button>}>
      {j.warnings && j.warnings.length > 0 && <div className="alert amber" style={{ marginBottom: 14 }}>{j.warnings.join('; ')}</div>}
      {j.error ? <div className="alert pink">{j.error}</div> : (
        <>
          <p className="text-2" style={{ marginTop: 0 }}>
            На новом сервере (Ubuntu/Debian, root, свободные порты 80 и 443) установите vynel, Xray и Caddy — по инструкции из
            <b> docs/USER_GUIDE.md, раздел «Ещё один сервер»</b> — и запустите ноду этой командой:
          </p>
          <CopyField value={`systemd-run --unit vynel-node ${j.command}`} wrap />
          <p className="muted small">Токен одноразовый и действует {j.ttl}. На панели должен быть открыт порт 9443. Нода появится здесь со статусом «работает».</p>
        </>
      )}
    </Modal>
  )
}

function EditNode({ n, onClose, onSaved }: { n: Node; onClose: () => void; onSaved: () => void }) {
  const [name, setName] = useState(n.name)
  const [country, setCountry] = useState(n.country)
  const [domain, setDomain] = useState(n.domain)
  const act = useAction()
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const r = await act(() => patch(`nodes/${n.id}`, { name, country, domain }), 'Сохранено')
    if (r !== undefined) onSaved()
  }
  return (
    <Modal title={`Нода ${n.code}`} onClose={onClose}
      footer={<><button className="btn ghost" onClick={onClose}>Отмена</button><button className="btn primary" form="edit-node">Сохранить</button></>}>
      <form id="edit-node" onSubmit={submit}>
        <div className="grid2">
          <Field label="Название"><input className="input" value={name} onChange={(e) => setName(e.target.value)} /></Field>
          <Field label="Код страны"><input className="input" maxLength={2} value={country} onChange={(e) => setCountry(e.target.value.toUpperCase())} /></Field>
        </div>
        <Field label="Домен" help="Смена домена меняет SNI и сертификат — клиенты получат новые настройки с подпиской">
          <input className="input" value={domain} onChange={(e) => setDomain(e.target.value)} />
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
    const r = await act(() => post('inbounds', { nodeId: node.id, profileId, port: Number(port) || 0, values }), 'Профиль добавлен на ноду')
    if (r !== undefined) onDone()
  }
  return (
    <Modal title={`Профиль на ноду ${node.code}`} onClose={onClose}
      footer={<><button className="btn ghost" onClick={onClose}>Отмена</button><button className="btn primary" form="attach" disabled={!profileId}>Добавить</button></>}>
      {profiles.length === 0 ? <div className="muted">Сначала создайте профиль в разделе «Профили».</div> : (
        <form id="attach" onSubmit={submit}>
          <Field label="Профиль">
            <select className="input" value={profileId} onChange={(e) => { setProfileId(Number(e.target.value)); setValues({}) }}>
              {profiles.map((x) => <option key={x.id} value={x.id}>{x.name} — {x.templateTitle}</option>)}
            </select>
          </Field>
          {inputs.map((v) => (
            <Field key={v.name} label={<>{v.name}{v.optional ? '' : <span className="pink-text"> *</span>}</>} help={v.description}>
              <input className="input" value={values[v.name] || ''} onChange={(e) => setValues({ ...values, [v.name]: e.target.value })}
                placeholder={v.default != null ? String(v.default) : v.default_from ? `по умолчанию из ${v.default_from}` : ''} />
            </Field>
          ))}
          <Field label="Порт" help="Пусто — из профиля"><input className="input" inputMode="numeric" value={port} onChange={(e) => setPort(e.target.value.replace(/\D/g, ''))} /></Field>
          <div className="muted small">Тег, ключи Reality и shortId создадутся автоматически.</div>
        </form>
      )}
    </Modal>
  )
}

function HostForm({ i, onClose, onSaved }: { i: Inbound; onClose: () => void; onSaved: () => void }) {
  const o = i.hostOverride
  const [remark, setRemark] = useState(String(o.remark ?? ''))
  const [address, setAddress] = useState(String(o.address ?? ''))
  const [port, setPort] = useState(o.port != null ? String(o.port) : '')
  const [sni, setSni] = useState(String(o.sni ?? ''))
  const [fp, setFp] = useState(String(o.fingerprint ?? ''))
  const [hidden, setHidden] = useState(!!o.hidden)
  const act = useAction()
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const v = (s: string) => (s.trim() === '' ? null : s.trim())
    const host = { remark: v(remark), address: v(address), port: port ? Number(port) : null, sni: v(sni), fingerprint: v(fp), hidden: hidden || null }
    const r = await act(() => patch(`inbounds/${i.id}`, { host }), 'Точка подключения сохранена')
    if (r !== undefined) onSaved()
  }
  const h = i.host
  return (
    <Modal title={`Точка подключения ${i.tag}`} onClose={onClose}
      footer={<><button className="btn ghost" onClick={onClose}>Отмена</button><button className="btn primary" form="host-form">Сохранить</button></>}>
      <form id="host-form" onSubmit={submit}>
        <p className="muted small" style={{ marginTop: 0 }}>Так сервер выглядит в подписке. Пустые поля заполняются автоматически (показаны серым).</p>
        <Field label="Название в приложении"><input className="input" value={remark} onChange={(e) => setRemark(e.target.value)} placeholder={h?.remark} /></Field>
        <div className="grid2">
          <Field label="Адрес"><input className="input" value={address} onChange={(e) => setAddress(e.target.value)} placeholder={h?.address} /></Field>
          <Field label="Порт"><input className="input" inputMode="numeric" value={port} onChange={(e) => setPort(e.target.value.replace(/\D/g, ''))} placeholder={String(h?.port ?? '')} /></Field>
          <Field label="SNI"><input className="input" value={sni} onChange={(e) => setSni(e.target.value)} placeholder={h?.sni} /></Field>
          <Field label="Fingerprint"><input className="input" value={fp} onChange={(e) => setFp(e.target.value)} placeholder={h?.fingerprint} /></Field>
        </div>
        <label className="check"><input type="checkbox" checked={hidden} onChange={(e) => setHidden(e.target.checked)} /> Скрыть из подписок</label>
      </form>
    </Modal>
  )
}

function ConfigModal({ i, onClose }: { i: Inbound; onClose: () => void }) {
  const { data, error } = useLoad(() => api<any>('GET', `inbounds/${i.id}/config`), [i.id])
  return (
    <Modal title={`Конфиг ${i.tag}`} wide onClose={onClose}>
      {!data ? <Loading error={error} /> : <pre className="code">{JSON.stringify(data, null, 2)}</pre>}
      <div className="muted small" style={{ marginTop: 8 }}>Так инбаунд выглядит в конфиге Xray на ноде (без пользователей; приватный ключ скрыт).</div>
    </Modal>
  )
}
