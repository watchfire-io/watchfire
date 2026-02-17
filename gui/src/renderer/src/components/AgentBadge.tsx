import { cn, formatTaskNumber } from '../lib/utils'
import type { AgentStatus } from '../generated/watchfire_pb'

interface AgentBadgeProps {
  status: AgentStatus
  className?: string
}

export function AgentBadge({ status, className }: AgentBadgeProps) {
  if (!status.isRunning) return null

  const label = status.mode === 'chat'
    ? 'Chat'
    : status.mode === 'wildfire'
      ? `Wildfire${status.wildfirePhase ? ` (${status.wildfirePhase})` : ''}`
      : `Task ${formatTaskNumber(status.taskNumber)}`

  return (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-xs font-medium',
        'bg-fire-500/20 text-fire-400',
        className
      )}
    >
      <span className="w-1.5 h-1.5 rounded-full bg-fire-500 animate-pulse" />
      {label}
    </span>
  )
}
