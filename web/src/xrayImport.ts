// Takes a whole Xray config (from Remnawave, 3x-ui or any server) apart for the panel: what becomes
// the common config of the nodes and what the panel keeps for itself. Nothing is renamed or
// rewritten beyond that: what is kept is kept as it was written.

export interface ImportedInbound { tag?: string; port?: unknown; protocol?: string }

export interface ImportReport {
  base: Record<string, any>
  kept: string[]
  inbounds: ImportedInbound[]
  inboundsRaw: unknown[]
  panelKeys: string[] // sections the panel writes itself (api, stats, policy, log)
  apiRules: number // routing rules to the «api» outbound, which the panel's own API replaces
  noDirect?: string // the outbound that looks like DIRECT but is named otherwise
}

const panelOwned = ['api', 'stats', 'policy', 'log']

export function splitConfig(text: string): { error?: string; report?: ImportReport } {
  let cfg: any
  try {
    cfg = JSON.parse(text)
  } catch (e: any) {
    return { error: String(e.message || e) }
  }
  if (!cfg || typeof cfg !== 'object' || Array.isArray(cfg)) return { error: 'object' }
  const base: Record<string, any> = {}
  const panelKeys: string[] = []
  for (const [k, v] of Object.entries(cfg)) {
    if (k === 'inbounds') continue
    if (panelOwned.includes(k)) { panelKeys.push(k); continue }
    base[k] = v
  }
  let apiRules = 0
  const routing = base.routing
  if (routing && Array.isArray(routing.rules)) {
    const rules = routing.rules.filter((r: any) => {
      const api = r && (r.outboundTag === 'api' || (Array.isArray(r.inboundTag) && r.inboundTag.length === 1 && r.inboundTag[0] === 'api'))
      if (api) apiRules++
      return !api
    })
    base.routing = { ...routing, rules }
  }
  const raw: unknown[] = Array.isArray(cfg.inbounds) ? cfg.inbounds : []
  const inbounds = raw.map((i: any) => ({ tag: i?.tag, port: i?.port, protocol: i?.protocol }))
  let noDirect: string | undefined
  if (Array.isArray(base.outbounds) && !base.outbounds.some((o: any) => o?.tag === 'DIRECT')) {
    noDirect = base.outbounds.find((o: any) => typeof o?.tag === 'string' && o.tag.toLowerCase() === 'direct')?.tag
  }
  return { report: { base, kept: Object.keys(base), inbounds, inboundsRaw: raw, panelKeys, apiRules, noDirect } }
}
