// Клиент API. Адреса относительные: панель открывается по секретному пути (/xxxx/),
// и "api/..." указывает на /xxxx/api/...

import { lang, t } from './i18n'

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
  }
}

let onUnauthorized: (message: string) => void = () => {}
export function setUnauthorizedHandler(fn: (message: string) => void) {
  onUnauthorized = fn
}

export async function api<T = any>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch('api/' + path, {
    method,
    credentials: 'same-origin',
    headers: { 'X-Vynel': '1', 'X-Lang': lang(), ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}) },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  const text = await res.text()
  let data: any = null
  try {
    data = text ? JSON.parse(text) : null
  } catch {
    data = text
  }
  if (!res.ok) {
    if (res.status === 401 && path !== 'login') onUnauthorized((data && data.error) || '')
    throw new ApiError(res.status, (data && data.error) || plainError(res))
  }
  return data as T
}

// plainError explains an answer that did not come from the panel's API (no JSON error).
function plainError(res: Response): string {
  const code = `HTTP ${res.status}${res.statusText ? ' ' + res.statusText : ''}`
  if (res.status === 404) return t('api.no_answer', { code })
  if (res.status >= 502 && res.status <= 504) return t('api.unavailable', { code })
  return t('api.server_error', { code })
}

export const get = <T = any>(p: string) => api<T>('GET', p)
export const post = <T = any>(p: string, b: unknown = {}) => api<T>('POST', p, b)
export const put = <T = any>(p: string, b: unknown = {}) => api<T>('PUT', p, b)
export const patch = <T = any>(p: string, b: unknown = {}) => api<T>('PATCH', p, b)
export const del = <T = any>(p: string) => api<T>('DELETE', p)

// ---- типы ----

export interface User {
  id: number
  username: string
  status: 'active' | 'disabled' | 'limited' | 'expired'
  enabled: boolean
  expireAt: number | null
  trafficLimit: number | null
  trafficUsed: number
  lifetimeUsed: number
  resetStrategy: string
  hwidLimit: number | null
  hwidOff: boolean
  ipLimit: number | null
  clientType: string
  note: string
  onlineAt: number | null
  createdAt: number
  subLastAt: number | null
  subLastUA: string
  groupIds: number[]
  templateId: number | null
}

export interface Device {
  id: number
  hwid: string
  platform: string
  osVersion: string
  model: string
  userAgent: string
  firstSeen: number
  lastSeen: number
  lastIp: string
}

// Адрес пользователя в онлайне (лимит IP): занял слот, ждёт или заблокирован.
export interface OnlineAddr {
  addr: string
  since: number
  seen: number
  admitted: boolean
  blocked: boolean
}

export interface Day {
  day: number
  up: number
  down: number
}

export interface UserDetails {
  user: User
  uuid: string
  subUrl?: string
  subError?: string
  devices: Device[]
  hwidDefault: number
  ipDefault: number
  ips: OnlineAddr[]
  trafficByNode: { code: string; bytes: number }[]
  daily: Day[]
  links: { tag: string; node: string; remark: string; hidden: boolean; link: string }[]
  grants: UserGrant[]
}

export interface Group {
  id: number
  name: string
  description: string
  users: number
  rules: { kind: string; refId: number; label: string }[]
}

export interface Template {
  id: number
  name: string
  isDefault: boolean
  expireMonths: number
  expireDays: number
  trafficLimit: number | null
  resetStrategy: string
  hwidLimit: number | null
  clientType: string
  groupIds: number[]
  note: string
  grants: TemplateGrant[] // каскады и подписки, которые получает новый пользователь
}

export interface TemplateGrant {
  kind: 'cascade' | 'external'
  refId: number
}

export interface Host {
  remark: string
  address: string
  port: number
  network: string
  security: string
  sni: string
  fingerprint: string
  publicKey: string
  shortId: string
  hidden: boolean
}

export interface Inbound {
  id: number
  nodeId: number
  profileId: number
  profileName: string
  tag: string
  listen: string
  port: number
  enabled: boolean
  values: Record<string, any>
  host: Host | null
  hostOverride: Record<string, any>
  error?: string
}

export interface Node {
  id: number
  code: string
  name: string
  country: string
  flag: string
  domain: string
  local: boolean
  enabled: boolean
  state: string
  lastSeenAt: number | null
  xrayVersion: string
  agentVersion: string
  caddyVersion: string
  nginxVersion: string
  problems: string[]
  todayBytes: number
  metrics: {
    ts: number
    cpu: number
    memUsed: number
    memTotal: number
    load1: number
    rxBps: number
    txBps: number
    uptime: number
    online: number
  } | null
  inbounds: Inbound[]
  addresses: { id: number; ip: string; interface: string; onInterface: boolean; primary: boolean }[]
  provider: Provider | null // где арендован сервер
}

