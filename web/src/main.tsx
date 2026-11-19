import { ReactElement, StrictMode, useCallback, useEffect, useState } from 'react'
import { createRoot } from 'react-dom/client'
import './styles.css'
import { get, post, setUnauthorizedHandler, UpdateInfo } from './api'
import { ConfirmProvider, Icon, Logo, Modal, Tabs, ToastProvider, useAction, useLoad } from './ui'
import { initTelegramMiniApp, openExternal } from './telegram'
import { Login } from './pages/Login'
import { Dashboard } from './pages/Dashboard'
import { Insights } from './pages/Insights'
import { Billing } from './pages/Billing'
import { Users } from './pages/Users'
import { Groups } from './pages/Groups'
import { Templates } from './pages/Templates'
import { Nodes } from './pages/Nodes'
import { Profiles } from './pages/Profiles'
import { Xray } from './pages/Xray'
import { Settings } from './pages/Settings'
import { Subscription } from './pages/Subscription'
import { Audit } from './pages/Audit'
import { Telegram } from './pages/Telegram'
import { Api } from './pages/Api'
import { Account } from './pages/Account'
import { languages, lang, setLang, t } from './i18n'
import { Mode, ModeProvider, isMigrant, markModeChosen, modeChosen, modeRank, setMigrant, useEngineer, useMode } from './mode'
import { confirmPageLeave } from './unsaved'

type RouteTarget = { page: string; sub?: string }

// 6 цельных разделов вместо 16 разрозненных пунктов.
type PageDef = { id: string; title: string; short?: string; icon: string; min?: Mode; el: (sub?: string) => ReactElement }

const navPages: PageDef[] = [
  { id: 'overview', title: t('main.dashboard'), icon: 'dashboard', el: (sub) => <OverviewHub initialTab={sub} /> },
  { id: 'users', title: t('common.users'), icon: 'users', el: (sub) => <UsersHub initialTab={sub} /> },
  { id: 'nodes', title: t('common.nodes'), icon: 'nodes', el: (sub) => <NodesHub initialTab={sub} /> },
  { id: 'protocols', title: t('main.protocols'), short: t('main.protocols_short'), icon: 'profiles', min: 'engineer', el: (sub) => <ProtocolsHub initialTab={sub} /> },
  { id: 'subscription', title: t('common.subscription'), icon: 'sub', el: () => <Subscription /> },
  { id: 'system', title: t('main.system'), icon: 'settings', el: (sub) => <SystemHub initialTab={sub} /> },
]

// Совместимость со всеми старыми ссылками (#/groups, #/billing, #/xray и т.д.).
const legacyMap: Record<string, RouteTarget> = {
  insights: { page: 'overview', sub: 'analytics' },
  groups: { page: 'users', sub: 'groups' },
  templates: { page: 'users', sub: 'tariffs' },
  billing: { page: 'nodes', sub: 'billing' },
  profiles: { page: 'protocols', sub: 'profiles' },
  xray: { page: 'protocols', sub: 'xray' },
  externals: { page: 'subscription', sub: 'externals' },
  settings: { page: 'system', sub: 'settings' },
  telegram: { page: 'system', sub: 'telegram' },
  api: { page: 'system', sub: 'api' },
  audit: { page: 'system', sub: 'audit' },
  account: { page: 'system', sub: 'account' },
}

