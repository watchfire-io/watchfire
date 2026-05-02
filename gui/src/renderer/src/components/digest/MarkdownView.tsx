// MarkdownView is a deliberately-tiny renderer for the constrained subset of
// Markdown the digest template emits: headings (#, ##, ###), bullet lists
// (-), bold (**…**), italics (_…_), and code spans (`…`). The full
// react-markdown dep would be ~50 KB minified for a renderer we only ever
// point at our own server-rendered template, so we keep this in-tree.

import { type ReactNode } from 'react'

function renderInline(line: string): ReactNode[] {
  // Match **bold**, _italic_, `code` — left-to-right, non-overlapping.
  const out: ReactNode[] = []
  let i = 0
  let key = 0
  const re = /\*\*([^*]+)\*\*|_([^_]+)_|`([^`]+)`/g
  let m: RegExpExecArray | null
  while ((m = re.exec(line)) !== null) {
    if (m.index > i) out.push(line.slice(i, m.index))
    if (m[1]) out.push(<strong key={`b${key++}`}>{m[1]}</strong>)
    else if (m[2]) out.push(<em key={`i${key++}`}>{m[2]}</em>)
    else if (m[3])
      out.push(
        <code key={`c${key++}`} className="px-1 py-0.5 rounded bg-[var(--wf-bg-elevated)] text-fire-400 text-[0.85em]">
          {m[3]}
        </code>
      )
    i = m.index + m[0].length
  }
  if (i < line.length) out.push(line.slice(i))
  return out
}

interface MarkdownViewProps {
  source: string
}

export function MarkdownView({ source }: MarkdownViewProps): ReactNode {
  const lines = source.split('\n')
  const blocks: ReactNode[] = []
  let listItems: ReactNode[] = []
  let listIndent = 0
  let key = 0

  const flushList = (): void => {
    if (listItems.length === 0) return
    blocks.push(
      <ul key={`l${key++}`} className="list-disc pl-5 space-y-1 text-sm">
        {listItems}
      </ul>
    )
    listItems = []
    listIndent = 0
  }

  for (const raw of lines) {
    if (raw.trim() === '') {
      flushList()
      continue
    }
    if (raw.startsWith('### ')) {
      flushList()
      blocks.push(
        <h3 key={`h${key++}`} className="text-sm font-semibold mt-4 mb-1.5 text-[var(--wf-text-primary)]">
          {renderInline(raw.slice(4))}
        </h3>
      )
      continue
    }
    if (raw.startsWith('## ')) {
      flushList()
      blocks.push(
        <h2 key={`h${key++}`} className="text-base font-semibold mt-5 mb-2 text-[var(--wf-text-primary)]">
          {renderInline(raw.slice(3))}
        </h2>
      )
      continue
    }
    if (raw.startsWith('# ')) {
      flushList()
      blocks.push(
        <h1 key={`h${key++}`} className="text-lg font-semibold mt-2 mb-3 text-[var(--wf-text-primary)]">
          {renderInline(raw.slice(2))}
        </h1>
      )
      continue
    }
    const indentMatch = raw.match(/^(\s*)-\s+(.*)$/)
    if (indentMatch) {
      const indent = indentMatch[1].length
      if (listItems.length === 0) listIndent = indent
      const text = indentMatch[2]
      const className = indent > listIndent ? 'ml-4 text-[var(--wf-text-secondary)]' : 'text-[var(--wf-text-secondary)]'
      listItems.push(
        <li key={`li${key++}`} className={className}>
          {renderInline(text)}
        </li>
      )
      continue
    }
    flushList()
    blocks.push(
      <p key={`p${key++}`} className="text-sm text-[var(--wf-text-secondary)] my-1.5">
        {renderInline(raw)}
      </p>
    )
  }
  flushList()
  return <div className="leading-relaxed">{blocks}</div>
}
