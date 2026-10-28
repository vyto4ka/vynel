import { useEffect, useRef, useState } from 'react'
import { EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter, drawSelection } from '@codemirror/view'
import { EditorState, Compartment } from '@codemirror/state'
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands'
import { HighlightStyle, syntaxHighlighting, bracketMatching, indentOnInput, foldGutter, indentUnit } from '@codemirror/language'
import { closeBrackets, closeBracketsKeymap } from '@codemirror/autocomplete'
import { linter, lintGutter, Diagnostic } from '@codemirror/lint'
import { searchKeymap, highlightSelectionMatches } from '@codemirror/search'
import { yaml } from '@codemirror/lang-yaml'
import { json, jsonLanguage } from '@codemirror/lang-json'
import { tags as tg } from '@lezer/highlight'
import { parseDocument } from 'yaml'
import { Key, t } from './i18n'

// Code editor in the panel palette: teal keys, pink strings, an inset mantle background.
const theme = EditorView.theme({
  '&': { backgroundColor: 'var(--mantle)', color: 'var(--on-surface)', fontSize: '12.5px', borderRadius: 'var(--shape-md)', overflow: 'hidden', boxShadow: 'inset 0 0 0 1px var(--outline-variant)' },
  '&.cm-focused': { outline: 'none', boxShadow: 'inset 0 0 0 2px var(--primary)' },
  '.cm-scroller': { fontFamily: '"JetBrains Mono", "Roboto Mono", ui-monospace, SFMono-Regular, Menlo, Consolas, monospace', lineHeight: '1.55' },
  '.cm-content': { caretColor: 'var(--primary)', padding: '10px 0' },
  '.cm-gutters': { backgroundColor: 'var(--crust)', color: 'var(--muted)', border: 'none' },
  '.cm-activeLine': { backgroundColor: 'rgba(117, 224, 215, 0.06)' },
  '.cm-activeLineGutter': { backgroundColor: 'rgba(117, 224, 215, 0.10)', color: 'var(--primary)' },
  '.cm-selectionBackground, &.cm-focused .cm-selectionBackground, ::selection': { backgroundColor: 'rgba(255, 148, 186, 0.26) !important' },
  '.cm-cursor': { borderLeftColor: 'var(--primary)' },
  '.cm-matchingBracket': { backgroundColor: 'rgba(117, 224, 215, 0.22)', outline: 'none' },
  '.cm-selectionMatch': { backgroundColor: 'rgba(255, 148, 186, 0.14)' },
  '.cm-foldGutter span': { color: 'var(--muted)' },
  '.cm-panels': { backgroundColor: 'var(--container-high)', color: 'var(--on-surface)' },
  '.cm-tooltip': { backgroundColor: 'var(--container-highest)', border: 'none', borderRadius: 'var(--shape-sm)', boxShadow: 'var(--elev-2)' },
}, { dark: true })

const highlight = HighlightStyle.define([
  { tag: [tg.propertyName, tg.definition(tg.propertyName)], color: '#75e0d7' },
  { tag: [tg.string, tg.special(tg.string)], color: '#ff94ba' },
  { tag: [tg.number, tg.bool, tg.null, tg.atom], color: '#f5c572' },
  { tag: [tg.keyword, tg.operator], color: '#c2b1f8' },
  { tag: [tg.comment, tg.lineComment, tg.blockComment], color: '#778990', fontStyle: 'italic' },
  { tag: [tg.punctuation, tg.separator, tg.bracket], color: '#9eb2ba' },
  { tag: tg.invalid, color: '#fb8276' },
])

export type CodeLang = 'yaml' | 'json'

// SyntaxError is where a text stops being valid, 1-based; from/to are offsets into the text.
export type SyntaxProblem = { message: string; from: number; to: number; line: number; col: number }

const jsonMessages: [RegExp, Key][] = [
  [/unexpected end|end of (json|data)|unterminated/i, 'code.json_cut_off'],
  [/double-quoted property name|property name/i, 'code.json_property_name'],
  [/after property value|after array element|expected ','/i, 'code.json_missing_comma'],
  [/expected ':'|after property name/i, 'code.json_missing_colon'],
  [/non-whitespace|after json/i, 'code.json_trailing_text'],
  [/control character/i, 'code.json_control_char'],
  [/bad escaped|escape/i, 'code.json_bad_escape'],
]

// ruJSONMessage words the engine's message for the admin; what it cannot place stays as it is.
function ruJSONMessage(raw: string): string {
  const hit = jsonMessages.find(([re]) => re.test(raw))
  if (hit) return t(hit[1])
  const tok = /unexpected token '?([^'\s,]+)/i.exec(raw)
  return tok ? t('code.json_unexpected', { token: tok[1] }) : raw
}

