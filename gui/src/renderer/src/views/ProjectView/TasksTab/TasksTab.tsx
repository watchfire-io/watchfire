import { useState } from 'react'
import { Plus, Search } from 'lucide-react'
import { useTasksStore } from '../../../stores/tasks-store'
import { Button } from '../../../components/ui/Button'
import { TaskGroup } from './TaskGroup'
import { TaskModal } from './TaskModal'

const EMPTY: never[] = []

interface Props {
  projectId: string
}

export function TasksTab({ projectId }: Props) {
  const tasks = useTasksStore((s) => s.tasks[projectId] ?? EMPTY)
  const [modalOpen, setModalOpen] = useState(false)
  const [search, setSearch] = useState('')

  const activeTasks = tasks.filter((t) => !t.deletedAt)
  const filtered = search
    ? activeTasks.filter(
        (t) =>
          t.title.toLowerCase().includes(search.toLowerCase()) ||
          String(t.taskNumber).includes(search)
      )
    : activeTasks

  const done = filtered.filter((t) => t.status === 'done')
  const ready = filtered.filter((t) => t.status === 'ready')
  const draft = filtered.filter((t) => t.status === 'draft')

  return (
    <div className="flex-1 flex flex-col overflow-hidden">
      {/* Toolbar */}
      <div className="flex items-center gap-2 px-4 py-2">
        <div className="relative flex-1">
          <Search size={14} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-[var(--wf-text-muted)]" />
          <input
            type="text"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search tasks..."
            className="w-full pl-8 pr-3 py-1.5 text-xs rounded-[var(--wf-radius-md)] bg-[var(--wf-bg-primary)] border border-[var(--wf-border)] text-[var(--wf-text-primary)] placeholder-[var(--wf-text-muted)] focus:outline-none focus:border-fire-500 transition-colors"
          />
        </div>
        <Button size="sm" onClick={() => setModalOpen(true)}>
          <Plus size={14} />
          New Task
        </Button>
      </div>

      {/* Task groups */}
      <div className="flex-1 overflow-y-auto px-4 pb-4 space-y-4">
        {ready.length > 0 && (
          <TaskGroup
            title="In Development"
            tasks={ready}
            projectId={projectId}
            color="var(--wf-warning)"
          />
        )}
        {draft.length > 0 && (
          <TaskGroup
            title="Todo"
            tasks={draft}
            projectId={projectId}
            color="var(--wf-text-muted)"
          />
        )}
        {done.length > 0 && (
          <TaskGroup
            title="Done"
            tasks={done}
            projectId={projectId}
            color="var(--wf-success)"
            collapsible
          />
        )}
        {activeTasks.length === 0 && (
          <div className="flex flex-col items-center justify-center py-16 text-[var(--wf-text-muted)]">
            <p className="text-sm mb-3">No tasks yet</p>
            <Button size="sm" onClick={() => setModalOpen(true)}>
              <Plus size={14} />
              Create First Task
            </Button>
          </div>
        )}
      </div>

      <TaskModal
        open={modalOpen}
        onClose={() => setModalOpen(false)}
        projectId={projectId}
      />
    </div>
  )
}
