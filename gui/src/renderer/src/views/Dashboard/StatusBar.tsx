import { useMemo } from 'react'
import type { AgentStatus, Project, Task } from '../../generated/watchfire_pb'
import { isAgentWorking } from '../../lib/agent-utils'
import { hasFailedTask } from '../../lib/dashboard-filters'
import { timestampToMs } from '../../lib/relative-time'
import { cn } from '../../lib/utils'

interface StatusBarProps {
  projects: Project[]
  tasksByProjectId: Record<string, Task[]>
  agentStatuses: Record<string, AgentStatus>
}

interface StatusCounts {
  working: number
  needsAttention: number
  idle: number
  doneToday: number
}

function startOfTodayMs(now: number = Date.now()): number {
  const d = new Date(now)
  d.setHours(0, 0, 0, 0)
  return d.getTime()
}

function computeCounts(
  projects: Project[],
  tasksByProjectId: Record<string, Task[]>,
  agentStatuses: Record<string, AgentStatus>,
  now: number = Date.now()
): StatusCounts {
  const todayStart = startOfTodayMs(now)
  let working = 0
  let needsAttention = 0
  let idle = 0
  let doneToday = 0

  for (const project of projects) {
    const tasks = tasksByProjectId[project.projectId]
    const status = agentStatuses[project.projectId]
    const isWorking = isAgentWorking(status)
    const hasFailed = hasFailedTask(tasks)
    if (isWorking) working++
    if (hasFailed) needsAttention++
    if (!isWorking && !hasFailed) idle++

    if (tasks) {
      for (const task of tasks) {
        if (task.status !== 'done' || task.success !== true) continue
        const ms = timestampToMs(task.updatedAt)
        if (ms === null) continue
        if (ms >= todayStart) doneToday++
      }
    }
  }

  return { working, needsAttention, idle, doneToday }
}

export function StatusBar({ projects, tasksByProjectId, agentStatuses }: StatusBarProps) {
  const counts = useMemo(
    () => computeCounts(projects, tasksByProjectId, agentStatuses),
    [projects, tasksByProjectId, agentStatuses]
  )

  const segments: Array<{ label: string; value: number; warn?: boolean }> = [
    { label: 'working', value: counts.working },
    { label: 'needs attention', value: counts.needsAttention, warn: true },
    { label: 'idle', value: counts.idle },
    { label: 'done today', value: counts.doneToday }
  ]

  return (
    <div
      role="status"
      aria-label="Project activity summary"
      className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[12px] text-[var(--wf-text-muted)] tabular-nums"
    >
      {segments.map((seg, i) => (
        <span key={seg.label} className="inline-flex items-center gap-2">
          {i > 0 && (
            <span aria-hidden="true" className="text-[var(--wf-text-muted)]">
              ·
            </span>
          )}
          <span className="inline-flex items-center gap-1">
            <span
              className={cn(
                'font-medium',
                seg.warn && seg.value > 0
                  ? 'text-[var(--wf-warning)]'
                  : 'text-[var(--wf-text-primary)]'
              )}
            >
              {seg.value}
            </span>
            <span>{seg.label}</span>
          </span>
        </span>
      ))}
    </div>
  )
}