// Хостинг-провайдер: сайт, вход в личный кабинет, иконка с сайта.
export interface Provider {
  id: number
  name: string
  url: string
  loginUrl: string
  icon: string // data: URI, '' — нет
}

// Нода в «Биллинге»: стоимость, оплата и лимит трафика у провайдера.
export interface BillingNode {
  id: number
  code: string
  name: string
  flag: string
  enabled: boolean
  providerId: number | null
  cost: number
  currency: string
  costPeriod: 'month' | 'year'
  monthlyCost: number
  paidUntil: number | null
  trafficLimit: number // байты, 0 — без лимита
  resetDay: number
  trafficCount: 'both' | 'out' | 'in'
  used: number
  periodFrom: number
  periodTo: number
  offByLimit: boolean
  overridden: boolean
}

export interface Billing {
  providers: Provider[]
  nodes: BillingNode[]
  margin: number
  remindDays: number
}

export interface Variable {
  name: string
  scope: 'profile' | 'node'
  source: string
  generator?: string
  options?: string[]
  default?: any
  default_from?: string
  from?: string
  secret?: boolean
  validate?: string
  optional?: boolean
  description?: string
}

export interface ProfileTemplate {
  id: string
  title: string
  summary: string
  version: number
  tag_pattern: string
  remark_pattern: string
  variables: Variable[]
  custom: boolean
  source: string
  profiles: string[]
  broken?: string
}

export interface ProfileDetails {
  id: number
  name: string
  templateId: string
  templateVersion: number
  values: Record<string, any>
  override: Record<string, any>
  tagPattern: string
  remarkPattern: string
  inboundSource?: Record<string, any>
  hostSource?: Record<string, any>
  ownInbound?: boolean
  ownHost?: boolean
  client: ProfileClient
  subRouting?: ClientRouting
  template?: {
    id: string; title: string; summary: string; version: number; variables: Variable[]; custom: boolean; tagPattern: string; remarkPattern: string
    inboundSource: Record<string, any>; hostSource: Record<string, any>
  }
}

export interface InboundDetails {
  id: number
  tag: string
  nodeId: number
  enabled: boolean
  port: number
  listenAddressId: number | null
  egressAddressId: number | null
  values: Record<string, any>
  override: Record<string, any>
  profile: { id: number; name: string }
  addresses: { id: number; ip: string; interface: string; onInterface: boolean; primary: boolean }[]
  variables?: Variable[]
  inboundSource?: Record<string, any> // the profile's inbound with ${VARIABLES} in place
  hostSource?: Record<string, any>
  rendered?: Record<string, any>
  error?: string
}

export interface Profile {
  id: number
  name: string
  templateId: string
  templateTitle: string
  values: Record<string, any>
  override: Record<string, any>
  tagPattern: string
  remarkPattern: string
  inbounds: string[]
}

export interface Setting {
  key: string
  section: string
  title: string
  hint?: string // short note under the field
  help?: string // what the setting is, in a popover
  type: 'string' | 'int' | 'bool' | 'select'
  options?: string[]
  default?: string
  value?: string
  set: boolean
  movesPanel?: boolean
}

export interface NetRole { kind: 'panel' | 'sub' | 'gateway' | 'inbound' | 'egress'; tag?: string; all?: boolean }

export interface Network {
  nodes: {
    id: number; code: string; name: string; country: string; domain: string; local: boolean
    addresses: { id: number; ip: string; interface: string; onInterface: boolean; primary: boolean; roles: NetRole[] }[]
    allRoles: NetRole[]
  }[]
  domains: { name: string; purpose: 'sub' | 'panel' | 'node'; node: string; resolved: string[]; expected: string[]; ok: boolean; error?: string }[]
}

export interface Overview {
  users: { total: number; active: number; limited: number; expired: number; disabled: number }
  onlineNow: number
  todayBytes: number
  monthBytes: number
  topUsers: { id: number; username: string; bytes: number }[]
  daily: Day[]
  nodes: Node[]
  webUrl: string
  version: string
}

export interface AuditEntry {
  id: number
  ts: number
  actor: string
  actorId: string
  action: string
  entity: string
  entityId: number
  diff: string
}

export interface JoinInfo {
  code: string
  id: number
  token?: string
  command?: string
  ttl: string
  error?: string
  warnings?: string[] | null
}

// ---- подписка ----

export interface SubHeader {
  name: string
  value: string
  base64: boolean
  clients: string
  enabled: boolean
  note?: string
}

export interface SubHeaderPreset extends SubHeader {
  apps: string
  description: string
}

export interface SubApp {
  id: string
  name: string
  link: string
  download: string
  platforms: string[]
  enabled: boolean
  note?: string
}

