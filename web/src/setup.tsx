import { useState } from 'react'
import { get, Node, post } from './api'
import { Icon, useAction, useLoad } from './ui'
import { t } from './i18n'
import { isMigrant, useEngineer } from './mode'

// SetupChecklist — ведущий чеклист на дашборде, пока серверной статистике меньше 1 суток.
// Скрывается кнопкой «Свернуть»; состояние свёртки — в localStorage.
export function SetupChecklist({ showWelcome, usersTotal }: { showWelcome?: boolean; usersTotal?: number }) {
  const standard = useEngineer()
  const [dismissed, setDismissed] = useState(loadDismissed)
  const active = !!showWelcome && !dismissed
  const nodes = useLoad(() => get<Node[]>('nodes'), [], active ? 15000 : 0, { cacheKey: 'nodes', staleTimeMs: 10000, enabled: active })
  const act = useAction()

  if (!active) return null
  if (!nodes.data) return null
  const list = nodes.data
  const hasNode = list.length > 0
  const hasAccess = list.some((n) => n.inbounds.length > 0)
  const hasUser = (usersTotal ?? 0) > 0
  const migrant = isMigrant()

  const steps: Step[] = migrant
    ? [
        { key: 'node', done: hasNode, title: t('setup.step_node'), action: { href: '#/nodes/new', label: t('setup.add_server') } },
        { key: 'transfer', done: hasUser, title: t('setup.step_transfer'), action: { href: '#/users/transfer', label: t('setup.transfer_users') } },
      ]
    : [
        { key: 'node', done: hasNode, title: t('setup.step_node'), action: { href: '#/nodes/new', label: t('setup.add_server') } },
        { key: 'access', done: hasAccess, title: t('setup.step_access'), action: standard ? { href: '#/nodes', label: t('setup.set_up_access') } : { auto: true, label: t('setup.auto_access') } },
        { key: 'user', done: hasUser, title: t('setup.step_user'), action: { href: '#/users/new', label: t('setup.create_user') } },
        { key: 'link', done: linkDone(), title: t('setup.step_link'), action: { href: '#/users', label: t('setup.copy_link') } },
      ]

  const doneAll = steps.every((s) => s.done)

  const runAuto = async () => {
    const target = list.filter((n) => n.enabled && n.domain)
    const body = target.length === 1 ? { nodeId: target[0].id } : {}
    await act(() => post('setup/auto-access', body), t('setup.access_ready'))
    nodes.reload()
  }

  return (
    <div className="card setup-card section-gap">
      <div className="card-head">
        <div>
          <h3>{doneAll ? t('setup.all_done') : t('setup.title')}</h3>
          <div className="card-sub">{migrant ? t('setup.sub_migrant') : t('setup.sub')}</div>
        </div>
        <button className="btn sm ghost" onClick={() => { saveDismissed(); setDismissed(true) }}>{t('setup.collapse')}</button>
      </div>
      <div className="card-pad setup-steps">
        {steps.map((s, i) => (
          <div key={s.key} className={'setup-step' + (s.done ? ' done' : '') + (!s.done && steps.slice(0, i).every((x) => x.done) ? ' current' : '')}>
            <span className="setup-num">{s.done ? <Icon name="check" size={16} /> : i + 1}</span>
            <div className="grow">
              <div>{s.title}</div>
              {!s.done && s.action && (
                s.action.auto
                  ? <button className="btn sm primary" style={{ marginTop: 8 }} onClick={runAuto}><Icon name="check" />{' '}{s.action.label}</button>
                  : <a className="btn sm primary" style={{ marginTop: 8 }} href={s.action.href} onClick={s.key === 'link' ? markLinkDone : undefined}>{s.action.label}</a>
              )}
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}

interface Step {
  key: string
  done: boolean
  title: string
  action?: { href?: string; label: string; auto?: boolean }
}

const dismissedKey = 'vynel.setup.dismissed'
const linkKey = 'vynel.setup.link'

function loadDismissed(): boolean {
  try { return localStorage.getItem(dismissedKey) === '1' } catch { return false }
}
function saveDismissed() {
  try { localStorage.setItem(dismissedKey, '1') } catch { /* приватный режим */ }
}
function linkDone(): boolean {
  try { return localStorage.getItem(linkKey) === '1' } catch { return false }
}
function markLinkDone() {
  try { localStorage.setItem(linkKey, '1') } catch { /* приватный режим */ }
}
