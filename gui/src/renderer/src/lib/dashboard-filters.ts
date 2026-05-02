import type { AgentStatus, Project, Task } from '../generated/watchfire_pb'

/** Project has at least one non-deleted task with `status === 'done' && success === false`. */
export function hasFailedTask(tasks: Task[] | undefined): boolean {
  if (!tasks) return false
  return tasks.some((t) => t.status === 'done' && t.success === false && !t.deletedAt)
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