function parseRoute(): RouteTarget {
  const parts = location.hash.replace(/^#\/?/, '').split('/')
  const id = parts[0] || 'overview'
  const sub = parts[1]
  if (legacyMap[id]) return legacyMap[id]
  if (navPages.some((p) => p.id === id)) return { page: id, sub }
  return { page: 'overview' }
}

function OverviewHub({ initialTab }: { initialTab?: string }) {
  const eng = useEngineer()
  const [tab, setTab] = useState(initialTab === 'analytics' && eng ? 'analytics' : 'summary')
  useEffect(() => {
    if (initialTab === 'analytics' && eng) setTab('analytics')
  }, [initialTab, eng])
  if (!eng) return <Dashboard />
  return (
    <>
      <Tabs
        value={tab}
        onChange={setTab}
        items={[
          { id: 'summary', label: t('main.overview') },
          { id: 'analytics', label: t('main.analytics') },
        ]}
      />
      {tab === 'summary' ? <Dashboard /> : <Insights />}
    </>
  )
}

function UsersHub({ initialTab }: { initialTab?: string }) {
  const [tab, setTab] = useState(initialTab === 'groups' || initialTab === 'tariffs' ? initialTab : 'list')
  useEffect(() => {
    if (initialTab === 'groups' || initialTab === 'tariffs') setTab(initialTab)
  }, [initialTab])
  return (
    <>
      <Tabs
        value={tab}
        onChange={setTab}
        items={[
          { id: 'list', label: t('common.users') },
          { id: 'groups', label: t('common.groups') },
          { id: 'tariffs', label: t('main.tariffs') },
        ]}
      />
      {tab === 'list' && <Users />}
      {tab === 'groups' && <Groups />}
      {tab === 'tariffs' && <Templates />}
    </>
  )
}

function NodesHub({ initialTab }: { initialTab?: string }) {
  const [tab, setTab] = useState(initialTab === 'billing' ? 'billing' : 'servers')
  useEffect(() => {
    if (initialTab === 'billing') setTab('billing')
  }, [initialTab])
  return (
    <>
      <Tabs
        value={tab}
        onChange={setTab}
        items={[
          { id: 'servers', label: t('common.nodes') },
          { id: 'billing', label: t('main.billing') },
        ]}
      />
      {tab === 'servers' ? <Nodes /> : <Billing />}
    </>
  )
}

function ProtocolsHub({ initialTab }: { initialTab?: string }) {
  const [tab, setTab] = useState(initialTab === 'xray' ? 'xray' : 'profiles')
  useEffect(() => {
    if (initialTab === 'xray' || initialTab === 'profiles') setTab(initialTab)
  }, [initialTab])
  return (
    <>
      <Tabs
        value={tab}
        onChange={setTab}
        items={[
          { id: 'profiles', label: t('common.profiles') },
          { id: 'xray', label: 'Xray' },
        ]}
      />
      {tab === 'profiles' ? <Profiles /> : <Xray />}
    </>
  )
}

function SystemHub({ initialTab }: { initialTab?: string }) {
  const eng = useEngineer()
  const valid = new Set(['settings', 'telegram', 'account', ...(eng ? ['api', 'audit'] : [])])
  const [tab, setTab] = useState(initialTab && valid.has(initialTab) ? initialTab : 'settings')
  useEffect(() => {
    if (initialTab && valid.has(initialTab)) setTab(initialTab)
  }, [initialTab, eng])
  const items = [
    { id: 'settings', label: t('common.settings') },
    { id: 'telegram', label: 'Telegram' },
    ...(eng ? [
      { id: 'api', label: t('integrations.title') },
      { id: 'audit', label: t('main.log') },
    ] : []),
    { id: 'account', label: t('main.account') },
  ]
  return (
    <>
      <Tabs value={tab} onChange={setTab} items={items} />
      {tab === 'settings' && <Settings />}
      {tab === 'telegram' && <Telegram />}
      {tab === 'api' && eng && <Api />}
      {tab === 'audit' && eng && <Audit />}
      {tab === 'account' && <Account />}
    </>
  )
}

function load(key: string, def: string): string {
  try { return localStorage.getItem(key) ?? def } catch { return def }
}
function save(key: string, v: string) {
  try { localStorage.setItem(key, v) } catch { /* приватный режим */ }
}

function Shell({ login, version, onLogout, inTelegram }: { login: string; version: string; onLogout: () => void; inTelegram?: boolean }) {
  const [route, setRoute] = useState(parseRoute)
  const [collapsed, setCollapsed] = useState(() => load('vynel.sidebar', 'open') === 'collapsed')
  const [mobileOpen, setMobileOpen] = useState(false)
  const [scrolled, setScrolled] = useState(false)
  const updates = useLoad(() => get<UpdateInfo>('updates'), [], 300000)

  useEffect(() => {
    let acted = false
    const mark = () => { acted = true }
    const kinds = ['pointerdown', 'keydown', 'wheel', 'touchstart']
    kinds.forEach((k) => window.addEventListener(k, mark, { capture: true, passive: true }))
    const ping = () => {
      const active = acted
      acted = false
      post('session/ping', { active }).catch(() => {})
    }
    const timer = setInterval(ping, 30000)
    const closing = () => {
      fetch('api/session/closing', { method: 'POST', keepalive: true, credentials: 'same-origin', headers: { 'X-Vynel': '1' } }).catch(() => {})
    }
    const back = (e: PageTransitionEvent) => { if (e.persisted) ping() }
    window.addEventListener('pagehide', closing)
    window.addEventListener('pageshow', back)
    return () => {
      kinds.forEach((k) => window.removeEventListener(k, mark, { capture: true }))
      clearInterval(timer)
      window.removeEventListener('pagehide', closing)
      window.removeEventListener('pageshow', back)
    }
  }, [])

  useEffect(() => {
    const h = () => setScrolled(window.scrollY > 4)
    h()
    window.addEventListener('scroll', h, { passive: true })
    return () => window.removeEventListener('scroll', h)
  }, [])

  useEffect(() => {
    const h = (event: HashChangeEvent) => {
      const next = parseRoute()
      if (next.page !== route.page && !confirmPageLeave()) {
        history.replaceState(null, '', event.oldURL)
        return
      }
      setRoute(next)
      setMobileOpen(false)
    }
    window.addEventListener('hashchange', h)
    return () => window.removeEventListener('hashchange', h)
  }, [route.page])

  const { mode, setMode } = useMode()
  const p = navPages.find((x) => x.id === route.page) ?? navPages[0]
  useEffect(() => {
    document.title = `${p.title} · vynel`
  }, [p])

  const rankOf = modeRank[mode]
  const visible = (x: PageDef) => x.id === route.page || rankOf >= modeRank[x.min ?? 'basic']

  const toggle = () => {
    setCollapsed((c) => {
      save('vynel.sidebar', c ? 'open' : 'collapsed')
      return !c
    })
  }

  return (
    <div className={'app' + (collapsed ? ' collapsed' : '') + (mobileOpen ? ' mobile-open' : '')}>
      <aside className="sidebar">
        <div className="brand">
          <Logo size={32} />
          <div className="brand-name">vyn<span>el</span></div>
        </div>
        <nav className="nav">
          {navPages.filter(visible).map((x) => (
            <a key={x.id} href={'#/' + x.id} className={'nav-item' + (x.id === route.page ? ' active' : '')} title={x.title}>
              <Icon name={x.icon} />
              <span className="nav-label">{x.title}</span>
              <span className="nav-short">{x.short || x.title}</span>
              {x.id === 'nodes' && (updates.data?.count || 0) > 0 && (
                <span className="nav-badge" title={t('main.update_available')}>{updates.data!.count}</span>
              )}
            </a>
          ))}
        </nav>
        <div className="side-foot">
          <div className="side-controls">
            <div className="side-mode" role="group" aria-label={t('main.mode_basic')}>
              <button type="button" className={mode === 'basic' ? 'on' : ''} title={t('main.mode_basic_info')} onClick={() => setMode('basic')}>
                {t('main.mode_basic')}
              </button>
              <button type="button" className={mode === 'engineer' ? 'on' : ''} title={t('main.mode_engineer_info')} onClick={() => setMode('engineer')}>
                {t('main.mode_engineer')}
              </button>
            </div>
            <div className="side-meta">
              <span className="side-foot-text" title={`${login} · ${version}`}>{login} · {version}</span>
              <div className="side-lang" title={t('main.language')}>
                {languages.map((l) => (
                  <button key={l.code} type="button" className={lang() === l.code ? 'on' : ''} title={l.name}
                    onClick={() => { if (lang() !== l.code) setLang(l.code).then(() => location.reload()) }}>{l.short}</button>
                ))}
              </div>
            </div>
          </div>
          <div className="side-actions">
            <button className="nav-item" onClick={() => { if (confirmPageLeave()) onLogout() }} title={t('main.log_out')}>
              <Icon name="logout" />
              <span className="nav-label">{t('main.log_out')}</span>
              <span className="nav-short">{t('main.log_out')}</span>
            </button>
            <button className="nav-item collapse-btn" style={{ width: collapsed ? '100%' : 36, padding: 0, justifyContent: 'center' }}
              onClick={toggle} title={collapsed ? t('main.expand_menu') : t('main.collapse_menu')}>
              <Icon name={collapsed ? 'expand' : 'collapse'} />
            </button>
          </div>
        </div>
      </aside>
      <div className="scrim" onClick={() => setMobileOpen(false)} />
      <div className="main">
        <header className={'topbar' + (scrolled ? ' scrolled' : '')}>
          <button className="btn ghost icon burger" onClick={() => setMobileOpen(true)} aria-label={t('main.menu')}><Icon name="menu" /></button>
          <h1>{p.title}</h1>
          <div className="spacer" />
          {inTelegram && <BrowserButton />}
        </header>
        <main className="content" key={route.page}>{p.el(route.sub)}</main>
      </div>
    </div>
  )
}

function BrowserButton() {
  const [busy, setBusy] = useState(false)
  const act = useAction()
  return (
    <button className="btn sm" disabled={busy} title={t('main.open_the_panel_in_a')} onClick={async () => {
      setBusy(true)
      const r = await act(() => post<{ url: string }>('login/browser-link'))
      setBusy(false)
      if (r?.url) openExternal(r.url)
    }}><Icon name="external" size={16} /> <span className="hide-sm">{t('main.in_browser')}</span><span className="show-sm">{t('main.browser')}</span></button>
  )
}

// Onboarding — одноразовый выбор из двух честных режимов при первом входе.
function Onboarding({ onDone }: { onDone: () => void }) {
  const { setMode } = useMode()
  const [migrant, setMigrantFlag] = useState(isMigrant())
  const pick = (m: Mode) => {
    setMode(m)
    setMigrant(migrant)
    markModeChosen()
    onDone()
  }
  const card = (m: Mode, icon: string, title: string, text: string) => (
    <button key={m} className="onboard-card" onClick={() => pick(m)}>
      <span className="onboard-icon"><Icon name={icon} size={22} /></span>
      <b>{title}</b>
      <span className="small text-2">{text}</span>
    </button>
  )
  return (
    <Modal title={t('main.onboarding_title')} onClose={() => pick('basic')}>
      <p className="text-2" style={{ marginTop: 0 }}>{t('main.onboarding_intro')}</p>
      <div className="onboard-cards">
        {card('basic', 'dashboard', t('main.mode_basic'), t('main.onboarding_basic'))}
        {card('engineer', 'code', t('main.mode_engineer'), t('main.onboarding_engineer'))}
      </div>
      <label className="check" style={{ marginTop: 16 }}>
        <input type="checkbox" checked={migrant} onChange={(e) => setMigrantFlag(e.target.checked)} />
        {' '}{t('main.onboarding_migrant')}
      </label>
    </Modal>
  )
}

function App() {
  const [session, setSession] = useState<{ login: string; version: string; method?: string; miniAppFullscreen?: boolean } | null | undefined>(undefined)
  const [magicError, setMagicError] = useState('')
  const [onboarding, setOnboarding] = useState(() => !modeChosen())
  const check = useCallback(() => {
    get('session').then(setSession, () => setSession(null))
  }, [])
  useEffect(() => {
    if (session?.method === 'telegram') {
      initTelegramMiniApp(!!session.miniAppFullscreen)
    }
  }, [session?.method, session?.miniAppFullscreen])
  useEffect(() => {
    setUnauthorizedHandler((message) => {
      if (message.startsWith(t('main.session'))) setMagicError(message)
      setSession(null)
    })
    const m = location.hash.match(/^#\/magic\/([A-Za-z0-9_-]+)/)
    if (m) {
      history.replaceState(null, '', location.pathname + '#/overview')
      post('login/magic', { token: m[1] }).then(check, (e) => {
        setMagicError(e.message)
        check()
      })
      return
    }
    const tg = location.hash.includes('tgWebAppData=') ? new URLSearchParams(location.hash.slice(1)).get('tgWebAppData') : null
    if (tg) {
      history.replaceState(null, '', location.pathname + '#/overview')
      get('session').then(setSession, () =>
        post('login/telegram', { initData: tg }).then(check, (e) => {
          setMagicError(e.message)
          check()
        }))
      return
    }
    check()
  }, [check])

  if (session === undefined) return null
  if (session === null) return <Login onLogin={() => { setMagicError(''); check() }} error={magicError} />
  return (
    <>
      <Shell login={session.login} version={session.version} inTelegram={session.method === 'telegram'}
        onLogout={() => post('logout').finally(() => setSession(null))} />
      {onboarding && <Onboarding onDone={() => setOnboarding(false)} />}
    </>
  )
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ToastProvider>
      <ConfirmProvider>
        <ModeProvider>
          <App />
        </ModeProvider>
      </ConfirmProvider>
    </ToastProvider>
  </StrictMode>,
)
