import { useEffect, useState } from 'react'
import { Activity, AlertTriangle, Folder, GitBranch, ChevronRight, CheckCircle2, Code2, ListTodo, X } from 'lucide-react'
import type { Project } from '../../generated/watchfire_pb'
import { useProjectsStore } from '../../stores/projects-store'
import { useAppStore } from '../../stores/app-store'
import { useTasksStore } from '../../stores/tasks-store'
import { useGitStore } from '../../stores/git-store'
import { StatusDot } from '../../components/StatusDot'
import { isAgentWorking } from '../../lib/agent-utils'
import { relativeTime, timestampToMs } from '../../lib/relative-time'
import { AgentBadge } from '../../components/AgentBadge'
import { Modal } from '../../components/ui/Modal'
import { useAgentPreview } from '../../hooks/useAgentPreview'

interface ProjectCardProps {
  project: Project
}

export function ProjectCard({ project }: ProjectCardProps) {
  const selectProject = useAppStore((s) => s.selectProject)
  const agentStatus = useProjectsStore((s) => s.agentStatuses[project.projectId])
  const removeProject = useProjectsStore((s) => s.removeProject)
  const tasks = useTasksStore((s) => s.tasks[project.projectId])
  const fetchTasks = useTasksStore((s) => s.fetchTasks)
  const gitInfo = useGitStore((s) => s.gitInfo[project.projectId])
  const fetchGitInfo = useGitStore((s) => s.fetchGitInfo)
  const [showConfirm, setShowConfirm] = useState(false)
  const isAgentRunning = !!agentStatus?.isRunning
  const ptyPreview = useAgentPreview(project.projectId, isAgentRunning)

  useEffect(() => {
    fetchTasks(project.projectId)
    fetchGitInfo(project.projectId)
  }, [project.projectId])

  const taskCounts = {
    draft: tasks?.filter((t) => t.status === 'draft' && !t.deletedAt).length || 0,
    ready: tasks?.filter((t) => t.status === 'ready' && !t.deletedAt).length || 0,
    done: tasks?.filter((t) => t.status === 'done' && t.success !== false && !t.deletedAt).length || 0,
    failed:
      tasks?.filter((t) => t.status === 'done' && t.success === false && !t.deletedAt).length || 0
  }
  const total = taskCounts.draft + taskCounts.ready + taskCounts.done + taskCounts.failed
  const hasFailed = taskCounts.failed > 0

  // Find next upcoming task
  const nextTask = tasks?.find((t) => t.status === 'draft' && !t.deletedAt)

  // Most recent activity: latest non-deleted task updated_at.
  // When the agent is running we always show "Active now", so the agent's
  // start time only matters as a tiebreaker we don't need to compute here.
  const lastActivityMs = (() => {
    if (!tasks || tasks.length === 0) return null
    let latest = -Infinity
    for (const t of tasks) {
      if (t.deletedAt) continue
      const ms = timestampToMs(t.updatedAt)
      if (ms !== null && ms > latest) latest = ms
    }
    return latest === -Infinity ? null : latest
  })()

  return (
    <>
    <div
      onClick={() => selectProject(project.projectId)}
      className={`group relative h-56 flex flex-col bg-[var(--wf-bg-secondary)] border rounded-[var(--wf-radius-lg)] p-4 transition-all hover:border-[var(--wf-fire)] hover:-translate-y-0.5 cursor-pointer ${
        hasFailed ? 'border-[var(--wf-error)]/50' : 'border-[var(--wf-border)]'
      }`}
    >
      {/* Remove button (visible on hover) */}
      <button
        onClick={(e) => { e.stopPropagation(); setShowConfirm(true) }}
        className="absolute top-2 right-2 z-10 p-1 rounded-[var(--wf-radius-md)] text-[var(--wf-text-muted)] hover:text-[var(--wf-error)] hover:bg-[var(--wf-bg-elevated)] opacity-0 group-hover:opacity-100 transition-all"
        title="Remove project"
      >
        <X size={14} />
      </button>

      {/* Header: color dot + name + chevron */}
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-2 min-w-0">
          <StatusDot
            color={project.color || '#e07040'}
            pulsing={isAgentWorking(agentStatus)}
          />
          <h3 className="font-heading font-semibold text-sm truncate">{project.name}</h3>
          {hasFailed && (
            <span
              className="flex items-center gap-0.5 shrink-0 px-1.5 py-0.5 rounded-[var(--wf-radius-sm)] bg-[var(--wf-error)]/15 text-[var(--wf-error)] text-[10px] font-semibold leading-none"
              title={`${taskCounts.failed} failed task${taskCounts.failed === 1 ? '' : 's'}`}
            >
              <AlertTriangle size={10} className="shrink-0" />
              <span>{taskCounts.failed}</span>
            </span>
          )}
        </div>
        <ChevronRight size={14} className="text-[var(--wf-text-muted)] shrink-0 group-hover:opacity-0 transition-opacity" />
      </div>

      {/* Meta: branch + folder + last activity */}
      <div className="flex items-center gap-3 text-xs text-[var(--wf-text-muted)] mb-3">
        <span className="flex items-center gap-1">
          <GitBranch size={11} className="shrink-0" />
          {gitInfo?.currentBranch || '—'}
        </span>
        <span className="flex items-center gap-1 min-w-0">
          <Folder size={11} className="shrink-0" />
          <span className="truncate">{project.path?.split('/').pop()}</span>
        </span>
        {lastActivityMs !== null && (
          <span className="flex items-center gap-1 shrink-0">
            <Activity size={11} className="shrink-0" />
            {isAgentRunning ? (
              <span className="text-[var(--wf-success)]">Active now</span>
            ) : (
              <span>Active {relativeTime(lastActivityMs)}</span>
            )}
          </span>
        )}
      </div>

      {/* Agent badge */}
      {isAgentRunning && (
        <div className="mb-3">
          <AgentBadge status={agentStatus} />
        </div>
      )}

      {/* Task counts row */}
      {total > 0 ? (
        <div className="mt-auto pt-3 border-t border-[var(--wf-border)]">
          <div className="flex items-center gap-4 text-xs mb-2">
            <span className="flex items-center gap-1 text-[var(--wf-text-muted)]">
              <ListTodo size={12} className="shrink-0" />
              {taskCounts.draft} todo
            </span>
            <span className="flex items-center gap-1 text-[var(--wf-warning)]">
              <Code2 size={12} className="shrink-0" />
              {taskCounts.ready} in dev
            </span>
            <span className="flex items-center gap-1 text-[var(--wf-success)]">
              <CheckCircle2 size={12} className="shrink-0" />
              {taskCounts.done} done
            </span>
            {taskCounts.failed > 0 && (
              <span className="flex items-center gap-1 text-[var(--wf-error)]">
                <AlertTriangle size={12} className="shrink-0" />
                {taskCounts.failed} failed
              </span>
            )}
          </div>
          {/* Progress bar */}
          <div className="h-1 rounded-full bg-[var(--wf-bg-elevated)] overflow-hidden flex">
            {taskCounts.failed > 0 && (
              <div className="bg-[var(--wf-error)]" style={{ width: `${(taskCounts.failed / total) * 100}%` }} />
            )}
            {taskCounts.done > 0 && (
              <div className="bg-[var(--wf-success)]" style={{ width: `${(taskCounts.done / total) * 100}%` }} />
            )}
            {taskCounts.ready > 0 && (
              <div className="bg-[var(--wf-warning)]" style={{ width: `${(taskCounts.ready / total) * 100}%` }} />
            )}
            {taskCounts.draft > 0 && (
              <div className="bg-[var(--wf-border)]" style={{ width: `${(taskCounts.draft / total) * 100}%` }} />
            )}
          </div>
          {/* Next up */}
          {nextTask && (
            <p className="text-[11px] text-[var(--wf-text-muted)] mt-2 truncate">
              Next: {nextTask.title}
            </p>
          )}
          {isAgentRunning && ptyPreview && (
            <p className="font-mono text-[10px] text-[var(--wf-text-muted)] mt-2 truncate">
              {ptyPreview}
            </p>
          )}
        </div>
      ) : (
        <div className="mt-auto pt-3 border-t border-[var(--wf-border)]">
          <p className="text-xs text-[var(--wf-text-muted)]">No tasks yet</p>
          {isAgentRunning && ptyPreview && (
            <p className="font-mono text-[10px] text-[var(--wf-text-muted)] mt-2 truncate">
              {ptyPreview}
            </p>
          )}
        </div>
      )}
    </div>

    <Modal
      open={showConfirm}
      onClose={() => setShowConfirm(false)}
      title="Remove Project"
      footer={
        <>
          <button
            className="px-3 py-1.5 text-sm rounded-[var(--wf-radius-md)] text-[var(--wf-text-secondary)] hover:bg-[var(--wf-bg-elevated)] transition-colors"
            onClick={() => setShowConfirm(false)}
          >
            Cancel
          </button>
          <button
            className="px-3 py-1.5 text-sm rounded-[var(--wf-radius-md)] bg-[var(--wf-error)] text-white hover:opacity-90 transition-colors"
            onClick={async () => {
              await removeProject(project.projectId)
              setShowConfirm(false)
            }}
          >
            Remove
          </button>
        </>
      }
    >
      <p className="text-sm text-[var(--wf-text-secondary)]">
        This will remove <strong>{project.name}</strong> from Watchfire. No files will be deleted — you can re-add the project folder later.
      </p>
    </Modal>
    </>
  )
}
