// MarkdownEditor — a controlled CodeMirror 6 markdown editor with a formatting
// toolbar and an optional live-preview pane.
//
// Decision D2 (spec `.watchfire/specs/v8-inferno.md`, GitHub #22): source +
// preview, NOT WYSIWYG. The values edited here round-trip through YAML
// `prompt:` / `acceptance_criteria:` block scalars and project definitions, so
// exact markdown/whitespace must be preserved — a WYSIWYG that re-serialises
// the document would corrupt them. CodeMirror keeps the source verbatim; the
// preview reuses the in-tree `MarkdownView` renderer read-only.
//
// The component is fully controlled (value / onChange) so the existing
// debounced-autosave (DefinitionTab) and modal-save (TaskModal) semantics in
// the consumers (#0109) are unchanged.

import { useEffect, useRef, useState, type ReactNode } from 'react'
import { EditorState, Compartment } from '@codemirror/state'
import {
  EditorView,
  keymap,
  placeholder as cmPlaceholder,
  drawSelection,
  highlightSpecialChars
} from '@codemirror/view'
import { defaultKeymap, history, historyKeymap } from '@codemirror/commands'
import { markdown } from '@codemirror/lang-markdown'
import { HighlightStyle, syntaxHighlighting } from '@codemirror/language'
import { tags as t } from '@lezer/highlight'
import {
  Bold,
  Italic,
  Code,
  Link as LinkIcon,
  List,
  Heading,
  Pencil,
  Eye,
  Columns2
} from 'lucide-react'
import { MarkdownView } from '../digest/MarkdownView'
import { cn } from '../../lib/utils'

// ---------------------------------------------------------------------------
// Pure toolbar transforms
//
// Each takes the full document text plus a [from, to) selection range and
// returns the new text and the new selection. Keeping them pure (no CodeMirror
// types) makes them trivially unit-testable — `MarkdownEditor.test.mjs` mirrors
// this logic per the repo convention (no TS toolchain in `node --test`).
// ---------------------------------------------------------------------------

export interface EditResult {
  text: string
  from: number
  to: number
}

/**
 * Wrap the selection in `marker` (e.g. `**` for bold). If the selection is
 * already immediately surrounded by `marker`, the markers are removed
 * (toggle off) instead.
 */
export function toggleInline(text: string, from: number, to: number, marker: string): EditResult {
  const selected = text.slice(from, to)
  const before = text.slice(Math.max(0, from - marker.length), from)
  const after = text.slice(to, to + marker.length)
  if (before === marker && after === marker) {
    const next = text.slice(0, from - marker.length) + selected + text.slice(to + marker.length)
    return { text: next, from: from - marker.length, to: to - marker.length }
  }
  const next = text.slice(0, from) + marker + selected + marker + text.slice(to)
  return { text: next, from: from + marker.length, to: to + marker.length }
}

/**
 * Prefix every line that the selection touches with `prefix` (e.g. `- ` for a
 * bullet list, `## ` for a heading). The selection expands to whole lines.
 */
export function prefixLines(text: string, from: number, to: number, prefix: string): EditResult {
  const lineStart = text.lastIndexOf('\n', Math.max(0, from - 1)) + 1
  let lineEnd = text.indexOf('\n', to)
  if (lineEnd === -1) lineEnd = text.length
  const block = text.slice(lineStart, lineEnd)
  const lines = block.split('\n')
  const prefixed = lines.map((l) => prefix + l).join('\n')
  const next = text.slice(0, lineStart) + prefixed + text.slice(lineEnd)
  return { text: next, from: from + prefix.length, to: to + prefix.length * lines.length }
}

/**
 * Insert a markdown link around the selection: `[selected](url)`. If nothing is
 * selected, `text` is used as the placeholder label. The returned selection
 * targets the `url` placeholder so the user can type the destination.
 */
export function insertLink(text: string, from: number, to: number): EditResult {
  const label = text.slice(from, to) || 'text'
  const insert = `[${label}](url)`
  const next = text.slice(0, from) + insert + text.slice(to)
  const urlStart = from + label.length + 3 // "[" + label + "]("
  return { text: next, from: urlStart, to: urlStart + 3 }
}

type Transform = (text: string, from: number, to: number) => EditResult

function applyTransform(view: EditorView, transform: Transform): boolean {
  const { state } = view
  const sel = state.selection.main
  const text = state.doc.toString()
  const res = transform(text, sel.from, sel.to)
  view.dispatch({
    changes: { from: 0, to: text.length, insert: res.text },
    selection: { anchor: res.from, head: res.to },
    scrollIntoView: true
  })
  view.focus()
  return true
}

