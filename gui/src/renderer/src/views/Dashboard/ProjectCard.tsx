import { useEffect, useState } from 'react'
import { Folder, GitBranch, ChevronRight, CheckCircle2, Code2, ListTodo, X } from 'lucide-react'
import type { Project } from '../../generated/watchfire_pb'
import { useProjectsStore } from '../../stores/projects-store'
import { useAppStore } from '../../stores/app-store'
import { useTasksStore } from '../../stores/tasks-store'
import { StatusDot } from '../../components/StatusDot'
import { AgentBadge } from '../../components/AgentBadge'
import { Modal } from '../../components/ui/Modal'

interface ProjectCardProps {
  project: Project
}

export function ProjectCard({ project }: ProjectCardProps) {
  const selectProject = useAppStore((s) => s.selectProject)
  const agentStatus = useProjectsStore((s) => s.agentStatuses[project.projectId])
  const removeProject = useProjectsStore((s) => s.removeProject)
  const tasks = useTasksStore((s) => s.tasks[project.projectId])
  const fetchTasks = useTasksStore((s) => s.fetchTasks)
  const [showConfirm, setShowConfirm] = useState(false)

  useEffect(() => {
    fetchTasks(project.projectId)
  }, [project.projectId])

  const taskCounts = {
    draft: tasks?.filter((t) => t.status === 'draft' && !t.deletedAt).length || 0,
    ready: tasks?.filter((t) => t.status === 'ready' && !t.deletedAt).length || 0,
    done: tasks?.filter((t) => t.status === 'done' && !t.deletedAt).length || 0
  }
  const total = taskCounts.draft + taskCounts.ready + taskCounts.done

  // Find next upcoming task
  const nextTask = tasks?.find((t) => t.status === 'draft' && !t.deletedAt)

  return (
    <>
    <div
      onClick={() => selectProject(project.projectId)}
      className="group relative h-56 flex flex-col bg-[var(--wf-bg-secondary)] border border-[var(--wf-border)] rounded-[var(--wf-radius-lg)] p-4 transition-all hover:border-[var(--wf-fire)] hover:-translate-y-0.5 cursor-pointer"
    >
      {/* Remove button (visible on hover) */}
      <button
        onClick={(e) => { e.stopPropagation(); setShowConfirm(true) }}
        className="absolute top-2 right-2 p-1 rounded-[var(--wf-radius-md)] text-[var(--wf-text-muted)] hover:text-[var(--wf-error)] hover:bg-[var(--wf-bg-elevated)] opacity-0 group-hover:opacity-100 transition-all"
        title="Remove project"
      >
        <X size={14} />
      </button>

      {/* Header: color dot + name + chevron */}
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-2 min-w-0">
          <StatusDot
            color={project.color || '#e07040'}
            pulsing={agentStatus?.isRunning}
          />
          <h3 className="font-heading font-semibold text-sm truncate">{project.name}</h3>
        </div>
        <ChevronRight size={14} className="text-[var(--wf-text-muted)] shrink-0" />
      </div>

      {/* Meta: branch + folder */}
      <div className="flex items-center gap-3 text-xs text-[var(--wf-text-muted)] mb-3">
        <span className="flex items-center gap-1">
          <GitBranch size={11} className="shrink-0" />
          {project.defaultBranch || 'main'}
        </span>
        <span className="flex items-center gap-1 min-w-0">
          <Folder size={11} className="shrink-0" />
          <span className="truncate">{project.path?.split('/').pop()}</span>
        </span>
      </div>

      {/* Agent badge */}
      {agentStatus?.isRunning && (
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
          </div>
          {/* Progress bar */}
          <div className="h-1 rounded-full bg-[var(--wf-bg-elevated)] overflow-hidden flex">
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
        </div>
      ) : (
        <div className="mt-auto pt-3 border-t border-[var(--wf-border)]">
          <p className="text-xs text-[var(--wf-text-muted)]">No tasks yet</p>
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
