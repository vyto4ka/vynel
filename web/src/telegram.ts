// Мост к Telegram для панели, открытой как Mini App, без загрузки telegram-web-app.js: события
// уходят тем же путём, что у официального скрипта (мобильные приложения, Desktop, веб-версия).

function postEvent(type: string, data: Record<string, unknown>): boolean {
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
      window.parent.postMessage(JSON.stringify({ eventType: type, eventData: data }), '*')
      return true
    }
  } catch { /* не Telegram */ }
  return false
}

// openExternal открывает ссылку во внешнем браузере (из Mini App) или в новой вкладке.
export function openExternal(url: string) {
  if (!postEvent('web_app_open_link', { url, try_instant_view: false })) window.open(url, '_blank', 'noopener')
}