// ---------------------------------------------------------------------------
// Theme — driven entirely by the app's `--wf-*` tokens so it follows
// dark/light mode automatically (the tokens flip via `[data-theme]`).
// ---------------------------------------------------------------------------

const wfTheme = EditorView.theme({
  '&': {
    color: 'var(--wf-text-primary)',
    backgroundColor: 'transparent',
    fontSize: '13px',
    height: '100%'
  },
  '&.cm-focused': { outline: 'none' },
  '.cm-scroller': {
    fontFamily: 'var(--wf-font-mono)',
    lineHeight: '1.6',
    overflow: 'auto'
  },
  '.cm-content': { padding: '12px 16px', caretColor: 'var(--wf-fire)' },
  '.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--wf-fire)' },
  '&.cm-focused .cm-selectionBackground, .cm-selectionBackground, .cm-content ::selection': {
    backgroundColor: 'var(--wf-bg-elevated)'
  },
  '.cm-placeholder': { color: 'var(--wf-text-muted)' },
  '.cm-line': { padding: '0' }
})

// Minimal syntax highlight for the markdown source. Uses brand tokens so it
// reads correctly on both themes.
const wfHighlight = HighlightStyle.define([
  { tag: t.heading, color: 'var(--wf-text-primary)', fontWeight: '600' },
  { tag: t.strong, color: 'var(--wf-text-primary)', fontWeight: '700' },
  { tag: t.emphasis, fontStyle: 'italic' },
  { tag: t.link, color: 'var(--wf-fire)', textDecoration: 'underline' },
  { tag: t.url, color: 'var(--wf-fire)' },
  { tag: t.monospace, color: 'var(--color-fire-400, #e88050)' },
  { tag: t.list, color: 'var(--wf-text-secondary)' },
  { tag: [t.quote, t.comment], color: 'var(--wf-text-muted)', fontStyle: 'italic' }
])

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

type Mode = 'edit' | 'split' | 'preview'

interface MarkdownEditorProps {
  value: string
  onChange: (value: string) => void
  placeholder?: string
  minHeight?: number | string
  readOnly?: boolean
  ariaLabel?: string
  className?: string
}

interface ToolbarButtonProps {
  label: string
  shortcut?: string
  onClick: () => void
  disabled?: boolean
  children: ReactNode
}

function ToolbarButton({ label, shortcut, onClick, disabled, children }: ToolbarButtonProps): ReactNode {
  return (
    <button
      type="button"
      // Use onMouseDown so the editor keeps its selection (a real click would
      // blur CodeMirror and collapse the selection before the handler runs).
      onMouseDown={(e) => {
        e.preventDefault()
        if (!disabled) onClick()
      }}
      disabled={disabled}
      title={shortcut ? `${label} (${shortcut})` : label}
      aria-label={label}
      className={cn(
        'inline-flex items-center justify-center w-7 h-7 rounded-[var(--wf-radius-sm)]',
        'text-[var(--wf-text-secondary)] hover:text-[var(--wf-text-primary)] hover:bg-[var(--wf-bg-elevated)]',
        'transition-colors disabled:opacity-40 disabled:pointer-events-none'
      )}
    >
      {children}
    </button>
  )
}

