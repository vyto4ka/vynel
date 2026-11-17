import { ReactElement, StrictMode, useCallback, useEffect, useState } from 'react'
import { createRoot } from 'react-dom/client'
import './styles.css'
import { get, post, setUnauthorizedHandler, UpdateInfo } from './api'
import { ConfirmProvider, Drawer, Icon, Logo, Modal, ToastProvider, useAction, useLoad } from './ui'
import { openExternal } from './telegram'
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
import { Externals } from './pages/Externals'
import { Audit } from './pages/Audit'
import { Telegram } from './pages/Telegram'
import { Api } from './pages/Api'
import { Account } from './pages/Account'
import { languages, lang, setLang, t } from './i18n'
import { Mode, ModeProvider, isMigrant, markModeChosen, modeChosen, modeRank, setMigrant, useMode } from './mode'
import { confirmPageLeave } from './unsaved'

// short is the label under the icon when the menu is collapsed into a rail.
// min — минимальный режим видимости: страница показывается «от уровня min и выше».
type Page = { id: string; title: string; short?: string; icon: string; min?: Mode; el: () => ReactElement }

const sections: { title: string; pages: Page[] }[] = [
  { title: t('main.overview'), pages: [
    { id: 'overview', title: t('main.dashboard'), icon: 'dashboard', el: () => <Dashboard /> },
    { id: 'insights', title: t('main.analytics'), icon: 'chart', min: 'standard', el: () => <Insights /> },
  ] },
  {
    title: t('main.access'),
    pages: [
      { id: 'users', title: t('common.users'), icon: 'users', el: () => <Users /> },
      { id: 'groups', title: t('common.groups'), icon: 'groups', min: 'standard', el: () => <Groups /> },
      { id: 'templates', title: t('main.user_templates'), short: t('main.templates'), icon: 'templates', min: 'standard', el: () => <Templates /> },
      { id: 'externals', title: t('main.external_subscriptions'), short: t('main.external'), icon: 'sub', min: 'standard', el: () => <Externals /> },
    ],
  },
  {
    title: t('main.infrastructure'),
    pages: [
      { id: 'nodes', title: t('common.nodes'), icon: 'nodes', el: () => <Nodes /> },
      { id: 'profiles', title: t('common.profiles'), icon: 'profiles', min: 'standard', el: () => <Profiles /> },
      { id: 'xray', title: 'Xray', icon: 'xray', min: 'pro', el: () => <Xray /> },
      { id: 'billing', title: t('main.billing'), icon: 'wallet', min: 'standard', el: () => <Billing /> },
    ],
  },
  { title: t('common.subscription'), pages: [
    { id: 'subscription', title: t('common.subscription'), icon: 'sub', el: () => <Subscription /> },
  ] },
  {
    title: t('main.system'),
    pages: [
      { id: 'settings', title: t('common.settings'), icon: 'settings', el: () => <Settings /> },
      { id: 'telegram', title: 'Telegram', icon: 'telegram', el: () => <Telegram /> },
      { id: 'api', title: t('integrations.title'), icon: 'plug', min: 'pro', el: () => <Api /> },
      { id: 'audit', title: t('main.log'), icon: 'audit', min: 'pro', el: () => <Audit /> },
      { id: 'account', title: t('main.account'), icon: 'account', el: () => <Account /> },
    ],
  },
]
const allPages = sections.flatMap((s) => s.pages)

