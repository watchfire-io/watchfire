import { Download } from 'lucide-react'

interface UpdateBannerProps {
  version: string
  onUpdate: () => void
  onDismiss: () => void
}

export function UpdateBanner({ version, onUpdate, onDismiss }: UpdateBannerProps) {
  return (
    <div className="flex items-center gap-3 px-4 py-2 bg-fire-500/10 border-b border-fire-500/20 text-sm">
      <Download size={16} className="shrink-0 text-fire-400" />
      <span className="flex-1 text-[var(--wf-text-secondary)]">
        Watchfire {version} is available
      </span>
      <button
        onClick={onUpdate}
        className="px-2 py-1 text-xs font-medium rounded bg-fire-500 text-white hover:bg-fire-400 transition-colors"
      >
        Update
      </button>
      <button
        onClick={onDismiss}
        className="text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)] transition-colors"
      >
        &times;
      </button>
    </div>
  )
}
