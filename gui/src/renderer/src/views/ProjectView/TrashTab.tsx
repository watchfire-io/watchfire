import { RotateCcw, Trash2 } from 'lucide-react'
import { useTasksStore } from '../../stores/tasks-store'
import { Button } from '../../components/ui/Button'
import { formatTaskNumber, formatDate } from '../../lib/utils'
import { useToast } from '../../components/ui/Toast'

const EMPTY: never[] = []

interface Props {
  projectId: string
}

export function TrashTab({ projectId }: Props) {
  const tasks = useTasksStore((s) => s.tasks[projectId] ?? EMPTY)
  const restoreTask = useTasksStore((s) => s.restoreTask)
  const emptyTrash = useTasksStore((s) => s.emptyTrash)
  const { toast } = useToast()

  const deletedTasks = tasks.filter((t) => t.deletedAt)

  const handleRestore = async (taskNumber: number) => {
    try {
      await restoreTask(projectId, taskNumber)
      toast('Task restored', 'success')
    } catch (err) {
      toast(String(err), 'error')
    }
  }

  const handleEmptyTrash = async () => {
    try {
      await emptyTrash(projectId)
      toast('Trash emptied', 'success')
    } catch (err) {
      toast(String(err), 'error')
    }
  }

  if (deletedTasks.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center h-full text-[var(--wf-text-muted)]">
        <Trash2 size={32} className="mb-3 opacity-30" />
        <p className="text-sm">Trash is empty</p>
      </div>
    )
  }

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center justify-between px-4 py-2 border-b border-[var(--wf-border)]">
        <span className="text-xs text-[var(--wf-text-muted)]">
          {deletedTasks.length} deleted task{deletedTasks.length !== 1 ? 's' : ''}
        </span>
        <Button size="sm" variant="danger" onClick={handleEmptyTrash}>
          <Trash2 size={12} />
          Empty Trash
        </Button>
      </div>
      <div className="flex-1 overflow-y-auto p-4 space-y-1">
        {deletedTasks.map((t) => (
          <div
            key={t.taskNumber}
            className="flex items-center gap-3 px-3 py-2 rounded-[var(--wf-radius-md)] bg-[var(--wf-bg-secondary)] border border-[var(--wf-border)]"
          >
            <span className="text-xs font-mono text-[var(--wf-text-muted)] w-12 shrink-0">
              {formatTaskNumber(t.taskNumber)}
            </span>
            <span className="flex-1 text-sm truncate text-[var(--wf-text-muted)] line-through">
              {t.title}
            </span>
            <span className="text-xs text-[var(--wf-text-muted)]">
              {formatDate(t.deletedAt)}
            </span>
            <button
              onClick={() => handleRestore(t.taskNumber)}
              title="Restore"
              className="p-1 text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)] transition-colors"
            >
              <RotateCcw size={14} />
            </button>
          </div>
        ))}
      </div>
    </div>
  )
}
