import { ReactElement, StrictMode, useCallback, useEffect, useState } from 'react'
import { createRoot } from 'react-dom/client'
import './styles.css'
import { get, post, setUnauthorizedHandler } from './api'
import { ConfirmProvider, Icon, ToastProvider } from './ui'
import { Login } from './pages/Login'
import { Overview } from './pages/Overview'
import { Stats } from './pages/Stats'
import { Users } from './pages/Users'
import { Groups } from './pages/Groups'
import { Templates } from './pages/Templates'
import { Nodes } from './pages/Nodes'
import { Profiles } from './pages/Profiles'
import { Settings } from './pages/Settings'
import { Subscription } from './pages/Subscription'
import { Audit } from './pages/Audit'
import { Telegram } from './pages/Telegram'
import { Account } from './pages/Account'

type Page = { id: string; title: string; icon: string; el: () => ReactElement }

const sections: { title: string; pages: Page[] }[] = [
  { title: 'Обзор', pages: [
    { id: 'overview', title: 'Дашборд', icon: 'dashboard', el: () => <Overview /> },
    { id: 'stats', title: 'Статистика', icon: 'chart', el: () => <Stats /> },
  ] },
  {
    title: 'Пользователи',
    pages: [
      { id: 'users', title: 'Пользователи', icon: 'users', el: () => <Users /> },
      { id: 'groups', title: 'Группы', icon: 'groups', el: () => <Groups /> },
      { id: 'templates', title: 'Шаблоны пользователей', icon: 'templates', el: () => <Templates /> },
      { id: 'subscription', title: 'Подписка', icon: 'sub', el: () => <Subscription /> },
    ],
  },
  {
    title: 'Серверы',
    pages: [
      { id: 'nodes', title: 'Ноды', icon: 'nodes', el: () => <Nodes /> },
      { id: 'profiles', title: 'Профили', icon: 'profiles', el: () => <Profiles /> },
    ],
  },
  {
    title: 'Система',
    pages: [
      { id: 'settings', title: 'Настройки', icon: 'settings', el: () => <Settings /> },
      { id: 'telegram', title: 'Telegram', icon: 'telegram', el: () => <Telegram /> },
      { id: 'audit', title: 'Журнал', icon: 'audit', el: () => <Audit /> },
      { id: 'account', title: 'Аккаунт', icon: 'account', el: () => <Account /> },
    ],
  },
]
const allPages = sections.flatMap((s) => s.pages)

function currentPage(): string {
  const id = location.hash.replace(/^#\/?/, '').split('/')[0]
  return allPages.some((p) => p.id === id) ? id : 'overview'
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

function Shell({ login, version, onLogout }: { login: string; version: string; onLogout: () => void }) {
  const [page, setPage] = useState(currentPage)
  const [collapsed, setCollapsed] = useState(() => load('vynel.sidebar', 'open') === 'collapsed')
  const [mobileOpen, setMobileOpen] = useState(false)

  useEffect(() => {
    const h = () => {
      setPage(currentPage())
      setMobileOpen(false)
    }
    window.addEventListener('hashchange', h)
    return () => window.removeEventListener('hashchange', h)
  }, [])

  const p = allPages.find((x) => x.id === page)!
  useEffect(() => {
    document.title = `${p.title} · vynel`
  }, [p])

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
          <div className="brand-logo">v</div>
          <div className="brand-name">vyn<span>el</span></div>
        </div>
        <nav className="nav">
          {sections.map((s) => (
            <div key={s.title}>
              <div className="nav-section">{s.title}</div>
              {s.pages.map((x) => (
                <a key={x.id} href={'#/' + x.id} className={'nav-item' + (x.id === page ? ' active' : '')} title={x.title}>
                  <Icon name={x.icon} />
                  <span className="nav-label">{x.title}</span>
                </a>
              ))}
            </div>
          ))}
        </nav>
        <div className="side-foot">
          <div className="side-foot-text">{login} · {version}</div>
          <button className="nav-item" onClick={onLogout} title="Выйти">
            <Icon name="logout" />
            <span className="nav-label">Выйти</span>
          </button>
          <button className="nav-item collapse-btn" onClick={toggle} title={collapsed ? 'Развернуть меню' : 'Свернуть меню'}>
            <Icon name={collapsed ? 'expand' : 'collapse'} />
            <span className="nav-label">Свернуть</span>
          </button>
        </div>
      </aside>
      <div className="scrim" onClick={() => setMobileOpen(false)} />
      <div className="main">
        <header className="topbar">
          <button className="btn ghost icon burger" onClick={() => setMobileOpen(true)} aria-label="Меню"><Icon name="menu" /></button>
          <h1>{p.title}</h1>
          <div className="spacer" />
        </header>
        <main className="content" key={page}>{p.el()}</main>
      </div>
    </div>
  )
}

function App() {
  const [session, setSession] = useState<{ login: string; version: string } | null | undefined>(undefined)
  const [magicError, setMagicError] = useState('')
  const check = useCallback(() => {
    get('session').then(setSession, () => setSession(null))
  }, [])
  useEffect(() => {
    setUnauthorizedHandler(() => setSession(null))
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
    check()
  }, [check])

  if (session === undefined) return null
  if (session === null) return <Login onLogin={check} error={magicError} />
  return (
    <Shell login={session.login} version={session.version}
      onLogout={() => post('logout').finally(() => setSession(null))} />
  )
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ToastProvider>
      <ConfirmProvider>
        <App />
      </ConfirmProvider>
    </ToastProvider>
  </StrictMode>,
)
