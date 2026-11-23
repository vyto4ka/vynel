// Мост к Telegram для панели, открытой как Mini App, без загрузки telegram-web-app.js: события
// уходят тем же путём, что у официального скрипта (мобильные приложения, Desktop, веб-версия).

function postEvent(type: string, data: Record<string, unknown> = {}): boolean {
  const w = window as any
  try {
    if (w.TelegramWebviewProxy?.postEvent) {
      w.TelegramWebviewProxy.postEvent(type, JSON.stringify(data))
      return true
    }
    if (w.external && 'notify' in w.external) {
      w.external.notify(JSON.stringify({ eventType: type, eventData: data }))
      return true
    }
    if (window.parent !== window) {
      window.parent.postMessage(JSON.stringify({ eventType: type, eventData: data }), 'https://web.telegram.org')
      return true
    }
  } catch { /* не Telegram */ }
  return false
}

// openExternal открывает ссылку во внешнем браузере (из Mini App) или в новой вкладке.
export function openExternal(url: string) {
  if (!postEvent('web_app_open_link', { url, try_instant_view: false })) window.open(url, '_blank', 'noopener')
}

let tgInited = false
let safeTop = 0
let contentTop = 0
let safeBottom = 0

function applyInsets() {
  const root = document.documentElement
  root.style.setProperty('--tg-top', `${safeTop + contentTop}px`)
  root.style.setProperty('--tg-bottom', `${safeBottom}px`)
}

function handleIncoming(eventType: string, eventData: any) {
  if (!eventData || typeof eventData !== 'object') return
  if (eventType === 'safe_area_changed') {
    safeTop = Number(eventData.top) || 0
    safeBottom = Number(eventData.bottom) || 0
    applyInsets()
  } else if (eventType === 'content_safe_area_changed') {
    contentTop = Number(eventData.top) || 0
    applyInsets()
  }
}

// setTelegramFullscreen переключает полноэкранный режим Mini App (Bot API 8.0+: на ПК
// разворачивает окно горизонтально на весь экран, на телефоне — на весь экран с safe-area).
export function setTelegramFullscreen(on: boolean) {
  postEvent(on ? 'web_app_request_fullscreen' : 'web_app_exit_fullscreen')
}

// initTelegramMiniApp инициализирует Mini App: ready, expand, цвета шапки, safe-area и fullscreen.
export function initTelegramMiniApp(fullscreen: boolean) {
  document.documentElement.classList.add('tg-app')
  if (!tgInited) {
    tgInited = true
    // Подписка на события safe-area от Telegram Desktop / iOS / Android / Web.
    const w = window as any
    w.Telegram = w.Telegram || {}
    w.Telegram.WebView = w.Telegram.WebView || {}
    const prevReceive = w.Telegram.WebView.receiveEvent
    w.Telegram.WebView.receiveEvent = (eventType: string, eventData: unknown) => {
      handleIncoming(eventType, eventData)
      if (typeof prevReceive === 'function') prevReceive(eventType, eventData)
    }
    window.addEventListener('message', (e) => {
      if (e.source !== window.parent || e.origin !== 'https://web.telegram.org') return
      if (typeof e.data !== 'string') return
      try {
        const msg = JSON.parse(e.data)
        if (msg?.eventType) handleIncoming(msg.eventType, msg.eventData)
      } catch { /* чужое сообщение */ }
    })

    const bg = getComputedStyle(document.documentElement).getPropertyValue('--mantle').trim() || '#191c20'
    postEvent('web_app_ready')
    postEvent('web_app_expand')
    postEvent('web_app_set_header_color', { color: bg })
    postEvent('web_app_set_background_color', { color: bg })
    postEvent('web_app_request_safe_area')
    postEvent('web_app_request_content_safe_area')
  }
  if (fullscreen) {
    postEvent('web_app_request_fullscreen')
  }
}
