import { useEffect, useRef } from 'react'
import { EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter, drawSelection } from '@codemirror/view'
import { EditorState, Compartment } from '@codemirror/state'
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands'
import { HighlightStyle, syntaxHighlighting, bracketMatching, indentOnInput, foldGutter } from '@codemirror/language'
import { searchKeymap, highlightSelectionMatches } from '@codemirror/search'
import { yaml } from '@codemirror/lang-yaml'
import { json } from '@codemirror/lang-json'
import { tags as t } from '@lezer/highlight'

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
  { tag: [t.propertyName, t.definition(t.propertyName)], color: '#75e0d7' },
  { tag: [t.string, t.special(t.string)], color: '#ff94ba' },
  { tag: [t.number, t.bool, t.null, t.atom], color: '#f5c572' },
  { tag: [t.keyword, t.operator], color: '#c2b1f8' },
  { tag: [t.comment, t.lineComment, t.blockComment], color: '#778990', fontStyle: 'italic' },
  { tag: [t.punctuation, t.separator, t.bracket], color: '#9eb2ba' },
  { tag: t.invalid, color: '#fb8276' },
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