function currentPage(): string {
  const id = location.hash.replace(/^#\/?/, '').split('/')[0]
  return allPages.some((p) => p.id === id) ? id : 'overview' // #/stats — вкладка дашборда
}

function load(key: string, def: string): string {
  try {
    return localStorage.getItem(key) ?? def
  } catch {
    return def
  }
}
function save(key: string, v: string) {
  try {
    localStorage.setItem(key, v)
  } catch {
    /* приватный режим */
  }
}

function Shell({ login, version, onLogout, inTelegram }: { login: string; version: string; onLogout: () => void; inTelegram?: boolean }) {
  const [page, setPage] = useState(currentPage)
  const [collapsed, setCollapsed] = useState(() => load('vynel.sidebar', 'open') === 'collapsed')
  const [mobileOpen, setMobileOpen] = useState(false)
  const [scrolled, setScrolled] = useState(false)
  // Сколько серверов отстало от свежей версии — значок у «Нод».
  const updates = useLoad(() => get<UpdateInfo>('updates'), [], 300000)

  // The server ends a session after a few minutes without actions and when the page is closed.
  // Every half a minute the page says it is alive and whether anything was clicked or typed.
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
    // A closed tab ends the session; a reload makes a request at once and keeps it.
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

  // The top app bar lifts onto a container tone once the page scrolls under it.
  useEffect(() => {
    const h = () => setScrolled(window.scrollY > 4)
    h()
    window.addEventListener('scroll', h, { passive: true })
    return () => window.removeEventListener('scroll', h)
  }, [])

  useEffect(() => {
    const h = (event: HashChangeEvent) => {
      const next = currentPage()
      if (next !== page && !confirmPageLeave()) {
        history.replaceState(null, '', event.oldURL)
        return
      }
      setPage(next)
      setMobileOpen(false)
    }
    window.addEventListener('hashchange', h)
    return () => window.removeEventListener('hashchange', h)
  }, [page])

  const { mode } = useMode()
  const p = allPages.find((x) => x.id === page)!
  useEffect(() => {
    document.title = `${p.title} · vynel`
  }, [p])

  // Видимость пункта: режим достаточного уровня, либо это текущая страница
  // (прямая ссылка не должна пропадать из меню).
  const rankOf = modeRank[mode]
  const visible = (x: Page) => x.id === page || rankOf >= modeRank[x.min ?? 'light']

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
          <Logo />
          <div className="brand-name">vyn<span>el</span></div>
        </div>
        <nav className="nav">
          {sections.map((s) => {
            const pages = s.pages.filter(visible)
            if (pages.length === 0) return null
            return (
              <div key={s.title}>
                <div className="nav-section">{s.title}</div>
                {pages.map((x) => (
                  <a key={x.id} href={'#/' + x.id} className={'nav-item' + (x.id === page ? ' active' : '')} title={x.title}>
                    <Icon name={x.icon} />
                    <span className="nav-label">{x.title}</span>
                    <span className="nav-short">{x.short || x.title}</span>
                    {x.id === 'nodes' && (updates.data?.count || 0) > 0 && <span className="nav-badge" title={t('main.update_available')}>{updates.data!.count}</span>}
                  </a>
                ))}
              </div>
            )
          })}
        </nav>
        <div className="side-foot">
          <div className="side-foot-text">{login} · {version}</div>
          <button className="nav-item" onClick={() => { if (confirmPageLeave()) onLogout() }} title={t('main.log_out')}>
            <Icon name="logout" />
            <span className="nav-label">{t('main.log_out')}</span>
            <span className="nav-short">{t('main.log_out')}</span>
          </button>
          <button className="nav-item collapse-btn" onClick={toggle} title={collapsed ? t('main.expand_menu') : t('main.collapse_menu')}>
            <Icon name={collapsed ? 'expand' : 'collapse'} />
            <span className="nav-label">{t('main.collapse')}</span>
            <span className="nav-short">{t('main.menu')}</span>
          </button>
        </div>
      </aside>
      <div className="scrim" onClick={() => setMobileOpen(false)} />
      <div className="main">
        <header className={'topbar' + (scrolled ? ' scrolled' : '')}>
          <button className="btn ghost icon burger" onClick={() => setMobileOpen(true)} aria-label={t('main.menu')}><Icon name="menu" /></button>
          <h1>{p.title}</h1>
          <div className="spacer" />
          <HelpButton pageId={p.id} />
          <ModeSwitch />
          <LangSwitch />
          {inTelegram && <BrowserButton />}
        </header>
        <main className="content" key={page}>{p.el()}</main>
      </div>
    </div>
  )
}

// ModeSwitch — «Лёгкий / Стандарт / Про»: сколько панель показывает сущностей и кнопок.
function ModeSwitch() {
  const { mode, setMode } = useMode()
  return (
    <div className="row mode-switch" style={{ gap: 6 }}>
      <span className="muted small hide-sm">{t('main.mode')}</span>
      <div className="seg sm">
        <button className={mode === 'light' ? 'on' : ''} title={t('main.mode_light_info')} onClick={() => setMode('light')}>{t('main.mode_light')}</button>
        <button className={mode === 'standard' ? 'on' : ''} title={t('main.mode_standard_info')} onClick={() => setMode('standard')}>{t('main.mode_standard')}</button>
        <button className={mode === 'pro' ? 'on' : ''} title={t('main.mode_pro_info')} onClick={() => setMode('pro')}>{t('main.mode_pro')}</button>
      </div>
    </div>
  )
}

