// ExportPill is the shared v6.0 Ember "Export ▾" control that lands on
// every Insights surface. It opens a small dropdown with Markdown / CSV
// options; picking one triggers the matching `useExportReport` call.
//
// Scope is set by the parent — three shapes are supported, mirroring the
// proto contract:
//   - { kind: 'singleTask', id: '<project_id>:<n>' }
//   - { kind: 'project',    projectId: string }
//   - { kind: 'global' }
//
// The pill is intentionally a plain `<Button>` (per the v6.0 spec) plus a
// dropdown built from divs — no new component primitive. That keeps the
// styling pinned to the existing design tokens.

import { useEffect, useRef, useState } from 'react'
import { Download, ChevronDown } from 'lucide-react'
import { Button } from './ui/Button'
import { cn } from '../lib/utils'
import {
  useExportReport,
  type ExportFormatLabel,
  type ExportWindow
} from '../hooks/useExportReport'

export type ExportScope =
  | { kind: 'singleTask'; id: string }
  | { kind: 'project'; projectId: string }
  | { kind: 'global' }

interface ExportPillProps {
  scope: ExportScope
  /** Optional time window for project / global exports. */
  window?: ExportWindow
  /** Optional label override; defaults to "Export". */
  label?: string
  /** Tailwind class merged into the trigger button. */
  className?: string
}

export function ExportPill({ scope, window, label = 'Export', className }: ExportPillProps): React.ReactNode {
  const { exportSingleTask, exportProject, exportGlobal, loading } = useExportReport()
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement | null>(null)

  // Close the dropdown when clicking outside — keeps the surface tidy
  // when the user changes their mind.
  useEffect(() => {
    if (!open) return
    const onDoc = (e: MouseEvent): void => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDoc)
    return () => document.removeEventListener('mousedown', onDoc)
  }, [open])

  const handlePick = async (format: ExportFormatLabel): Promise<void> => {
    setOpen(false)
    try {
      switch (scope.kind) {
        case 'singleTask':
          await exportSingleTask(scope.id, format)
          break
        case 'project':
          await exportProject(scope.projectId, format, window)
          break
        case 'global':
          await exportGlobal(format, window)
          break
      }
    } catch {
      /* hook surfaces the error via state; the pill stays usable */
    }
  }

  return (
    <div ref={ref} className={cn('relative', className)}>
      <Button
        variant="secondary"
        size="sm"
        onClick={() => setOpen((v) => !v)}
        disabled={loading}
        aria-haspopup="menu"
        aria-expanded={open}
        data-testid="export-pill"
      >
        <Download size={14} />
        {loading ? 'Exporting…' : label}
        <ChevronDown size={12} />
      </Button>
      {open && (
        <div
          role="menu"
          className="absolute right-0 mt-1 z-50 min-w-[10rem] rounded-[var(--wf-radius-md)] border border-[var(--wf-border)] bg-[var(--wf-bg-elevated)] shadow-lg overflow-hidden"
        >
          <button
            role="menuitem"
            onClick={() => void handlePick('markdown')}
            data-testid="export-pill-md"
            className="block w-full text-left px-3 py-1.5 text-xs text-[var(--wf-text-primary)] hover:bg-[var(--wf-bg)]"
          >
            Markdown (.md)
          </button>
          <button
            role="menuitem"
            onClick={() => void handlePick('csv')}
            data-testid="export-pill-csv"
            className="block w-full text-left px-3 py-1.5 text-xs text-[var(--wf-text-primary)] hover:bg-[var(--wf-bg)]"
          >
            CSV (.csv)
          </button>
        </div>
      )}
    </div>
  )
}