export interface SubPage {
  heading: string
  description: string
  instructions: string
  footer: string
  theme: 'auto' | 'dark' | 'light'
  accent: string
  showQr: boolean
  showTraffic: boolean
  apps: SubApp[]
}

export interface SubBasics {
  title: string
  updateHours: number
  supportUrl: string
  announce: string
  announceUrl: string
  pageUrl: string
}

export interface SubConfig {
  basics: SubBasics
  headers: SubHeader[]
  page: SubPage
  headerCatalog: SubHeaderPreset[]
  appCatalog: SubApp[]
  defaultHeaders: SubHeader[]
  defaultPage: SubPage
  variables: { name: string; description: string; example: string }[]
  rules: SubRule[]
  defaultRules: SubRule[]
  formats: string[]
  routing: ClientRouting
  defaultRouting: ClientRouting
}

// Маршрутизация, которую подписка отдаёт приложениям (Xray JSON, Mihomo, sing-box).
export interface ClientRouting {
  ruDirect: boolean
  dnsDirect: string[]
  dnsProxy: string[]
  torrent: 'proxy' | 'direct' | 'block'
  blockAds: boolean
  direct: string[]
  proxy: string[]
  block: string[]
  fragment?: ClientFragment
}

// Фрагментация ClientHello (finalmask «fragment» в Xray JSON).
export interface ClientFragment {
  enabled: boolean
  packets: string
  length: string
  delay: string
}

// Свой клиентский конфиг профиля (галочка «Кастомный профиль клиента»).
export interface ProfileClient {
  custom: boolean
  mode: 'routing' | 'json' | ''
  routing?: ClientRouting
  config?: string
}

export interface SubRule {
  pattern: string
  format: string
  enabled: boolean
  note?: string
  template?: string // свой конфиг клиента (Mihomo YAML, sing-box или Xray JSON), как в Remnawave
}

export interface TemplateInfo {
  summary: string[]
  routing: ClientRouting | null
  skipped: string[]
}

// ---- обновления ----

export interface UpdateNode {
  id: number
  code: string
  name: string
  flag: string
  local: boolean
  online: boolean
  version: string
  available: boolean
  updating: boolean
  // sent → running → restarting → done | failed; '' — ничего не происходит
  state: '' | 'sent' | 'running' | 'restarting' | 'done' | 'failed'
  startedAt?: number
  finishedAt?: number
  log?: string
}

export interface UpdateInfo {
  release: string
  latest: string
  checkError?: string
  checkedAt: number
  panel: UpdateNode
  nodes: UpdateNode[]
  count: number
}

// Каскад: вход на одной ноде, выход через другую.
export interface Cascade {
  id: number
  name: string
  entryInboundId: number
  exitInboundId: number
  entry: string
  exit: string
  remark: string
  title: string
  enabled: boolean
  problem?: string // почему его нет в подписках (или есть не везде)
}

// Чужая подписка, чьи серверы добавляются в наши.
export interface ExternalSub {
  id: number
  name: string
  url: string
  enabled: boolean
  fetchedAt: number | null
  error: string
  servers: string[]
}

export interface GrantRow {
  id: number
  userId: number | null
  groupId: number | null
  kind: 'cascade' | 'external'
  refId: number
  expireAt: number | null
}

// Что есть у пользователя: своя выдача и/или через группы; одно и то же — один раз.
export interface UserGrant {
  kind: 'cascade' | 'external'
  refId: number
  expireAt: number | null
  grantId: number
  groups: number[]
  active: boolean
  problem?: string
}

// ---- интеграции: API-токены и вебхуки ----

export interface ApiToken {
  id: number
  name: string
  prefix: string
  scopes: string[]
  cidrs: string[]
  rateLimit: number
  expiresAt: number | null
  createdBy: string
  createdAt: number
  lastUsedAt: number | null
  lastUsedIp: string
  uses: number
  revokedAt: number | null
  state: 'active' | 'expired' | 'revoked'
}

export interface Webhook {
  id: number
  name: string
  url: string
  events: string[]
  enabled: boolean
  allowPrivate: boolean
  fails: number
  offReason: string
  createdAt: number
  lastAt: number | null
  lastStatus: number
  secretHint: string
  secret?: string // только сразу после создания и смены
}

export interface Delivery {
  id: number
  eventId: number
  type: string
  state: 'pending' | 'ok' | 'failed'
  attempts: number
  nextAt: number
  createdAt: number
  status: number
  error: string
  tookMs: number
  body: string
}

export interface Integrations {
  baseUrl: string
  specUrl: string
  scopes: string[]
  events: string[]
  defaultRate: number
  tokens: ApiToken[]
  webhooks: Webhook[]
  headers: { signature: string; timestamp: string; event: string; delivery: string }
}
