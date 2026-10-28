import { createContext, ReactNode, useCallback, useContext, useMemo, useState } from 'react'

// A form field and the lines of the JSON it fills. A field says where it lands (JSON paths of the
// inbound and of the subscription's connection point); the preview next to the form lights those
// lines up while the pointer is over the field or the field has focus.
export type Link = { inbound?: string[]; host?: string[] }

type Ctx = { active: Link | null; hover: (l: Link | null) => void; focus: (l: Link | null) => void }
const LinkCtx = createContext<Ctx | null>(null)

export function LinkProvider({ children }: { children: ReactNode }) {
  const [hovered, hover] = useState<Link | null>(null)
  const [focused, focus] = useState<Link | null>(null)
  const value = useMemo(() => ({ active: hovered ?? focused, hover, focus }), [hovered, focused])
  return <LinkCtx.Provider value={value}>{children}</LinkCtx.Provider>
}

// useActiveLink is the field the pointer or the keyboard is on right now.
export const useActiveLink = (): Link | null => useContext(LinkCtx)?.active ?? null

// useLinkHandlers gives the events to put on the element that wraps a field.
export function useLinkHandlers(link: Link | undefined) {
  const ctx = useContext(LinkCtx)
  const on = useCallback(() => link, [link])
  if (!ctx || !link) return {}
  return {
    onMouseEnter: () => ctx.hover(on()!),
    onMouseLeave: () => ctx.hover(null),
    onFocusCapture: () => ctx.focus(on()!),
    onBlurCapture: () => ctx.focus(null),
  }
}

// pathsWith lists the JSON paths whose string value uses ${NAME} (or $NAME) of a template variable.
export function pathsWith(src: unknown, name: string): string[] {
  const out: string[] = []
  const re = new RegExp('\\$\\{' + name + '\\}|\\$' + name + '\\b')
  const walk = (v: unknown, path: string) => {
    if (typeof v === 'string') {
      if (re.test(v)) out.push(path)
    } else if (Array.isArray(v)) {
      v.forEach((x, i) => walk(x, path ? `${path}.${i}` : String(i)))
    } else if (v && typeof v === 'object') {
      for (const [k, x] of Object.entries(v)) walk(x, path ? `${path}.${k}` : k)
    }
  }
  walk(src, '')
  return out
}

// Linked is a block that lights up its JSON lines while the pointer or the focus is inside it.
export function Linked({ link, className, children }: { link?: Link; className?: string; children: ReactNode }) {
  const h = useLinkHandlers(link)
  return <div className={className} {...h}>{children}</div>
}
