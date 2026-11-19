import { createContext, ReactNode, useCallback, useContext, useState } from 'react'

// Interface mode: two honest modes without the middle clutter.
// «Обычный» ('basic') is a clean daily dashboard (users, groups, tariffs, nodes, subscription page & simple routes);
// «Инженерный» ('engineer') unlocks full Config-First control (Xray base per node, Raw Inbound,
// compiled xray.json + nginx.conf + diff, protocol blueprints, client YAML/JSON configs and live subscription simulator).
export type Mode = 'basic' | 'engineer'

const key = 'vynel.mode'
const Ctx = createContext<{ mode: Mode; setMode: (m: Mode) => void }>({ mode: 'basic', setMode: () => {} })

export const modeRank: Record<Mode, number> = { basic: 0, engineer: 1 }

function load(): Mode {
  try {
    const v = localStorage.getItem(key)
    if (v === 'basic' || v === 'engineer') return v
    // Миграция со старых значений: pro / advanced → engineer, остальное → basic.
    if (v === 'pro' || v === 'advanced') return 'engineer'
  } catch {
    /* приватный режим */
  }
  return 'basic'
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
export const useEngineer = () => useContext(Ctx).mode === 'engineer'
export const useAdvanced = useEngineer

export function useAtLeast(m: Mode): boolean {
  return modeRank[useContext(Ctx).mode] >= modeRank[m]
}

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
