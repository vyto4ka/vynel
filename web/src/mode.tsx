import { createContext, ReactNode, useCallback, useContext, useState } from 'react'

// Interface mode. «Simple» is forms only; «Advanced» adds the raw Xray config next to them:
// JSON editors, the config preview, templates and the «Xray» page. It only changes what is shown.
export type Mode = 'simple' | 'advanced'

const key = 'vynel.mode'
const Ctx = createContext<{ mode: Mode; setMode: (m: Mode) => void }>({ mode: 'simple', setMode: () => {} })

function load(): Mode {
  try {
    return localStorage.getItem(key) === 'advanced' ? 'advanced' : 'simple'
  } catch {
    return 'simple'
  }
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
export const useAdvanced = () => useContext(Ctx).mode === 'advanced'