// LangSwitch — язык интерфейса; после смены страница перезагружается: подписи читаются при загрузке.
function LangSwitch() {
  return (
    <div className="seg sm" title={t('main.language')}>
      {languages.map((l) => (
        <button key={l.code} className={lang() === l.code ? 'on' : ''} title={l.name}
          onClick={() => { if (lang() !== l.code) setLang(l.code).then(() => location.reload()) }}>{l.short}</button>
      ))}
    </div>
  )
}

// BrowserButton — в панели, открытой в Telegram: одноразовая ссылка входа, сразу во внешний браузер.
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

// HelpButton — «?» в топбаре: справка по текущей странице в drawer.
function HelpButton({ pageId }: { pageId: string }) {
  const [open, setOpen] = useState(false)
  const text = (pageHelp as Record<string, string>)[pageId]
  return (
    <>
      <button className="btn ghost icon" title={t('main.page_help')} aria-label={t('main.page_help')} onClick={() => setOpen(true)}><Icon name="help" /></button>
      {open && (
        <Drawer title={t('main.page_help')} onClose={() => setOpen(false)}>
          <div className="card card-pad">
            <p style={{ marginTop: 0 }}>{text || t('main.page_help_generic')}</p>
            <p className="muted small" style={{ marginBottom: 0 }}>{t('main.page_help_modes')}</p>
          </div>
        </Drawer>
      )}
    </>
  )
}

// pageHelp — короткая справка по каждой странице (ключи main.help_*).
const pageHelp: Record<string, string> = {
  overview: t('main.help_overview'),
  insights: t('main.help_insights'),
  users: t('main.help_users'),
  groups: t('main.help_groups'),
  templates: t('main.help_templates'),
  externals: t('main.help_externals'),
  nodes: t('main.help_nodes'),
  profiles: t('main.help_profiles'),
  xray: t('main.help_xray'),
  billing: t('main.help_billing'),
  subscription: t('main.help_subscription'),
  settings: t('main.help_settings'),
  telegram: t('main.help_telegram'),
  api: t('main.help_api'),
  audit: t('main.help_audit'),
  account: t('main.help_account'),
}

// Onboarding — одноразовый выбор режима при первом входе.
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
      <span className="onboard-icon"><Icon name={icon} size={28} /></span>
      <b>{title}</b>
      <span className="small text-2">{text}</span>
    </button>
  )
  return (
    <Modal title={t('main.onboarding_title')} onClose={() => pick('standard')}>
      <p className="text-2" style={{ marginTop: 0 }}>{t('main.onboarding_intro')}</p>
      <div className="onboard-cards">
        {card('light', 'dashboard', t('main.mode_light'), t('main.onboarding_light'))}
        {card('standard', 'settings', t('main.mode_standard'), t('main.onboarding_standard'))}
        {card('pro', 'code', t('main.mode_pro'), t('main.onboarding_pro'))}
      </div>
      <label className="check" style={{ marginTop: 14 }}>
        <input type="checkbox" checked={migrant} onChange={(e) => setMigrantFlag(e.target.checked)} />
        {' '}{t('main.onboarding_migrant')}
      </label>
    </Modal>
  )
}

function App() {
  const [session, setSession] = useState<{ login: string; version: string; method?: string } | null | undefined>(undefined)
  const [magicError, setMagicError] = useState('')
  const [onboarding, setOnboarding] = useState(() => !modeChosen())
  const check = useCallback(() => {
    get('session').then(setSession, () => setSession(null))
  }, [])
  useEffect(() => {
    setUnauthorizedHandler((message) => {
      // Why the server ended the session (inactivity, another IP, a closed page) goes on the login screen.
      if (message.startsWith(t('main.session'))) setMagicError(message)
      setSession(null)
    })
    // Одноразовая ссылка из Telegram-бота: токен во фрагменте адреса (#/magic/…). Сразу убрать его
    // из адресной строки и истории, затем обменять на сессию.
    const m = location.hash.match(/^#\/magic\/([A-Za-z0-9_-]+)/)
    if (m) {
      history.replaceState(null, '', location.pathname + '#/overview')
      post('login/magic', { token: m[1] }).then(check, (e) => {
        setMagicError(e.message)
        check()
      })
      return
    }
    // Telegram Mini App: Telegram кладёт подписанные данные в фрагмент (#tgWebAppData=…). Если сессии
    // ещё нет, обменять их на сессию; фрагмент сразу убрать.
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
