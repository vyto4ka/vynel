import { useMemo, useState } from 'react'
import { Icon } from '../ui'
import { t } from '../i18n'
import { GeoExplorerModal } from './GeoExplorerModal'

interface Props {
  value: string
  onChange: (next: string) => void
  placeholder?: string
  rows?: number
}

function parseGeoChip(line: string): { file: string; code: string; label: string } | null {
  const s = line.trim()
  if (!s) return null
  if (s.startsWith('geosite:')) {
    const rest = s.slice('geosite:'.length)
    const code = rest.split('@')[0]
    if (code) return { file: 'geosite.dat', code, label: s }
  }
  if (s.startsWith('geoip:')) {
    const code = s.slice('geoip:'.length).replace(/^!/, '')
    if (code) return { file: 'geoip.dat', code, label: s }
  }
  if (s.startsWith('ext-domain:') || s.startsWith('ext-ip:')) {
    const rest = s.slice(s.indexOf(':') + 1)
    const idx = rest.indexOf(':')
    if (idx > 0) {
      const file = rest.slice(0, idx)
      const code = rest.slice(idx + 1).split('@')[0].replace(/^!/, '')
      if (file && code) return { file, code, label: s }
    }
  }
  return null
}

export function RuleListInput({ value, onChange, placeholder, rows = 6 }: Props) {
  const [explorerOpen, setExplorerOpen] = useState(false)
  const [target, setTarget] = useState<{ file: string; code: string } | null>(null)

  const lines = useMemo(
    () => value.split('\n').map((l) => l.trim()).filter(Boolean),
    [value],
  )

  const geoChips = useMemo(() => {
    const out: { file: string; code: string; label: string }[] = []
    for (const l of lines) {
      const c = parseGeoChip(l)
      if (c) out.push(c)
    }
    return out
  }, [lines])

  const toggleRule = (rule: string) => {
    const low = rule.trim().toLowerCase()
    const exists = lines.some((l) => l.toLowerCase() === low)
    const next = exists
      ? lines.filter((l) => l.toLowerCase() !== low)
      : [...lines, rule.trim()]
    onChange(next.join('\n'))
  }

  return (
    <div>
      <div className="row" style={{ gap: 8, alignItems: 'flex-start' }}>
        <textarea
          className="mono"
          rows={rows}
          style={{ flex: 1 }}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={placeholder || t('warp.rules_placeholder')}
        />
        <button
          type="button"
          className="btn"
          style={{ whiteSpace: 'nowrap' }}
          onClick={() => { setTarget(null); setExplorerOpen(true) }}
        >
          <Icon name="search" /> {t('warp.pick_geo')}
        </button>
      </div>

      {geoChips.length > 0 && (
        <div className="row" style={{ gap: 6, flexWrap: 'wrap', marginTop: 8 }}>
          {geoChips.map((c) => (
            <button
              key={c.label}
              type="button"
              className="btn sm"
              title={t('warp.geo_browse')}
              onClick={() => { setTarget({ file: c.file, code: c.code }); setExplorerOpen(true) }}
            >
              <Icon name="globe" size={13} /> <span className="mono">{c.label}</span>
            </button>
          ))}
        </div>
      )}

      <GeoExplorerModal
        open={explorerOpen}
        onClose={() => setExplorerOpen(false)}
        selected={lines}
        onToggleRule={toggleRule}
        initialTarget={target}
      />
    </div>
  )
}