function lineCol(text: string, pos: number): { line: number; col: number } {
  const before = text.slice(0, pos)
  const line = before.split('\n').length
  return { line, col: pos - (before.lastIndexOf('\n') + 1) + 1 }
}

// checkSyntax tells why a text is not valid JSON or YAML; null when it is (or is empty).
export function checkSyntax(lang: CodeLang, text: string): SyntaxProblem | null {
  if (!text.trim()) return null
  if (lang === 'json') {
    try {
      JSON.parse(text)
      return null
    } catch (e: any) {
      const msg = String(e.message || e)
      // The error node of the syntax tree sits where the text goes wrong; the engine's own message
      // names the place only for some mistakes (and differently in each browser).
      let pos = -1
      jsonLanguage.parser.parse(text).iterate({
        enter(n) {
          if (pos >= 0) return false
          if (n.type.isError) pos = n.from
        },
      })
      const at = /position (\d+)/.exec(msg)
      const lc = /line (\d+) column (\d+)/.exec(msg)
      if (pos < 0 && at) pos = +at[1]
      if (pos < 0 && lc) pos = text.split('\n').slice(0, +lc[1] - 1).reduce((n, l) => n + l.length + 1, 0) + +lc[2] - 1
      if (pos < 0) pos = /unexpected end|end of/i.test(msg) ? text.length : 0
      pos = Math.max(0, Math.min(pos, text.length))
      return { message: ruJSONMessage(msg), from: pos, to: Math.min(text.length, pos + 1), ...lineCol(text, pos) }
    }
  }
  const doc = parseDocument(text)
  const err = doc.errors[0]
  if (!err) return null
  const [from, to] = err.pos
  const first = err.message.split('\n')[0].replace(/ at line \d+, column \d+:?$/, '')
  return { message: first, from, to: Math.max(to, from), ...lineCol(text, from) }
}

// formatText re-indents a text: JSON in two spaces, YAML with its comments kept.
export function formatText(lang: CodeLang, text: string): { text: string } | { error: SyntaxProblem } {
  const bad = checkSyntax(lang, text)
  if (bad) return { error: bad }
  if (!text.trim()) return { text }
  if (lang === 'json') return { text: JSON.stringify(JSON.parse(text), null, 2) }
  return { text: parseDocument(text).toString({ indent: 2, lineWidth: 0 }).replace(/\n+$/, '') }
}

function lintSource(lang: CodeLang) {
  return (view: EditorView): Diagnostic[] => {
    const text = view.state.doc.toString()
    const p = checkSyntax(lang, text)
    return p ? [{ from: p.from, to: Math.max(p.to, Math.min(p.from + 1, text.length)), severity: 'error', message: p.message }] : []
  }
}

type Status = { line: number; col: number; problem: SyntaxProblem | null; size: number }

