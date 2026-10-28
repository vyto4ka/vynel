import { fmtDate, t } from './i18n'

export function bytes(b: number | null | undefined): string {
  if (b == null) return '∞'
  const u = [t('format.b'), t('format.kb'), t('format.mb'), t('format.gb'), t('format.tb')]
  let i = 0
  let v = b
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024
    i++
  }
  return (i === 0 ? v.toFixed(0) : v.toFixed(v >= 100 ? 0 : v >= 10 ? 1 : 2)) + ' ' + u[i]
}

export function bps(b: number): string {
  return bytes(b) + t('format.per_second')
}

export const GiB = 1024 ** 3

export function date(ts: number | null | undefined): string {
  if (!ts) return '—'
  return fmtDate(new Date(ts * 1000), { day: '2-digit', month: '2-digit', year: 'numeric' })
}

export function dateTime(ts: number | null | undefined): string {
  if (!ts) return '—'
  return fmtDate(new Date(ts * 1000), { day: '2-digit', month: '2-digit', year: '2-digit', hour: '2-digit', minute: '2-digit' })
}

export function ago(ts: number | null | undefined): string {
  if (!ts) return t('format.never')
  const s = Math.floor(Date.now() / 1000 - ts)
  if (s < 60) return t('format.just_now')
  const m = Math.floor(s / 60)
  if (m < 60) return t('format.minutes_ago', { n: m })
  const h = Math.floor(m / 60)
  if (h < 24) return t('format.hours_ago', { n: h })
  return t('format.days_ago', { n: Math.floor(h / 24) })
}

export function left(ts: number | null | undefined): string {
  if (!ts) return t('format.unlimited')
  const s = ts - Date.now() / 1000
  if (s <= 0) return t('format.expired')
  const d = Math.floor(s / 86400)
  if (d >= 1) return t('format.days_left', { n: d })
  const h = Math.floor(s / 3600)
  return h >= 1 ? t('format.hours_left', { n: h }) : t('format.less_than_an_hour')
}

export function isOnline(ts: number | null | undefined): boolean {
  return !!ts && Date.now() / 1000 - ts < 180
}

export function uptime(s: number): string {
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  if (d > 0) return t('format.uptime_days', { d, h })
  const m = Math.floor((s % 3600) / 60)
  return t('format.uptime_hours', { h, m })
}

// Labels are looked up when used, so they follow the interface language.
export const statusInfo: Record<string, { label: string; color: string }> = {
  active: { get label() { return t('format.status_active') }, color: 'green' },
  limited: { get label() { return t('format.status_limited') }, color: 'pink' },
  expired: { get label() { return t('format.status_expired') }, color: 'amber' },
  disabled: { get label() { return t('format.status_disabled') }, color: '' },
}

export const nodeStateInfo: Record<string, { label: string; color: string }> = {
  'in sync': { get label() { return t('format.node_in_sync') }, color: 'green' },
  syncing: { get label() { return t('format.node_syncing') }, color: 'amber' },
  offline: { get label() { return t('format.node_offline') }, color: 'pink' },
  pending: { get label() { return t('format.node_pending') }, color: 'amber' },
  disabled: { get label() { return t('format.node_disabled') }, color: '' },
}

export const resetLabels: Record<string, string> = {
  get no() { return t('format.reset_no') },
  get day() { return t('format.reset_day') },
  get week() { return t('format.reset_week') },
  get month() { return t('format.reset_month') },
}

export const clientTypes: Record<string, string> = {
  get auto() { return t('format.client_auto') },
  get base64() { return t('format.client_base64') },
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
