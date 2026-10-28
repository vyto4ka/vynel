// Клиент API Vynel на встроенном fetch (Node.js 18+), без зависимостей.
//
//   export VYNEL_API=https://panel.example.com/<секретный путь>/api/v1
//   export VYNEL_TOKEN=vyn_...
//   node client.mjs
import { randomUUID } from 'node:crypto'
import { setTimeout as sleep } from 'node:timers/promises'

export class VynelError extends Error {
  constructor(status, error = {}) {
    super(`${status} ${error.code ?? 'unknown'}: ${error.message ?? ''}`)
    Object.assign(this, { status, code: error.code, field: error.field, requestId: error.request_id })
  }
}

export class Vynel {
  constructor({ base = process.env.VYNEL_API, token = process.env.VYNEL_TOKEN, retries = 4 } = {}) {
    this.base = base.replace(/\/$/, '')
    this.token = token
    this.retries = retries
  }

  // Всё, что меняет данные, уходит с Idempotency-Key, и повторы несут тот же ключ.
  async request(method, path, { json, params, key } = {}) {
    const url = this.base + path + (params ? '?' + new URLSearchParams(params) : '')
    const headers = { Authorization: `Bearer ${this.token}` }
    if (json !== undefined) headers['Content-Type'] = 'application/json'
    if (method !== 'GET') headers['Idempotency-Key'] = key ?? randomUUID()
    for (let attempt = 0; ; attempt++) {
      let res
      try {
        res = await fetch(url, { method, headers, body: json !== undefined ? JSON.stringify(json) : undefined, signal: AbortSignal.timeout(15000) })
      } catch (e) {
        if (attempt >= this.retries) throw e
        await sleep(Math.min(2 ** attempt, 20) * 1000)
        continue
      }
      if ((res.status === 429 || res.status >= 500) && attempt < this.retries) {
        await sleep((res.status === 429 ? Number(res.headers.get('retry-after') ?? 1) : Math.min(2 ** attempt, 20)) * 1000)
        continue
      }
      const text = await res.text()
      const body = text ? JSON.parse(text) : null
      if (!res.ok) throw new VynelError(res.status, body?.error)
      return body
    }
  }

  me = () => this.request('GET', '/me')
  users = (params) => this.request('GET', '/users', { params })
  user = (id) => this.request('GET', `/users/${id}`)
  async findUser(by) { return (await this.users(by)).items[0] ?? null }
  createUser = (username, fields = {}, key) => this.request('POST', '/users', { json: { username, ...fields }, key })
  updateUser = (id, fields, key) => this.request('PATCH', `/users/${id}`, { json: fields, key })
  extend = (id, { months = 0, days = 0 }, key) => this.request('POST', `/users/${id}/extend`, { json: { months, days }, key })
  disable = (id) => this.request('POST', `/users/${id}/disable`)
  enable = (id) => this.request('POST', `/users/${id}/enable`)
  subscription = (id) => this.request('GET', `/users/${id}/subscription`)
  nodes = async () => (await this.request('GET', '/nodes')).items
  overview = () => this.request('GET', '/stats/overview')
  events = (after = 'now', types, limit = 100) => this.request('GET', '/events', { params: { after, limit, ...(types ? { types: types.join(',') } : {}) } })
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const api = new Vynel()
  const me = await api.me()
  console.log('токен:', me.token.name, me.token.scopes)
  console.log('пользователей:', (await api.users({ limit: 1 })).total)
}
