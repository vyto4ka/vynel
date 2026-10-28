import { useEffect, useState } from 'react'
import { Overview } from './Overview'
import { ServerLoad } from './ServerLoad'
import { Stats } from './Stats'
import { t } from '../i18n'

type Tab = 'summary' | 'connections' | 'servers'
const tabs: [Tab, string][] = [['summary', t('dashboard.summary')], ['connections', t('dashboard.connections')], ['servers', t('common.servers')]]

// Вкладка из адреса: #/overview/servers; старая ссылка #/stats — это «Подключения».
function tabFromHash(): Tab {
  const [page, sub] = location.hash.replace(/^#\/?/, '').split('/')
  if (page === 'stats') return 'connections'
  return tabs.some(([t]) => t === sub) ? (sub as Tab) : 'summary'
}

// Dashboard — сводка, статистика подключений (трафик, пользователи, ноды) и нагрузка серверов.
export function Dashboard() {
  const [tab, setTab] = useState<Tab>(tabFromHash)
  useEffect(() => {
    const h = () => setTab(tabFromHash())
    window.addEventListener('hashchange', h)
    return () => window.removeEventListener('hashchange', h)
  }, [])
  const open = (t: Tab) => {
    history.replaceState(null, '', t === 'summary' ? '#/overview' : `#/overview/${t}`)
    setTab(t)
  }
  return (
    <>
      <div className="toolbar">
        <div className="seg">
          {tabs.map(([t, label]) => <button key={t} className={tab === t ? 'on' : ''} onClick={() => open(t)}>{label}</button>)}
        </div>
      </div>
      {tab === 'summary' && <Overview />}
      {tab === 'connections' && <Stats />}
      {tab === 'servers' && <ServerLoad />}
    </>
  )
}
