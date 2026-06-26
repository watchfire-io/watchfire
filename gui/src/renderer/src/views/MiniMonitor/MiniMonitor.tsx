import { useEffect, useMemo } from 'react'
import { AlertTriangle, LayoutDashboard } from 'lucide-react'
import { useProjectsStore } from '../../stores/projects-store'
import { useTasksStore } from '../../stores/tasks-store'
import { StatusDot } from '../../components/StatusDot'
import { isAgentWorking } from '../../lib/agent-utils'
import { sortProjectsByActivity, hasFailedTask } from '../../lib/dashboard-filters'
import { useNeedsAttention } from '../../hooks/useNeedsAttention'
import { formatTaskNumber } from '../../lib/utils'
import type { AgentStatus, Project, Task } from '../../generated/watchfire_pb'

/**
 * The always-on-top mini-monitor (v8 Inferno — stretch). A small, floating,
 * glanceable fleet status rendered in its own `?monitor=1` window. One compact
 * row per project: a status dot (pulsing when the agent is working), the name,
 * and a one-line status (what the agent is doing, a needs-attention flag, or
 * the ready/idle task summary).
 *
 * Lives in its own renderer, so it keeps its data fresh independently: it polls
 * the project list + agent statuses, and reuses `useNeedsAttention` (which polls
 * tasks and subscribes to agent-issue streams) for the attention flags. Clicking
 * a row opens (or focuses) that project's own window.
 */
export function MiniMonitor() {
  const projects = useProjectsStore((s) => s.projects)
  const agentStatuses = useProjectsStore((s) => s.agentStatuses)
  const fetchProjects = useProjectsStore((s) => s.fetchProjects)
  const fetchAllAgentStatuses = useProjectsStore((s) => s.fetchAllAgentStatuses)
  const tasksByProjectId = useTasksStore((s) => s.tasks)
  // Drives the per-row attention flag and the header count. Also keeps the
  // tasks store populated, which the activity sort below consumes.
  const attention = useNeedsAttention()

  // Keep project + agent status live. The home window only refetches on
  // (re)connect, so the monitor owns its own light poll while it's open.
  useEffect(() => {
    fetchProjects()
    const interval = setInterval(() => {
      void fetchAllAgentStatuses()
    }, 4000)
    return () => clearInterval(interval)
  }, [fetchProjects, fetchAllAgentStatuses])

  const attentionByProject = useMemo(() => {
    const map = new Map<string, number>()
    for (const entry of attention) {
      map.set(entry.projectId, (map.get(entry.projectId) ?? 0) + 1)
    }
    return map
  }, [attention])

  const sorted = useMemo(
    () => sortProjectsByActivity(projects, tasksByProjectId, agentStatuses),
    [projects, tasksByProjectId, agentStatuses]
  )

  const workingCount = useMemo(
    () => projects.filter((p) => isAgentWorking(agentStatuses[p.projectId])).length,
    [projects, agentStatuses]
  )
  const attentionCount = attentionByProject.size

  return (
    <div className="flex flex-col h-screen bg-[var(--wf-bg-primary)] text-[var(--wf-text-primary)] overflow-hidden">
      {/* Draggable header doubles as the macOS title bar. */}
      <div className="titlebar-drag shrink-0 flex items-center justify-between gap-2 pl-[68px] pr-2 h-9 border-b border-[var(--wf-border)]">
        <div className="flex items-center gap-2 min-w-0">
          <span className="font-heading text-[11px] font-semibold tracking-wide text-[var(--wf-text-secondary)]">
            Monitor
          </span>
          <span className="text-[10px] text-[var(--wf-text-muted)] truncate tabular-nums">
            {workingCount > 0 && `${workingCount} working`}
            {workingCount > 0 && attentionCount > 0 && ' · '}
            {attentionCount > 0 && (
              <span className="text-[var(--wf-error)]">{attentionCount} need attention</span>
            )}
            {workingCount === 0 && attentionCount === 0 && projects.length > 0 && 'All idle'}
          </span>
        </div>
        <button
          type="button"
          onClick={() => window.watchfire.openHomeWindow()}
          title="Open dashboard"
          className="titlebar-no-drag p-1 rounded-[var(--wf-radius-md)] text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)] hover:bg-[var(--wf-bg-elevated)] transition-colors shrink-0"
        >
          <LayoutDashboard size={13} />
        </button>
      </div>

      <div className="flex-1 overflow-y-auto py-1">
        {projects.length === 0 ? (
          <div className="h-full flex items-center justify-center px-4 text-center">
            <p className="text-[11px] text-[var(--wf-text-muted)]">No projects yet.</p>
          </div>
        ) : (
          sorted.map((project) => (
            <MonitorRow
              key={project.projectId}
              project={project}
              status={agentStatuses[project.projectId]}
              attentionCount={attentionByProject.get(project.projectId) ?? 0}
              tasksByProjectId={tasksByProjectId}
            />
          ))
        )}
      </div>
    </div>
  )
}

function MonitorRow({
  project,
  status,
  attentionCount,
  tasksByProjectId
}: {
  project: Project
  status: AgentStatus | undefined
  attentionCount: number
  tasksByProjectId: Record<string, Task[]>
}) {
  const working = isAgentWorking(status)
  const tasks = tasksByProjectId[project.projectId]
  const needsAttention = attentionCount > 0 || hasFailedTask(tasks)

  return (
    <button
      type="button"
      onClick={() => window.watchfire.openProjectWindow(project.projectId)}
      title={`Open ${project.name}`}
      className="group w-full flex items-center gap-2 px-3 py-1.5 text-left hover:bg-[var(--wf-bg-elevated)] transition-colors"
    >
      <StatusDot color={project.color || '#e07040'} pulsing={working} size="sm" />
      <span className="font-medium text-xs truncate min-w-0 flex-1">{project.name}</span>
      <span className="shrink-0 text-[10px] max-w-[55%] truncate text-right">
        {working ? (
          <span className="text-fire-400">{workingLabel(status)}</span>
        ) : needsAttention ? (
          <span className="inline-flex items-center gap-1 text-[var(--wf-error)]">
            <AlertTriangle size={10} className="shrink-0" />
            {attentionCount > 0 ? `${attentionCount} issue${attentionCount === 1 ? '' : 's'}` : 'Failed'}
          </span>
        ) : (
          <span className="text-[var(--wf-text-muted)]">{idleLabel(tasks)}</span>
        )}
      </span>
    </button>
  )
}

// Compact label for a working agent: the task title if known, otherwise the
// mode (chat / wildfire phase / task number).
function workingLabel(status: AgentStatus | undefined): string {
  if (!status) return 'Working'
  if (status.taskTitle) return status.taskTitle
  switch (status.mode) {
    case 'chat':
      return 'Chat'
    case 'wildfire':
      return status.wildfirePhase ? `Wildfire · ${status.wildfirePhase}` : 'Wildfire'
    case 'generate-definition':
      return 'Generating definition'
    case 'generate-tasks':
      return 'Planning tasks'
    default:
      return status.taskNumber ? `Task ${formatTaskNumber(status.taskNumber)}` : 'Working'
  }
}

// Compact label for an idle project: ready/done counts, or a hint when empty.
function idleLabel(tasks: Task[] | undefined): string {
  const live = tasks?.filter((t) => !t.deletedAt) ?? []
  if (live.length === 0) return 'No tasks'
  const ready = live.filter((t) => t.status === 'ready').length
  if (ready > 0) return `${ready} ready`
  return 'Idle'
}
