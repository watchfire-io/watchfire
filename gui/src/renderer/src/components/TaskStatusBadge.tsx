import { statusLabel, statusColor, cn } from '../lib/utils'

interface TaskStatusBadgeProps {
  status: string
  success?: boolean
  /**
   * v5.0 — populated when the post-task auto-merge into the default branch
   * failed. Distinct from `success === false` (agent-reported failure):
   * the agent's work is fine but the merge couldn't land, so the badge
   * reads "Merge failed" instead of "Failed".
   */
  mergeFailureReason?: string
  className?: string
}

export function TaskStatusBadge({
  status,
  success,
  mergeFailureReason,
  className
}: TaskStatusBadgeProps) {
  const isAgentFailed = status === 'done' && success !== true
  const isMergeFailed = status === 'done' && !isAgentFailed && (mergeFailureReason ?? '') !== ''
  const isFailed = isAgentFailed || isMergeFailed

  const bgMap: Record<string, string> = {
    draft: 'bg-[var(--wf-bg-elevated)]',
    ready: 'bg-amber-900/30',
    done: 'bg-emerald-900/30'
  }

  let label: string = statusLabel(status)
  if (isMergeFailed) label = 'Merge failed'
  else if (isAgentFailed) label = 'Failed'

  return (
    <span
      className={cn(
        'inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium',
        isFailed ? 'bg-red-900/30 text-red-400' : bgMap[status] || 'bg-[var(--wf-bg-elevated)]',
        !isFailed && statusColor(status),
        className
      )}
      title={isMergeFailed ? `Merge failed: ${mergeFailureReason}` : undefined}
    >
      {label}
    </span>
  )
}
