import { createContext, ReactNode, useCallback, useContext, useState } from 'react'

// Interface mode: how much is shown. «Лёгкий» is the guided path without Xray terms
// (no profiles, templates or inbounds); «Стандарт» is the full product without raw
// JSON; «Про» adds raw configs, profile templates, headers and the «Xray» page.
// It only changes what is shown — the data is the same.
export type Mode = 'light' | 'standard' | 'pro'

const key = 'vynel.mode'
const Ctx = createContext<{ mode: Mode; setMode: (m: Mode) => void }>({ mode: 'standard', setMode: () => {} })

// rank orders modes by how much they show.
export const modeRank: Record<Mode, number> = { light: 0, standard: 1, pro: 2 }

function load(): Mode {
  try {
    const v = localStorage.getItem(key)
    // Старые значения: simple → standard, advanced → pro.
    if (v === 'light' || v === 'standard' || v === 'pro') return v
    if (v === 'advanced') return 'pro'
  } catch {
    /* приватный режим */
  }
  return 'standard'
}

export function ModeProvider({ children }: { children: ReactNode }) {
  const [mode, set] = useState<Mode>(load)
  const setMode = useCallback((m: Mode) => {
    set(m)
    try { localStorage.setItem(key, m) } catch { /* приватный режим */ }
  }, [])
  return <Ctx.Provider value={{ mode, setMode }}>{children}</Ctx.Provider>
}

export const useMode = () => useContext(Ctx)

// atLeast(m) — видимость «от уровня m и выше»: pro ≥ standard ≥ light.
export function useAtLeast(m: Mode): boolean {
  return modeRank[useContext(Ctx).mode] >= modeRank[m]
}

// Обратная совместимость: «расширенный» = Про.
export const useAdvanced = () => useContext(Ctx).mode === 'pro'

// Флаг «режим уже выбирали» (экран первого входа показывается один раз).
const chosenKey = 'vynel.mode.chosen'
export function modeChosen(): boolean {
  try { return localStorage.getItem(chosenKey) === '1' } catch { return true }
}
export function markModeChosen() {
  try { localStorage.setItem(chosenKey, '1') } catch { /* приватный режим */ }
}

// Флаг «я переехал с другой панели»: меняет чеклист на дашборде.
const migrantKey = 'vynel.migrant'
export function isMigrant(): boolean {
  try { return localStorage.getItem(migrantKey) === '1' } catch { return false }
}
export function setMigrant(v: boolean) {
  try { v ? localStorage.setItem(migrantKey, '1') : localStorage.removeItem(migrantKey) } catch { /* приватный режим */ }
}
