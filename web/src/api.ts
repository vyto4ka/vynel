// Клиент API. Адреса относительные: панель открывается по секретному пути (/xxxx/),
// и "api/..." указывает на /xxxx/api/...

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
  }
}

let onUnauthorized: () => void = () => {}
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn
}

export async function api<T = any>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch('api/' + path, {
    method,
    credentials: 'same-origin',
    headers: { 'X-Vynel': '1', ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}) },
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
    if (res.status === 401 && path !== 'login') onUnauthorized()
    throw new ApiError(res.status, (data && data.error) || res.statusText)
  }
  return data as T
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
  trafficByNode: { code: string; bytes: number }[]
  daily: Day[]
  links: { tag: string; node: string; remark: string; hidden: boolean; link: string }[]
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
  help?: string
  type: 'string' | 'int' | 'bool' | 'select'
  options?: string[]
  default?: string
  value?: string
  set: boolean
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
  uaRules: { pattern: string; format: string }[]
}
