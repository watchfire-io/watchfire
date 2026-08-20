import { useEffect, useMemo } from 'react'
import { useProjectsStore } from '../stores/projects-store'
import { useTasksStore } from '../stores/tasks-store'
import { useAgentStore } from '../stores/agent-store'
import { buildAttentionEntries, type AttentionEntry } from '../lib/needs-attention'

/**
 * Aggregate "needs me" entries across ALL projects for the home window's
 * mission-control surface (v8 Inferno — Feature 4).
 *
 * Reuses the streams the dashboard already consumes — no new RPC:
 *   - failed tasks come from the task list (the Dashboard cards load tasks per
 *     project; here we also poll so the panel stays live as runs fail); and
 *   - agent issues (auth / rate-limit) come from AgentService.SubscribeAgentIssues.
 *     In a project window the ChatTab owns that subscription, but the home
 *     window has no ChatTab — so this hook opens one issue stream per running
 *     agent. Each renderer (window) has its own agent-store, so there's no
 *     double subscription within a window.
 */
export function useNeedsAttention(): AttentionEntry[] {
  const projects = useProjectsStore((s) => s.projects)
  const agentStatuses = useProjectsStore((s) => s.agentStatuses)
  const fetchAllAgentStatuses = useProjectsStore((s) => s.fetchAllAgentStatuses)
  const tasksByProjectId = useTasksStore((s) => s.tasks)
  const fetchTasks = useTasksStore((s) => s.fetchTasks)
  const issues = useAgentStore((s) => s.issues)
  const subscribeIssues = useAgentStore((s) => s.subscribeIssues)

  // A stable key for the project set so the effects below re-run only when a
  // project is added or removed, not on every status tick.
  const idsKey = projects.map((p) => p.projectId).join(',')

  // Poll tasks + agent statuses for all projects so a freshly-failed task
  // shows up without waiting for the user to mount that project's card, and
  // so the issue-stream gating below sees agents start and stop.
  useEffect(() => {
    const ids = idsKey ? idsKey.split(',') : []
    if (ids.length === 0) return
    const tick = (): void => {
      for (const id of ids) void fetchTasks(id)
      void fetchAllAgentStatuses()
    }
    tick()
    const interval = setInterval(tick, 5000)
    return () => clearInterval(interval)
  }, [idsKey, fetchTasks, fetchAllAgentStatuses])

  // One live agent-issue stream per RUNNING agent. The daemon rejects
  // SubscribeAgentIssues for projects with no agent, and a stream that opened
  // against a running agent ends when that agent exits — so blind-subscribing
  // per project both spams the console and goes permanently deaf for agents
  // that start later. Gating on the polled isRunning status (re)subscribes
  // whenever the set of running agents changes. The store updates `issues[id]`
  // itself, so the onIssue callback is a no-op; we keep the abort handles to
  // tear the streams down when the running set changes or the panel unmounts.
  const runningIdsKey = projects
    .map((p) => p.projectId)
    .filter((id) => agentStatuses[id]?.isRunning)
    .join(',')

  useEffect(() => {
    const ids = runningIdsKey ? runningIdsKey.split(',') : []
    const aborts = ids.map((id) => subscribeIssues(id, () => {}))
    return () => aborts.forEach((a) => a.abort())
  }, [runningIdsKey, subscribeIssues])

  return useMemo(
    () => buildAttentionEntries(projects, tasksByProjectId, issues),
    [projects, tasksByProjectId, issues]
  )
}
