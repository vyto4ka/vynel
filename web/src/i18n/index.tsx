import { Fragment, ReactNode, createElement } from 'react'
import ru from './locales/ru'
import type { Plural } from './plural'

// Interface language. Russian is the source language and the fallback; every other language is a
// file in locales/<code>/ that must have the same keys as Russian (TypeScript checks it), loaded
// on demand. To add a language: copy locales/en, translate, and add a line to `languages`.

export { p } from './plural'

export type Catalog = typeof ru
type Leaf = string | Plural
type Paths<T, P extends string = ''> = { [K in keyof T & string]: T[K] extends Leaf ? `${P}${K}` : Paths<T[K], `${P}${K}.`> }[keyof T & string]
export type Key = Paths<Catalog>

export const languages = [
  { code: 'ru', name: 'Русский', short: 'RU', locale: 'ru-RU', load: async () => ru },
  { code: 'en', name: 'English', short: 'EN', locale: 'en-GB', load: async () => (await import('./locales/en')).default as Catalog },
] as const
export type Lang = (typeof languages)[number]['code']

const catalogs: Partial<Record<Lang, Catalog>> = { ru }
let current: Lang = 'ru'

export function detectLang(): Lang {
  try {
    const saved = localStorage.getItem('vynel.lang')
    if (languages.some((l) => l.code === saved)) return saved as Lang
  } catch { /* приватный режим */ }
  const nav = (navigator.language || 'ru').slice(0, 2).toLowerCase()
  return (languages.find((l) => l.code === nav)?.code ?? 'en') as Lang
}

// setLang loads the language and makes it current; the app re-renders from the top afterwards.
export async function setLang(code: Lang): Promise<void> {
  const l = languages.find((x) => x.code === code) ?? languages[0]
  if (!catalogs[l.code]) catalogs[l.code] = await l.load()
  current = l.code
  document.documentElement.lang = l.code
  try { localStorage.setItem('vynel.lang', l.code) } catch { /* приватный режим */ }
}

export const lang = (): Lang => current
const locale = () => languages.find((l) => l.code === current)!.locale

function lookup(c: Catalog | undefined, key: string): Leaf | undefined {
  let v: any = c
  for (const part of key.split('.')) {
    v = v?.[part]
    if (v === undefined) return undefined
  }
  return v
}

export type Vars = Record<string, string | number | null | undefined>

function pick(msg: Leaf, vars?: Vars): string {
  if (typeof msg === 'string') return msg
  const n = Number(vars?.n ?? 0)
  const form = new Intl.PluralRules(locale()).select(n)
  return msg[form] ?? msg.other
}

function raw(key: Key): Leaf {
  const m = lookup(catalogs[current], key) ?? lookup(ru, key)
  if (m === undefined) {
    console.warn('i18n: no message', key)
    return key
  }
  return m
}

// t returns the message of the current language; {name} is replaced from vars.
export function t(key: Key, vars?: Vars): string {
  const msg = pick(raw(key), vars)
  return vars ? msg.replace(/\{(\w+)\}/g, (m, k) => (k in vars ? String(vars[k] ?? '') : m)) : msg
}

type Tags = Record<string, (children: ReactNode) => ReactNode>
const defaultTags: Tags = {
  b: (c) => <b>{c}</b>,
  i: (c) => <i>{c}</i>,
  c: (c) => <span className="mono">{c}</span>,
  code: (c) => <code>{c}</code>,
  muted: (c) => <span className="muted">{c}</span>,
}

// rich is t() for messages with markup: <b>bold</b>, <c>mono</c>, <code>…</code> and {values} that
// are React nodes. Own tags are passed in `tags`.
export function rich(key: Key, vars?: Record<string, ReactNode>, tags?: Tags): ReactNode {
  const n = typeof vars?.n === 'number' ? { n: vars.n } : undefined
  const msg = pick(raw(key), n)
  const all = { ...defaultTags, ...tags }
  const tokens = msg.split(/(<\/?\w+\/?>|\{\w+\})/g).filter((s) => s !== '')
  let i = 0
  const parse = (until?: string): ReactNode[] => {
    const out: ReactNode[] = []
    while (i < tokens.length) {
      const tok = tokens[i++]
      const close = /^<\/(\w+)>$/.exec(tok)
      if (close) {
        if (close[1] === until) return out
        out.push(tok)
        continue
      }
      const open = /^<(\w+)>$/.exec(tok)
      if (open && all[open[1]]) {
        const children = parse(open[1])
        out.push(createElement(Fragment, { key: out.length }, all[open[1]](children)))
        continue
      }
      if (/^<br\/>$/.test(tok)) {
        out.push(<br key={out.length} />)
        continue
      }
      const v = /^\{(\w+)\}$/.exec(tok)
      if (v && vars && v[1] in vars) {
        out.push(createElement(Fragment, { key: out.length }, vars[v[1]]))
        continue
      }
      out.push(tok)
    }
    return out
  }
  return <>{parse()}</>
}

// Trans is rich() as a component, for JSX.
export function Trans({ k, v, tags }: { k: Key; v?: Record<string, ReactNode>; tags?: Tags }) {
  return <>{rich(k, v, tags)}</>
}

// ---------- числа, даты, размеры ----------

export const fmtNumber = (n: number, o?: Intl.NumberFormatOptions) => new Intl.NumberFormat(locale(), o).format(n)
export const fmtDate = (d: Date, o: Intl.DateTimeFormatOptions) => d.toLocaleString(locale(), o)