export function MarkdownEditor({
  value,
  onChange,
  placeholder,
  minHeight = 160,
  readOnly = false,
  ariaLabel = 'Markdown editor',
  className
}: MarkdownEditorProps): ReactNode {
  const hostRef = useRef<HTMLDivElement | null>(null)
  const viewRef = useRef<EditorView | null>(null)
  const onChangeRef = useRef(onChange)
  const syncingRef = useRef(false)
  const readOnlyComp = useRef(new Compartment())
  const [mode, setMode] = useState<Mode>('edit')

  onChangeRef.current = onChange

  // Create the editor once on mount.
  useEffect(() => {
    if (!hostRef.current) return
    const view = new EditorView({
      parent: hostRef.current,
      state: EditorState.create({
        doc: value,
        extensions: [
          history(),
          drawSelection(),
          highlightSpecialChars(),
          EditorView.lineWrapping,
          markdown(),
          syntaxHighlighting(wfHighlight),
          cmPlaceholder(placeholder ?? ''),
          keymap.of([
            { key: 'Mod-b', run: (v) => applyTransform(v, (text, f, to) => toggleInline(text, f, to, '**')) },
            { key: 'Mod-i', run: (v) => applyTransform(v, (text, f, to) => toggleInline(text, f, to, '_')) },
            ...historyKeymap,
            ...defaultKeymap
          ]),
          readOnlyComp.current.of(EditorState.readOnly.of(readOnly)),
          EditorView.editable.of(!readOnly),
          EditorView.updateListener.of((update) => {
            if (update.docChanged && !syncingRef.current) {
              onChangeRef.current(update.state.doc.toString())
            }
          }),
          wfTheme
        ]
      })
    })
    view.contentDOM.setAttribute('aria-label', ariaLabel)
    viewRef.current = view
    return () => {
      view.destroy()
      viewRef.current = null
    }
    // Mount-once: subsequent prop changes are handled by the effects below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Keep the document in sync when `value` changes from the outside (e.g. the
  // consumer's polling or a project switch). Guarded so this programmatic edit
  // doesn't echo back through onChange.
  useEffect(() => {
    const view = viewRef.current
    if (!view) return
    const current = view.state.doc.toString()
    if (value === current) return
    syncingRef.current = true
    view.dispatch({ changes: { from: 0, to: current.length, insert: value } })
    syncingRef.current = false
  }, [value])

  // Reconfigure read-only without recreating the editor.
  useEffect(() => {
    const view = viewRef.current
    if (!view) return
    view.dispatch({ effects: readOnlyComp.current.reconfigure(EditorState.readOnly.of(readOnly)) })
  }, [readOnly])

  const run = (transform: Transform): void => {
    const view = viewRef.current
    if (!view || readOnly) return
    applyTransform(view, transform)
  }

  const showEditor = mode !== 'preview'
  const showPreview = mode !== 'edit'
  const minHeightStyle = typeof minHeight === 'number' ? `${minHeight}px` : minHeight

  return (
    <div
      className={cn(
        'flex flex-col rounded-[var(--wf-radius-md)] border border-[var(--wf-border)] overflow-hidden',
        'bg-[var(--wf-bg-primary)] focus-within:border-fire-500',
        className
      )}
    >
      {/* Toolbar */}
      <div className="flex items-center gap-0.5 px-1.5 py-1 border-b border-[var(--wf-border)] bg-[var(--wf-bg-secondary)]">
        <ToolbarButton label="Bold" shortcut="⌘B" disabled={readOnly} onClick={() => run((tx, f, to) => toggleInline(tx, f, to, '**'))}>
          <Bold size={14} />
        </ToolbarButton>
        <ToolbarButton label="Italic" shortcut="⌘I" disabled={readOnly} onClick={() => run((tx, f, to) => toggleInline(tx, f, to, '_'))}>
          <Italic size={14} />
        </ToolbarButton>
        <ToolbarButton label="Inline code" disabled={readOnly} onClick={() => run((tx, f, to) => toggleInline(tx, f, to, '`'))}>
          <Code size={14} />
        </ToolbarButton>
        <ToolbarButton label="Link" disabled={readOnly} onClick={() => run(insertLink)}>
          <LinkIcon size={14} />
        </ToolbarButton>
        <div className="w-px h-4 mx-1 bg-[var(--wf-border)]" />
        <ToolbarButton label="Heading" disabled={readOnly} onClick={() => run((tx, f, to) => prefixLines(tx, f, to, '## '))}>
          <Heading size={14} />
        </ToolbarButton>
        <ToolbarButton label="Bullet list" disabled={readOnly} onClick={() => run((tx, f, to) => prefixLines(tx, f, to, '- '))}>
          <List size={14} />
        </ToolbarButton>

        {/* Mode toggle (right-aligned) */}
        <div className="ml-auto flex items-center gap-0.5">
          <ToolbarButton label="Edit" onClick={() => setMode('edit')}>
            <Pencil size={14} className={mode === 'edit' ? 'text-fire-500' : undefined} />
          </ToolbarButton>
          <ToolbarButton label="Split view" onClick={() => setMode('split')}>
            <Columns2 size={14} className={mode === 'split' ? 'text-fire-500' : undefined} />
          </ToolbarButton>
          <ToolbarButton label="Preview" onClick={() => setMode('preview')}>
            <Eye size={14} className={mode === 'preview' ? 'text-fire-500' : undefined} />
          </ToolbarButton>
        </div>
      </div>

      {/* Body */}
      <div className="flex flex-1 min-h-0" style={{ minHeight: minHeightStyle }}>
        <div
          ref={hostRef}
          className={cn('min-w-0 overflow-auto', showEditor ? 'flex-1' : 'hidden', showPreview && showEditor && 'border-r border-[var(--wf-border)]')}
        />
        {showPreview && (
          <div className={cn('min-w-0 overflow-auto px-4 py-3', showEditor ? 'flex-1' : 'w-full')}>
            {value.trim() ? (
              <MarkdownView source={value} />
            ) : (
              <p className="text-sm text-[var(--wf-text-muted)] italic">Nothing to preview</p>
            )}
          </div>
        )}
      </div>
    </div>
  )
}
