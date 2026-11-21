// Клиентский SWR-кэш данных и UI-состояния вкладок в памяти браузера.
// API панели намеренно отдаёт Cache-Control: no-store, поэтому мгновенное переключение
// вкладок обеспечивается этим модулем и полностью очищается при выходе из аккаунта.

type CacheEntry<T = unknown> = {
  data: T
  updatedAt: number
}

const entries = new Map<string, CacheEntry>()
const inFlight = new Map<string, Promise<unknown>>()
const listeners = new Map<string, Set<() => void>>()
const uiStates = new Map<string, unknown>()

function notify(key: string) {
  const subs = listeners.get(key)
  if (!subs) return
  subs.forEach((fn) => fn())
}

export function getCacheEntry<T>(key?: string): CacheEntry<T> | undefined {
  if (!key) return undefined
  return entries.get(key) as CacheEntry<T> | undefined
}

export function setCacheEntry<T>(key: string, data: T | null) {
  if (data === null) {
    entries.delete(key)
  } else {
    entries.set(key, { data, updatedAt: Date.now() })
  }
  notify(key)
}

export function isCacheFresh(key?: string, staleTimeMs = 0): boolean {
  if (!key || staleTimeMs <= 0) return false
  const e = entries.get(key)
  if (!e) return false
  return Date.now() - e.updatedAt < staleTimeMs
}

export function subscribeCache(key: string | undefined, listener: () => void): () => void {
  if (!key) return () => {}
  let subs = listeners.get(key)
  if (!subs) {
    subs = new Set()
    listeners.set(key, subs)
  }
  subs.add(listener)
  return () => {
    const s = listeners.get(key)
    if (!s) return
    s.delete(listener)
    if (s.size === 0) listeners.delete(key)
  }
}

// fetchCached дедуплицирует одновременные запросы с одним cacheKey и записывает ответ в кэш.
export function fetchCached<T>(key: string, fn: () => Promise<T>, force = false): Promise<T> {
  if (!force) {
    const existing = inFlight.get(key) as Promise<T> | undefined
    if (existing) return existing
  }
  const p = fn()
    .then((res) => {
      setCacheEntry(key, res)
      return res
    })
    .finally(() => {
      if (inFlight.get(key) === p) inFlight.delete(key)
    })
  inFlight.set(key, p)
  return p
}

// prefetch прогревает кэш в фоне (например, при наведении курсора на вкладку), если данных нет или они устарели.
export function prefetch<T>(key: string, fn: () => Promise<T>, staleTimeMs = 15000) {
  if (isCacheFresh(key, staleTimeMs) || inFlight.has(key)) return
  fetchCached(key, fn).catch(() => {})
}

export function invalidateCache(matcher: string | string[] | RegExp) {
  if (Array.isArray(matcher)) {
    for (const m of matcher) invalidateCache(m)
    return
  }
  for (const k of Array.from(entries.keys())) {
    const match = typeof matcher === 'string' ? k === matcher || k.startsWith(matcher + ':') : matcher.test(k)
    if (match) {
      const cur = entries.get(k)
      if (cur) entries.set(k, { data: cur.data, updatedAt: 0 })
    }
  }
}

export function getUIState<T>(key: string, fallback: T): T {
  return uiStates.has(key) ? (uiStates.get(key) as T) : fallback
}

export function setUIState<T>(key: string, value: T) {
  uiStates.set(key, value)
}

export function clearAllCache() {
  entries.clear()
  inFlight.clear()
  uiStates.clear()
}