export function CodeEditor({ value, onChange, lang, height = 360, readOnly, onValidity }: {
  value: string
  onChange?: (v: string) => void
  lang: CodeLang
  height?: number | string
  readOnly?: boolean
  // Called with the reason when the text is not valid, null when it is: for a Save button.
  onValidity?: (error: string | null) => void
}) {
  const host = useRef<HTMLDivElement>(null)
  const view = useRef<EditorView | null>(null)
  const onChangeRef = useRef(onChange)
  onChangeRef.current = onChange
  const onValidityRef = useRef(onValidity)
  onValidityRef.current = onValidity
  const ro = useRef(new Compartment())
  const [st, setSt] = useState<Status>({ line: 1, col: 1, problem: null, size: value.length })
  const [copied, setCopied] = useState(false)

  const refresh = (v: EditorView) => {
    const text = v.state.doc.toString()
    const head = v.state.selection.main.head
    const line = v.state.doc.lineAt(head)
    const problem = readOnly ? null : checkSyntax(lang, text)
    setSt({ line: line.number, col: head - line.from + 1, problem, size: text.length })
    onValidityRef.current?.(problem ? t('code.problem', { line: problem.line, col: problem.col, message: problem.message }) : null)
  }

  // Re-indent the whole text; the cursor stays on the same line. A text with an error is left alone.
  const format = (v: EditorView): boolean => {
    const text = v.state.doc.toString()
    const r = formatText(lang, text)
    if ('error' in r) {
      v.dispatch({ selection: { anchor: r.error.from }, scrollIntoView: true })
      v.focus()
      return true
    }
    if (r.text !== text) {
      const line = v.state.doc.lineAt(v.state.selection.main.head).number
      v.dispatch({ changes: { from: 0, to: text.length, insert: r.text } })
      const at = v.state.doc.line(Math.min(line, v.state.doc.lines))
      v.dispatch({ selection: { anchor: at.to }, scrollIntoView: true })
    }
    return true
  }

  useEffect(() => {
    const state = EditorState.create({
      doc: value,
      extensions: [
        lineNumbers(), foldGutter(), history(), drawSelection(), indentOnInput(), bracketMatching(), closeBrackets(),
        highlightActiveLine(), highlightActiveLineGutter(), highlightSelectionMatches(),
        indentUnit.of('  '),
        keymap.of([
          { key: 'Shift-Alt-f', run: format }, // как в VS Code
          indentWithTab, ...closeBracketsKeymap, ...defaultKeymap, ...historyKeymap, ...searchKeymap,
        ]),
        lang === 'yaml' ? yaml() : json(),
        syntaxHighlighting(highlight), theme,
        EditorView.lineWrapping,
        ...(readOnly ? [] : [linter(lintSource(lang), { delay: 250 }), lintGutter()]),
        // Ctrl+A is the editor's own while it has focus, so a read-only one stays focusable too.
        ro.current.of(EditorState.readOnly.of(!!readOnly)),
        // A whole config pasted over an empty editor or over everything comes out indented.
        EditorView.domEventHandlers({
          paste(e, v) {
            const text = e.clipboardData?.getData('text/plain') ?? ''
            const all = v.state.selection.ranges.length === 1 && v.state.selection.main.from === 0 && v.state.selection.main.to === v.state.doc.length
            if (readOnly || !text.trim() || !(all || v.state.doc.length === 0)) return false
            const r = formatText(lang, text)
            if ('error' in r) return false
            e.preventDefault()
            v.dispatch(v.state.replaceSelection(r.text))
            return true
          },
        }),
        EditorView.updateListener.of((u) => {
          if (u.docChanged) onChangeRef.current?.(u.state.doc.toString())
          if (u.docChanged || u.selectionSet) refresh(u.view)
        }),
        EditorView.theme({ '&': { height: typeof height === 'number' ? `${height}px` : height } }),
      ],
    })
    view.current = new EditorView({ state, parent: host.current! })
    refresh(view.current)
    return () => view.current?.destroy()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [lang])

  // Значение поменялось снаружи (загрузка, сброс) — заменить текст, не трогая историю правок вручную.
  useEffect(() => {
    const v = view.current
    if (v && v.state.doc.toString() !== value) {
      v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: value } })
    }
  }, [value])

  useEffect(() => {
    view.current?.dispatch({ effects: ro.current.reconfigure(EditorState.readOnly.of(!!readOnly)) })
  }, [readOnly])

  const copy = () => {
    navigator.clipboard?.writeText(view.current?.state.doc.toString() ?? '').then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    }, () => {})
  }
  const jump = () => {
    const v = view.current
    if (v && st.problem) {
      v.dispatch({ selection: { anchor: st.problem.from }, scrollIntoView: true })
      v.focus()
    }
  }

  return (
    <div className="code-box">
      <div ref={host} className="code-editor" />
      <div className={'code-status' + (st.problem ? ' bad' : '')}>
        {readOnly ? <span className="muted">{t('code.read_only')}</span> : st.problem ? (
          <button type="button" className="code-problem" onClick={jump} title={t('code.go_to_error')}>
            {t('code.problem_marked', { line: st.problem.line, col: st.problem.col, message: st.problem.message })}
          </button>
        ) : <span className="code-ok">{st.size === 0 ? t('code.empty') : t('code.valid', { lang: lang === 'json' ? 'JSON' : 'YAML' })}</span>}
        <span className="spacer" />
        <span className="muted code-pos">{t('code.position', { line: st.line, col: st.col })}</span>
        {!readOnly && <button type="button" className="btn sm ghost" onClick={() => view.current && format(view.current)} title={t('code.format_title')}>{t('code.format')}</button>}
        <button type="button" className="btn sm ghost" onClick={copy}>{copied ? t('code.copied') : t('code.copy')}</button>
      </div>
    </div>
  )
}

// JSON с отступами для показа.
export function pretty(v: unknown): string {
  return JSON.stringify(v, null, 2)
}

// Разобрать JSON-объект: null — пусто, ошибка — строка.
export function parseObject(text: string): { value: Record<string, unknown> | null; error?: string } {
  if (!text.trim()) return { value: null }
  try {
    const v = JSON.parse(text)
    if (v === null || typeof v !== 'object' || Array.isArray(v)) return { value: null, error: t('code.need_object') }
    return { value: v as Record<string, unknown> }
  } catch (e: any) {
    return { value: null, error: e.message }
  }
}
