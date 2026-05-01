import { useEffect, useState } from 'react'
import { GitBranch, ChevronRight, X } from 'lucide-react'
import type { Project } from '../../generated/watchfire_pb'
import { useProjectsStore } from '../../stores/projects-store'
import { useAppStore } from '../../stores/app-store'
import { useTasksStore } from '../../stores/tasks-store'
import { useGitStore } from '../../stores/git-store'
import { StatusDot } from '../../components/StatusDot'
import { isAgentWorking } from '../../lib/agent-utils'
import { AgentBadge } from '../../components/AgentBadge'
import { Modal } from '../../components/ui/Modal'
import { cn } from '../../lib/utils'

interface ProjectRowProps {
  project: Project
}

export function ProjectRow({ project }: ProjectRowProps) {
  const selectProject = useAppStore((s) => s.selectProject)
  const agentStatus = useProjectsStore((s) => s.agentStatuses[project.projectId])
  const removeProject = useProjectsStore((s) => s.removeProject)
  const tasks = useTasksStore((s) => s.tasks[project.projectId])
  const fetchTasks = useTasksStore((s) => s.fetchTasks)
  const gitInfo = useGitStore((s) => s.gitInfo[project.projectId])
  const fetchGitInfo = useGitStore((s) => s.fetchGitInfo)
  const [showConfirm, setShowConfirm] = useState(false)

  useEffect(() => {
    fetchTasks(project.projectId)
    fetchGitInfo(project.projectId)
  }, [project.projectId])

  const live = tasks?.filter((t) => !t.deletedAt) ?? []
  const taskCounts = {
    draft: live.filter((t) => t.status === 'draft').length,
    ready: live.filter((t) => t.status === 'ready').length,
    done: live.filter((t) => t.status === 'done').length,
    failed: live.filter((t) => t.status === 'done' && t.success === false).length
  }

  const running = !!agentStatus?.isRunning
  const hasFailed = taskCounts.failed > 0

  return (
    <>
      <div
        onClick={() => selectProject(project.projectId)}
        className={cn(
          'group relative flex items-center gap-3 h-[46px] px-4 bg-[var(--wf-bg-secondary)] border border-[var(--wf-border)] rounded-[var(--wf-radius-lg)] transition-all hover:border-[var(--wf-fire)] hover:-translate-y-0.5 cursor-pointer overflow-hidden',
          hasFailed && 'border-l-2 border-l-[var(--wf-error)]'
        )}
      >
        <StatusDot
          color={project.color || '#e07040'}
          pulsing={isAgentWorking(agentStatus)}
        />

        <h3 className="font-heading font-semibold text-sm truncate min-w-0 max-w-[180px] shrink-0">
          {project.name}
        </h3>

        <span className="flex items-center gap-1 text-xs text-[var(--wf-text-muted)] min-w-0 max-w-[140px] shrink-0">
          <GitBranch size={11} className="shrink-0" />
          <span className="truncate">{gitInfo?.currentBranch || '—'}</span>
        </span>

        <div className="flex items-center gap-3 text-xs min-w-0 flex-1">
          {running ? (
            <>
              <AgentBadge status={agentStatus} className="shrink-0" />
              {agentStatus.taskTitle && (
                <span className="text-[var(--wf-text-secondary)] truncate min-w-0">
                  {agentStatus.taskTitle}
                </span>
              )}
            </>
          ) : live.length > 0 ? (
            <span className="text-[var(--wf-text-muted)] truncate">
              <span>{taskCounts.draft} todo</span>
              <span className="mx-1.5 text-[var(--wf-border)]">·</span>
              <span className="text-[var(--wf-warning)]">{taskCounts.ready} in dev</span>
              <span className="mx-1.5 text-[var(--wf-border)]">·</span>
              <span className="text-[var(--wf-success)]">{taskCounts.done} done</span>
              {taskCounts.failed > 0 && (
                <>
                  <span className="mx-1.5 text-[var(--wf-border)]">·</span>
                  <span className="text-[var(--wf-error)]">{taskCounts.failed} failed</span>
                </>
              )}
            </span>
          ) : (
            <span className="text-[var(--wf-text-muted)]">No tasks yet</span>
          )}
        </div>

        <button
          onClick={(e) => { e.stopPropagation(); setShowConfirm(true) }}
          className="p-1 rounded-[var(--wf-radius-md)] text-[var(--wf-text-muted)] hover:text-[var(--wf-error)] hover:bg-[var(--wf-bg-elevated)] opacity-0 group-hover:opacity-100 transition-all shrink-0"
          title="Remove project"
        >
          <X size={14} />
        </button>

        <ChevronRight size={14} className="text-[var(--wf-text-muted)] shrink-0" />
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
