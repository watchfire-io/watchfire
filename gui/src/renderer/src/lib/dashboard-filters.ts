import type { AgentStatus, Project, Task } from '../generated/watchfire_pb'

export type DashboardFilter = 'all' | 'working' | 'needs-attention' | 'idle' | 'has-ready'

export const DASHBOARD_FILTERS: DashboardFilter[] = [
  'all',
  'working',
  'needs-attention',
  'idle',
  'has-ready'
]

/**
 * Project has at least one non-deleted task in a "needs attention" state:
 * either an agent-reported failure (`status === 'done' && success === false`)
 * or a v5.0 post-task auto-merge failure (`mergeFailureReason` populated).
 * Merge failures keep `success: true` because the agent's work is fine —
 * only the merge into the default branch failed — so we have to consult
 * `mergeFailureReason` explicitly. Without this the dashboard chip would
 * stay dark on a silent run-all halt, which is exactly what v5.0 fixes.
 */
export function hasFailedTask(tasks: Task[] | undefined): boolean {
  if (!tasks) return false
  return tasks.some(
    (t) =>
      t.status === 'done' &&
      !t.deletedAt &&
      (t.success === false || (t.mergeFailureReason ?? '') !== '')
  )
}

/** Project has at least one non-deleted task with `status === 'ready'`. */
export function hasReadyTask(tasks: Task[] | undefined): boolean {
  if (!tasks) return false
  return tasks.some((t) => t.status === 'ready' && !t.deletedAt)
}

/** Agent is currently running for the project (chat sessions still count as "running"). */
export function isProjectWorking(status: AgentStatus | undefined): boolean {
  return !!status?.isRunning
}

/** Project is neither working nor in a needs-attention state. */
export function isProjectIdle(
  tasks: Task[] | undefined,
  status: AgentStatus | undefined
): boolean {
  return !isProjectWorking(status) && !hasFailedTask(tasks)
}

/**
 * Activity-sort priority. Lower number = higher priority.
 *   0 — needs attention (any failed task)
 *   1 — working (agent currently running)
 *   2 — has ready tasks (and not working / not needs-attention)
 *   3 — idle / no tasks
 */
export type ActivityGroup = 0 | 1 | 2 | 3

export function projectActivityGroup(
  tasks: Task[] | undefined,
  status: AgentStatus | undefined
): ActivityGroup {
  if (hasFailedTask(tasks)) return 0
  if (isProjectWorking(status)) return 1
  if (hasReadyTask(tasks)) return 2
  return 3
}

/**
 * Sort projects by activity group, breaking ties with the input array order
 * (stable). Returns a new array; the input is not mutated.
 */
export function sortProjectsByActivity(
  projects: Project[],
  tasksByProjectId: Record<string, Task[]>,
  agentStatuses: Record<string, AgentStatus>
): Project[] {
  return projects
    .map((project, idx) => ({
      project,
      idx,
      group: projectActivityGroup(
        tasksByProjectId[project.projectId],
        agentStatuses[project.projectId]
      )
    }))
    .sort((a, b) => a.group - b.group || a.idx - b.idx)
    .map((entry) => entry.project)
}

/** True when two project lists are in different ID order. */
export function projectOrderDiffers(a: Project[], b: Project[]): boolean {
  if (a.length !== b.length) return true
  for (let i = 0; i < a.length; i++) {
    if (a[i].projectId !== b[i].projectId) return true
  }
  return false
}

/** Whether a project matches the given dashboard filter. */
export function projectMatchesFilter(
  filter: DashboardFilter,
  tasks: Task[] | undefined,
  status: AgentStatus | undefined
): boolean {
  switch (filter) {
    case 'all':
      return true
    case 'working':
      return isProjectWorking(status)
    case 'needs-attention':
      return hasFailedTask(tasks)
    case 'idle':
      return isProjectIdle(tasks, status)
    case 'has-ready':
      return hasReadyTask(tasks)
  }
}

export interface DashboardCounts {
  all: number
  working: number
  'needs-attention': number
  idle: number
  'has-ready': number
}

/**
 * Per-filter project counts derived from a single pass over the project list.
 * Shared by the aggregate status bar (task 0036) and the filter chips (task
 * 0037) so the two surfaces never drift.
 */
export function dashboardCounts(
  projects: Project[],
  tasksByProjectId: Record<string, Task[]>,
  agentStatuses: Record<string, AgentStatus>
): DashboardCounts {
  const counts: DashboardCounts = {
    all: projects.length,
    working: 0,
    'needs-attention': 0,
    idle: 0,
    'has-ready': 0
  }
  for (const project of projects) {
    const tasks = tasksByProjectId[project.projectId]
    const status = agentStatuses[project.projectId]
    if (isProjectWorking(status)) counts.working++
    if (hasFailedTask(tasks)) counts['needs-attention']++
    if (isProjectIdle(tasks, status)) counts.idle++
    if (hasReadyTask(tasks)) counts['has-ready']++
  }
  return counts
}

/** Filter projects in place, preserving input order. */
export function filterProjects(
  projects: Project[],
  filter: DashboardFilter,
  tasksByProjectId: Record<string, Task[]>,
  agentStatuses: Record<string, AgentStatus>
): Project[] {
  if (filter === 'all') return projects
  return projects.filter((p) =>
    projectMatchesFilter(filter, tasksByProjectId[p.projectId], agentStatuses[p.projectId])
  )
}
