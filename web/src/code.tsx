import { useEffect, useRef } from 'react'
import { EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter, drawSelection } from '@codemirror/view'
import { EditorState, Compartment } from '@codemirror/state'
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands'
import { HighlightStyle, syntaxHighlighting, bracketMatching, indentOnInput, foldGutter } from '@codemirror/language'
import { searchKeymap, highlightSelectionMatches } from '@codemirror/search'
import { yaml } from '@codemirror/lang-yaml'
import { json } from '@codemirror/lang-json'
import { tags as t } from '@lezer/highlight'

// Редактор кода в цветах панели: бирюзовые ключи, розовые строки, серый фон.
const theme = EditorView.theme({
  '&': { backgroundColor: 'var(--bg-2)', color: 'var(--text)', fontSize: '12.5px', border: '1px solid var(--border-2)', borderRadius: 'var(--radius-sm)' },
  '&.cm-focused': { outline: 'none', borderColor: 'var(--miku)', boxShadow: '0 0 0 3px var(--miku-soft)' },
  '.cm-scroller': { fontFamily: '"JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, Consolas, monospace', lineHeight: '1.55' },
  '.cm-content': { caretColor: 'var(--miku-2)', padding: '8px 0' },
  '.cm-gutters': { backgroundColor: 'var(--surface)', color: 'var(--muted)', border: 'none', borderRight: '1px solid var(--border)' },
  '.cm-activeLine': { backgroundColor: 'rgba(57, 197, 187, 0.06)' },
  '.cm-activeLineGutter': { backgroundColor: 'rgba(57, 197, 187, 0.10)', color: 'var(--miku-2)' },
  '.cm-selectionBackground, &.cm-focused .cm-selectionBackground, ::selection': { backgroundColor: 'rgba(255, 95, 162, 0.28) !important' },
  '.cm-cursor': { borderLeftColor: 'var(--miku-2)' },
  '.cm-matchingBracket': { backgroundColor: 'rgba(57, 197, 187, 0.22)', outline: 'none' },
  '.cm-selectionMatch': { backgroundColor: 'rgba(255, 95, 162, 0.14)' },
  '.cm-foldGutter span': { color: 'var(--muted)' },
}, { dark: true })

const highlight = HighlightStyle.define([
  { tag: [t.propertyName, t.definition(t.propertyName)], color: '#5fd8cf' },
  { tag: [t.string, t.special(t.string)], color: '#ff8dbd' },
  { tag: [t.number, t.bool, t.null, t.atom], color: '#f2b84b' },
  { tag: [t.keyword, t.operator], color: '#b49cff' },
  { tag: [t.comment, t.lineComment, t.blockComment], color: '#6b7c82', fontStyle: 'italic' },
  { tag: [t.punctuation, t.separator, t.bracket], color: '#8a9aa0' },
  { tag: t.invalid, color: '#ff6b6b' },
])

export function CodeEditor({ value, onChange, lang, height = 360, readOnly }: {
  value: string
  onChange?: (v: string) => void
  lang: 'yaml' | 'json'
  height?: number | string
  readOnly?: boolean
}) {
  const host = useRef<HTMLDivElement>(null)
  const view = useRef<EditorView | null>(null)
  const onChangeRef = useRef(onChange)
  onChangeRef.current = onChange
  const ro = useRef(new Compartment())

  useEffect(() => {
    const state = EditorState.create({
      doc: value,
      extensions: [
        lineNumbers(), foldGutter(), history(), drawSelection(), indentOnInput(), bracketMatching(),
        highlightActiveLine(), highlightActiveLineGutter(), highlightSelectionMatches(),
        keymap.of([indentWithTab, ...defaultKeymap, ...historyKeymap, ...searchKeymap]),
        lang === 'yaml' ? yaml() : json(),
        syntaxHighlighting(highlight), theme,
        EditorView.lineWrapping,
        ro.current.of([EditorState.readOnly.of(!!readOnly), EditorView.editable.of(!readOnly)]),
        EditorView.updateListener.of((u) => {
          if (u.docChanged) onChangeRef.current?.(u.state.doc.toString())
        }),
        EditorView.theme({ '&': { height: typeof height === 'number' ? `${height}px` : height } }),
      ],
    })
    view.current = new EditorView({ state, parent: host.current! })
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
    view.current?.dispatch({ effects: ro.current.reconfigure([EditorState.readOnly.of(!!readOnly), EditorView.editable.of(!readOnly)]) })
  }, [readOnly])

  return <div ref={host} className="code-editor" />
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
    if (v === null || typeof v !== 'object' || Array.isArray(v)) return { value: null, error: 'нужен JSON-объект: { … }' }
    return { value: v as Record<string, unknown> }
  } catch (e: any) {
    return { value: null, error: e.message }
  }
}
