import { useEffect, type ReactNode } from 'react'
import { X } from 'lucide-react'
import { cn } from '../../lib/utils'

interface SlidePanelProps {
  open: boolean
  onClose: () => void
  title: string
  children: ReactNode
  footer?: ReactNode
  // Optional header slot rendered inline with the title (used by the
  // task detail panel to surface Edit / Inspect tabs).
  headerSlot?: ReactNode
  // Tailwind width class. Defaults to a 560px panel that matches the
  // historical task form. Diff viewers pass a wider class.
  widthClass?: string
  // 'none' switches off the body's px-5 py-4 — the InspectTab paints
  // edge-to-edge and manages its own padding.
  bodyPadding?: 'default' | 'none'
}

export function SlidePanel({
  open,
  onClose,
  title,
  children,
  footer,
  headerSlot,
  widthClass = 'w-[560px]',
  bodyPadding = 'default'
}: SlidePanelProps) {
  useEffect(() => {
    if (!open) return
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [open, onClose])

  if (!open) return null

  return (
    <div className="fixed inset-0 z-[200] flex justify-end">
      <div className="absolute inset-0 bg-black/60" onClick={onClose} />
      <div
        className={cn(
          'relative max-w-full h-full bg-[var(--wf-bg-secondary)] border-l border-[var(--wf-border)] shadow-wf-lg flex flex-col',
          widthClass
        )}
        style={{ animation: 'slideInRight 0.2s ease-out' }}
      >
        <div className="flex items-center px-5 py-4 border-b border-[var(--wf-border)] shrink-0 gap-2">
          <h3 className="text-base font-semibold truncate">{title}</h3>
          {headerSlot}
          <div className="flex-1" />
          <button onClick={onClose} className="text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)] transition-colors">
            <X size={18} />
          </button>
        </div>
        <div
          className={cn(
            'flex-1 min-h-0',
            bodyPadding === 'default' ? 'overflow-y-auto px-5 py-4' : 'flex flex-col overflow-hidden'
          )}
        >
          {children}
        </div>
        {footer && (
          <div className="flex items-center justify-end gap-2 px-5 py-3 border-t border-[var(--wf-border)] shrink-0">
            {footer}
          </div>
        )}
      </div>
    </div>
  )
}
