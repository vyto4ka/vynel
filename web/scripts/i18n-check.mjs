// Checks the interface catalogs: every key is used somewhere in src, and no catalog of another
// language keeps Russian text. (That a language has exactly the keys of Russian is checked by tsc.)
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, basename } from 'node:path'

const src = new URL('../src/', import.meta.url).pathname
const localesDir = join(src, 'i18n/locales')

function walk(dir) {
  return readdirSync(dir).flatMap((f) => {
    const p = join(dir, f)
    return statSync(p).isDirectory() ? walk(p) : [p]
  })
}

const code = walk(src)
  .filter((f) => /\.tsx?$/.test(f) && !f.startsWith(localesDir))
  .map((f) => readFileSync(f, 'utf8'))
  .join('\n')

const keys = (file) => [...readFileSync(file, 'utf8').matchAll(/^ {2}([A-Za-z0-9_]+):/gm)].map((m) => m[1])
// Keys built at run time ('xray.origin_' + kind): the prefix counts as use.
const dynamic = [...code.matchAll(/['"]([a-z0-9]+\.[a-z0-9_]+_)['"]\s*\+/g)].map((m) => m[1])

// Russian examples that belong to the text (Russian domains).
const allowedRussian = new Set(['routing.lists_take_one_entry_per', 'routing.russian_sites_geosite_category'])

let bad = 0
for (const lang of readdirSync(localesDir)) {
  const dir = join(localesDir, lang)
  if (!statSync(dir).isDirectory()) continue
  for (const f of readdirSync(dir)) {
    if (f === 'index.ts') continue
    const ns = basename(f, '.ts')
    const text = readFileSync(join(dir, f), 'utf8')
    if (lang === 'ru') {
      for (const k of keys(join(dir, f))) {
        const full = `${ns}.${k}`
        if (!code.includes(`'${full}'`) && !code.includes(`"${full}"`) && !dynamic.some((d) => full.startsWith(d))) {
          console.error(`unused key: ${full}`)
          bad++
        }
      }
    } else if (/[А-Яа-яЁё]/.test(text)) {
      for (const [i, line] of text.split('\n').entries()) {
        const key = line.match(/^ {2}([A-Za-z0-9_]+):/)?.[1]
        if (/[А-Яа-яЁё]/.test(line) && !allowedRussian.has(`${ns}.${key}`)) {
          console.error(`${lang}/${f}:${i + 1}: Russian text in a ${lang} catalog: ${line.trim().slice(0, 80)}`)
          bad++
        }
      }
    }
  }
}
if (bad) process.exit(1)
console.log('i18n catalogs are fine')
