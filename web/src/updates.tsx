import { useEffect, useState } from 'react'
import { get, post, UpdateInfo } from './api'
import { Icon, useAction, useConfirm, useLoad, useToast } from './ui'

type RunResult = { started: string[]; failed: Record<string, string> }

// Запуск обновления. Если среди запущенных есть панель, страница ждёт, пока панель вернётся
// с новой версией, и перезагружается сама.
export function useRunUpdate(info: UpdateInfo | null, reload: () => void) {
  const act = useAction()
  const toast = useToast()
  const [waiting, setWaiting] = useState(false)

  useEffect(() => {
    if (!waiting || !info) return
    const from = info.panel.version
    const t = setInterval(() => {
      get<{ version: string }>('session').then((s) => { if (s.version !== from) location.reload() }, () => {})
    }, 3000)
    return () => clearInterval(t)
  }, [waiting, info])

  return {
    waiting,
    run: async (target: 'all' | 'panel' | 'node', id?: number) => {
      const r = await act(() => post<RunResult>('updates/run', { target, id }))
      if (!r) return
      const failed = Object.entries(r.failed)
      if (r.started.length) toast('Обновление запущено: ' + r.started.join(', '))
      for (const [name, err] of failed) toast(`${name}: ${err}`, true)
      if (r.started.includes('Панель')) setWaiting(true)
      reload()
    },
  }
}

// Баннер на дашборде: есть ли новая версия, что устарело, кнопка «Обновить всё».
export function UpdateBanner() {
  const { data, reload } = useLoad(() => get<UpdateInfo>('updates'), [], 20000)
  const confirm = useConfirm()
  const { waiting, run } = useRunUpdate(data, reload)
  if (!data) return null

  const updating = [data.panel, ...data.nodes].filter((n) => n.updating)
  if (waiting || updating.length > 0) {
    return (
      <div className="banner">
        <span className="spinner" />
        <div className="grow">
          <b>Идёт обновление</b>
          <div className="small text-2">
            {waiting || data.panel.updating ? 'Панель перезапустится и эта страница обновится сама, обычно за 1–2 минуты. ' : ''}
            {updating.filter((n) => n.id).length > 0 && <>Ноды: {updating.filter((n) => n.id).map((n) => n.name).join(', ')}.</>}
          </div>
        </div>
      </div>
    )
  }
  if (data.lastPanel && !data.lastPanel.ok) {
    return (
      <div className="banner error">
        <Icon name="refresh" />
        <div className="grow">
          <b>Последнее обновление панели не удалось</b>
          <pre className="small banner-log">{data.lastPanel.error}</pre>
          <div className="small text-2">Полный лог на сервере: /var/log/vynel-update.log</div>
        </div>
        <button className="btn sm" onClick={() => run('panel')}>Повторить</button>
      </div>
    )
  }
  if (data.count === 0) return null

  const nodes = data.nodes.filter((n) => n.available)
  const what = [data.panel.available && 'панель', nodes.length > 0 && `${nodes.length} ${nodes.length === 1 ? 'нода' : 'ноды'}`].filter(Boolean).join(' и ')
  return (
    <div className="banner">
      <Icon name="refresh" />
      <div className="grow">
        <b>Доступна новая версия vynel · {data.latest}</b>
        <div className="small text-2">Устарели: {what}. Обновятся vynel, Xray и Caddy, данные сохранятся.</div>
      </div>
      <a className="btn ghost sm" href="#/nodes">Подробнее</a>
      <button className="btn primary sm" onClick={async () => {
        if (await confirm(<>Обновить {what} сейчас? Всё обновляется <b>одновременно</b>: около минуты подключения не будут работать, затем всё поднимется само. Если новая версия не запустится, вернётся прежняя.</>, 'Обновить всё', false))
          run('all')
      }}>Обновить всё</button>
    </div>
  )
}
