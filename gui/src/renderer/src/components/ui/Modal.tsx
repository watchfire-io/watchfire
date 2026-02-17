import { useEffect, type ReactNode } from 'react'
import { X } from 'lucide-react'

interface ModalProps {
  open: boolean
  onClose: () => void
  title: string
  children: ReactNode
  footer?: ReactNode
}

export function Modal({ open, onClose, title, children, footer }: ModalProps) {
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
    <div className="fixed inset-0 z-[200] flex items-center justify-center">
      <div className="absolute inset-0 bg-black/60" onClick={onClose} />
      <div className="relative w-full max-w-lg mx-4 bg-[var(--wf-bg-secondary)] border border-[var(--wf-border)] rounded-[var(--wf-radius-xl)] shadow-wf-lg overflow-hidden">
        <div className="flex items-center justify-between px-5 py-4 border-b border-[var(--wf-border)]">
          <h3 className="text-base font-semibold">{title}</h3>
          <button onClick={onClose} className="text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)] transition-colors">
            <X size={18} />
          </button>
        </div>
        <div className="px-5 py-4 max-h-[60vh] overflow-y-auto">{children}</div>
        {footer && (
          <div className="flex items-center justify-end gap-2 px-5 py-3 border-t border-[var(--wf-border)]">
            {footer}
          </div>
        )}
      </div>
    </div>
  )
}
