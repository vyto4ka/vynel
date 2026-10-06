export function bytes(b: number | null | undefined): string {
  if (b == null) return '∞'
  const u = ['Б', 'КБ', 'МБ', 'ГБ', 'ТБ']
  let i = 0
  let v = b
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024
    i++
  }
  return (i === 0 ? v.toFixed(0) : v.toFixed(v >= 100 ? 0 : v >= 10 ? 1 : 2)) + ' ' + u[i]
}

export function bps(b: number): string {
  return bytes(b) + '/с'
}

export const GiB = 1024 ** 3

export function date(ts: number | null | undefined): string {
  if (!ts) return '—'
  return new Date(ts * 1000).toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit', year: 'numeric' })
}

export function dateTime(ts: number | null | undefined): string {
  if (!ts) return '—'
  return new Date(ts * 1000).toLocaleString('ru-RU', { day: '2-digit', month: '2-digit', year: '2-digit', hour: '2-digit', minute: '2-digit' })
}

export function plural(n: number, one: string, few: string, many: string): string {
  const m10 = n % 10
  const m100 = n % 100
  if (m10 === 1 && m100 !== 11) return one
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return few
  return many
}

export function ago(ts: number | null | undefined): string {
  if (!ts) return 'никогда'
  const s = Math.floor(Date.now() / 1000 - ts)
  if (s < 60) return 'только что'
  const m = Math.floor(s / 60)
  if (m < 60) return `${m} мин назад`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h} ч назад`
  const d = Math.floor(h / 24)
  return `${d} ${plural(d, 'день', 'дня', 'дней')} назад`
}

export function left(ts: number | null | undefined): string {
  if (!ts) return 'бессрочно'
  const s = ts - Date.now() / 1000
  if (s <= 0) return 'истекла'
  const d = Math.floor(s / 86400)
  if (d >= 1) return `ещё ${d} ${plural(d, 'день', 'дня', 'дней')}`
  const h = Math.floor(s / 3600)
  return h >= 1 ? `ещё ${h} ч` : 'меньше часа'
}

export function isOnline(ts: number | null | undefined): boolean {
  return !!ts && Date.now() / 1000 - ts < 180
}

export function uptime(s: number): string {
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  if (d > 0) return `${d} д ${h} ч`
  const m = Math.floor((s % 3600) / 60)
  return `${h} ч ${m} мин`
}

export const statusInfo: Record<string, { label: string; color: string }> = {
  active: { label: 'активен', color: 'green' },
  limited: { label: 'трафик исчерпан', color: 'pink' },
  expired: { label: 'истёк', color: 'amber' },
  disabled: { label: 'отключён', color: '' },
}

export const nodeStateInfo: Record<string, { label: string; color: string }> = {
  'in sync': { label: 'работает', color: 'green' },
  syncing: { label: 'применяет', color: 'amber' },
  offline: { label: 'нет связи', color: 'pink' },
  pending: { label: 'ждёт подключения', color: 'amber' },
  disabled: { label: 'выключена', color: '' },
}

export const resetLabels: Record<string, string> = {
  no: 'не сбрасывать',
  day: 'каждый день',
  week: 'каждую неделю',
  month: 'каждый месяц',
}

export const clientTypes: Record<string, string> = {
  auto: 'авто (по приложению)',
  base64: 'ссылки (base64)',
  mihomo: 'Clash / Mihomo',
  singbox: 'sing-box',
  xray: 'Xray JSON',
}

export function toDateInput(ts: number | null): string {
  if (!ts) return ''
  const d = new Date(ts * 1000)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}

export function fromDateInput(v: string): number | null {
  if (!v) return null
  const [y, m, d] = v.split('-').map(Number)
  return Math.floor(new Date(y, m - 1, d, 23, 59, 0).getTime() / 1000)
}
