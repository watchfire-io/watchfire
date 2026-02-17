import { statusLabel, statusColor, cn } from '../lib/utils'

interface TaskStatusBadgeProps {
  status: string
  className?: string
}

export function TaskStatusBadge({ status, className }: TaskStatusBadgeProps) {
  const bgMap: Record<string, string> = {
    draft: 'bg-[var(--wf-bg-elevated)]',
    ready: 'bg-amber-900/30',
    done: 'bg-emerald-900/30'
  }

  return (
    <span
      className={cn(
        'inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium',
        bgMap[status] || 'bg-[var(--wf-bg-elevated)]',
        statusColor(status),
        className
      )}
    >
      {statusLabel(status)}
    </span>
  )
}
