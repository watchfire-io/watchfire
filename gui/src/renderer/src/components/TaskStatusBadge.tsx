import { statusLabel, statusColor, cn } from '../lib/utils'

interface TaskStatusBadgeProps {
  status: string
  success?: boolean
  /**
   * Populated when the agent reported the task as failed
   * (`status === 'done'` + `success !== true`). Surfaced through the
   * native `title` tooltip so the user can see WHY a task failed
   * without opening the modal.
   */
  failureReason?: string
  /**
   * v5.0 — populated when the post-task auto-merge into the default
   * branch failed. Distinct from `success === false` (agent-reported
   * failure): the agent's work is fine but the merge couldn't land,
   * so the badge reads "Merge failed" instead of "Failed".
   */
  mergeFailureReason?: string
  className?: string
}

const TOOLTIP_MAX_RUNES = 500

export function truncate(s: string, max: number): string {
  const runes = Array.from(s)
  if (runes.length <= max) return s
  return runes.slice(0, max - 1).join('') + '…'
}

export function computeBadgeTooltip(opts: {
  isAgentFailed: boolean
  isMergeFailed: boolean
  failureReason?: string
  mergeFailureReason?: string
}): string | undefined {
  const merge = (opts.mergeFailureReason ?? '').trim()
  const agent = (opts.failureReason ?? '').trim()
  if (opts.isMergeFailed && merge !== '') {
    return `Merge failed: ${truncate(merge, TOOLTIP_MAX_RUNES - 'Merge failed: '.length)}`
  }
  if (opts.isAgentFailed && agent !== '') {
    return `Failed: ${truncate(agent, TOOLTIP_MAX_RUNES - 'Failed: '.length)}`
  }
  return undefined
}

export function TaskStatusBadge({
  status,
  success,
  failureReason,
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
      title={computeBadgeTooltip({
        isAgentFailed,
        isMergeFailed,
        failureReason,
        mergeFailureReason
      })}
    >
      {label}
    </span>
  )
}
