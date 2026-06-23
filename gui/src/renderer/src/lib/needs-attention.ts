import type { AgentIssue, Project, Task } from '../generated/watchfire_pb'
import type { FocusRequestTarget } from '../stores/app-store'

// v8 Inferno — mission control. A single "needs me" entry aggregated across all
// projects for the home window's needs-attention surface. Two sources feed it:
//   - live agent issues (auth_required / rate_limited) from the daemon's issue
//     detector, streamed via AgentService.SubscribeAgentIssues; and
//   - TASK_FAILED state (an agent-reported failure, or a post-task auto-merge
//     failure) derived from the task list the dashboard already loads.
// Click-through opens/focuses the offending project's window and routes it to
// the relevant surface (`target` / `taskNumber`).

export type AttentionKind = 'auth_required' | 'rate_limited' | 'agent_issue' | 'task_failed' | 'merge_failed'

export interface AttentionEntry {
  // Stable key so React lists don't thrash and so identical re-renders dedupe.
  id: string
  projectId: string
  projectName: string
  kind: AttentionKind
  // Short human label for the kind, e.g. "Auth required".
  label: string
  // Secondary detail — the issue message or the failing task's title.
  detail: string
  // Present for task-derived entries so click-through can deep-link the task.
  taskNumber?: number
  // Where to route the project window on click. Agent issues surface in the
  // always-visible chat pane (the IssueBanner + Resume live there), so they
  // only need the window focused ('main'); failed tasks open the Tasks tab.
  target: FocusRequestTarget
}

/**
 * Non-deleted tasks in a "needs attention" terminal state: an agent-reported
 * failure (`status === 'done' && success === false`) or a v5.0 post-task
 * auto-merge failure (`mergeFailureReason` populated while `success` stays
 * true, because the agent's work is fine — only the merge failed). Mirrors
 * `dashboard-filters.hasFailedTask` but returns the tasks themselves so the
 * panel can deep-link each one.
 */
export function failedTasks(tasks: Task[] | undefined): Task[] {
  if (!tasks) return []
  return tasks.filter(
    (t) =>
      t.status === 'done' &&
      !t.deletedAt &&
      (t.success === false || (t.mergeFailureReason ?? '') !== '')
  )
}

function issueKindLabel(issueType: string): { kind: AttentionKind; label: string } {
  switch (issueType) {
    case 'auth_required':
      return { kind: 'auth_required', label: 'Auth required' }
    case 'rate_limited':
      return { kind: 'rate_limited', label: 'Rate limited' }
    default:
      return { kind: 'agent_issue', label: 'Agent issue' }
  }
}

/**
 * Build the flat, ordered needs-attention list across all projects. Agent
 * issues come first (they block the agent right now), then failed tasks.
 * Within each group projects keep their input order so the surface is stable.
 */
export function buildAttentionEntries(
  projects: Project[],
  tasksByProjectId: Record<string, Task[]>,
  issuesByProjectId: Record<string, AgentIssue | null>
): AttentionEntry[] {
  const issueEntries: AttentionEntry[] = []
  const taskEntries: AttentionEntry[] = []

  for (const project of projects) {
    const { projectId, name } = project
    const projectName = name || projectId

    const issue = issuesByProjectId[projectId]
    if (issue && issue.issueType !== '') {
      const { kind, label } = issueKindLabel(issue.issueType)
      issueEntries.push({
        id: `issue:${projectId}`,
        projectId,
        projectName,
        kind,
        label,
        detail: issue.message || 'Agent is paused',
        target: 'main'
      })
    }

    for (const task of failedTasks(tasksByProjectId[projectId])) {
      const isMergeFailure = task.success !== false && (task.mergeFailureReason ?? '') !== ''
      taskEntries.push({
        id: `task:${projectId}:${task.taskNumber}`,
        projectId,
        projectName,
        kind: isMergeFailure ? 'merge_failed' : 'task_failed',
        label: isMergeFailure ? 'Merge failed' : 'Task failed',
        detail: task.title || `Task #${task.taskNumber}`,
        taskNumber: task.taskNumber,
        target: 'tasks'
      })
    }
  }

  return [...issueEntries, ...taskEntries]
}
