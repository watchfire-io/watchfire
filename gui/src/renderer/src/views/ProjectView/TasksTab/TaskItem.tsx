import { useState } from 'react'
import { Play, MoreHorizontal, ArrowUp, ArrowDown, Trash2, CheckCircle } from 'lucide-react'
import type { Task } from '../../../generated/watchfire_pb'
import { useTasksStore } from '../../../stores/tasks-store'
import { useAgentStore } from '../../../stores/agent-store'
import { TaskStatusBadge } from '../../../components/TaskStatusBadge'
import { formatTaskNumber, cn } from '../../../lib/utils'
import { useToast } from '../../../components/ui/Toast'
import { TaskModal } from './TaskModal'

interface Props {
  task: Task
  projectId: string
}

export function TaskItem({ task, projectId }: Props) {
  const updateTask = useTasksStore((s) => s.updateTask)
  const deleteTask = useTasksStore((s) => s.deleteTask)
  const startAgent = useAgentStore((s) => s.startAgent)
  const agentStatus = useAgentStore((s) => s.statuses[projectId])
  const { toast } = useToast()
  const [showMenu, setShowMenu] = useState(false)
  const [editOpen, setEditOpen] = useState(false)

  const isAgentOnTask = agentStatus?.isRunning && agentStatus?.taskNumber === task.taskNumber

  const handleStatusChange = async (status: string) => {
    try {
      await updateTask(projectId, task.taskNumber, { status })
    } catch (err) {
      toast(String(err), 'error')
    }
  }

  const handleStart = async () => {
    try {
      await startAgent(projectId, 'task', { taskNumber: task.taskNumber })
      toast(`Agent started on ${formatTaskNumber(task.taskNumber)}`, 'success')
    } catch (err) {
      toast(String(err), 'error')
    }
  }

  const handleDelete = async () => {
    try {
      await deleteTask(projectId, task.taskNumber)
    } catch (err) {
      toast(String(err), 'error')
    }
    setShowMenu(false)
  }

  return (
    <>
      <div
        className={cn(
          'group flex items-center gap-3 px-3 py-2 rounded-[var(--wf-radius-md)] transition-colors cursor-pointer',
          'hover:bg-[var(--wf-bg-elevated)]',
          isAgentOnTask && 'bg-fire-500/5 border border-fire-500/20'
        )}
        onClick={() => setEditOpen(true)}
      >
        <span className="text-xs font-mono text-[var(--wf-text-muted)] w-12 shrink-0">
          {formatTaskNumber(task.taskNumber)}
        </span>
        <span className="flex-1 text-sm truncate">{task.title}</span>
        {isAgentOnTask && (
          <span className="w-1.5 h-1.5 rounded-full bg-fire-500 animate-pulse shrink-0" />
        )}
        <TaskStatusBadge status={task.status} />

        {/* Action buttons — visible on hover */}
        <div className="flex items-center gap-0.5 opacity-0 group-hover:opacity-100 transition-opacity" onClick={(e) => e.stopPropagation()}>
          {task.status === 'draft' && (
            <button
              onClick={() => handleStatusChange('ready')}
              title="Move to Ready"
              className="p-1 text-[var(--wf-text-muted)] hover:text-[var(--wf-warning)] transition-colors"
            >
              <ArrowUp size={14} />
            </button>
          )}
          {task.status === 'ready' && !agentStatus?.isRunning && (
            <button
              onClick={handleStart}
              title="Start Agent"
              className="p-1 text-[var(--wf-text-muted)] hover:text-fire-500 transition-colors"
            >
              <Play size={14} />
            </button>
          )}
          {task.status === 'ready' && (
            <button
              onClick={() => handleStatusChange('done')}
              title="Mark Done"
              className="p-1 text-[var(--wf-text-muted)] hover:text-[var(--wf-success)] transition-colors"
            >
              <CheckCircle size={14} />
            </button>
          )}
          <div className="relative">
            <button
              onClick={() => setShowMenu(!showMenu)}
              className="p-1 text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)] transition-colors"
            >
              <MoreHorizontal size={14} />
            </button>
            {showMenu && (
              <>
                <div className="fixed inset-0 z-10" onClick={() => setShowMenu(false)} />
                <div className="absolute right-0 top-full z-20 mt-1 py-1 w-36 bg-[var(--wf-bg-elevated)] border border-[var(--wf-border)] rounded-[var(--wf-radius-md)] shadow-wf">
                  {task.status !== 'draft' && (
                    <MenuButton onClick={() => { handleStatusChange('draft'); setShowMenu(false) }}>
                      <ArrowDown size={12} /> Move to Draft
                    </MenuButton>
                  )}
                  <MenuButton onClick={handleDelete} danger>
                    <Trash2 size={12} /> Delete
                  </MenuButton>
                </div>
              </>
            )}
          </div>
        </div>
      </div>

      <TaskModal
        open={editOpen}
        onClose={() => setEditOpen(false)}
        projectId={projectId}
        task={task}
      />
    </>
  )
}

function MenuButton({ children, onClick, danger }: { children: React.ReactNode; onClick: () => void; danger?: boolean }) {
  return (
    <button
      onClick={onClick}
      className={cn(
        'flex items-center gap-2 w-full px-3 py-1.5 text-xs text-left transition-colors',
        danger
          ? 'text-red-400 hover:bg-red-900/20'
          : 'text-[var(--wf-text-secondary)] hover:bg-[var(--wf-bg-primary)]'
      )}
    >
      {children}
    </button>
  )
}
