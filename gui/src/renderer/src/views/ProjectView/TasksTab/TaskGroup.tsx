import { useState } from 'react'
import { ChevronDown, ChevronRight, ArrowRightCircle } from 'lucide-react'
import {
  DndContext,
  closestCenter,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent
} from '@dnd-kit/core'
import {
  SortableContext,
  arrayMove,
  verticalListSortingStrategy
} from '@dnd-kit/sortable'
import type { Task } from '../../../generated/watchfire_pb'
import { useTasksStore } from '../../../stores/tasks-store'
import { useToast } from '../../../components/ui/Toast'
import { TaskItem } from './TaskItem'

interface MoveTarget {
  status: string
  label: string
}

interface Props {
  title: string
  tasks: Task[]
  projectId: string
  color: string
  collapsible?: boolean
  defaultCollapsed?: boolean
  moveTargets?: MoveTarget[]
  sortable?: boolean
  // Called with the new flat order of all task numbers across the project
  // (this group's new order followed by everything else in current order).
  // Built locally so the store stays oblivious to grouping.
  onReorder?: (taskNumbers: number[]) => Promise<void>
  // All active+inactive tasks in the project. Used only when `sortable` is on
  // to construct the flat reorder payload — the dragged group goes first,
  // everything else preserves its current relative order.
  allTasks?: Task[]
}

export function TaskGroup({
  title,
  tasks,
  projectId,
  color,
  collapsible,
  defaultCollapsed,
  moveTargets,
  sortable,
  onReorder,
  allTasks
}: Props) {
  const [collapsed, setCollapsed] = useState(defaultCollapsed ?? false)
  const [menuOpen, setMenuOpen] = useState(false)
  const bulkUpdateStatus = useTasksStore((s) => s.bulkUpdateStatus)
  const { toast } = useToast()

  // Activation distance keeps a single click on a row from triggering a drag —
  // only an actual pointer movement past the threshold starts sorting.
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 8 } })
  )

  const handleDragEnd = async (event: DragEndEvent) => {
    const { active, over } = event
    if (!over || active.id === over.id) return
    const ids = tasks.map((t) => String(t.taskNumber))
    const oldIndex = ids.indexOf(String(active.id))
    const newIndex = ids.indexOf(String(over.id))
    // Cross-group drags (or vanished rows mid-drag) fall through this guard.
    if (oldIndex === -1 || newIndex === -1) return
    const newGroupOrder = arrayMove(tasks, oldIndex, newIndex).map(
      (t) => t.taskNumber
    )
    const groupSet = new Set(newGroupOrder)
    const everythingElse = (allTasks ?? tasks)
      .filter((t) => !groupSet.has(t.taskNumber))
      .map((t) => t.taskNumber)
    const flat = [...newGroupOrder, ...everythingElse]
    try {
      await onReorder?.(flat)
    } catch (err) {
      toast(`Reorder failed: ${err}`, 'error')
    }
  }

  const handleMoveAll = async (status: string, label: string) => {
    setMenuOpen(false)
    try {
      const taskNumbers = tasks.map((t) => t.taskNumber)
      await bulkUpdateStatus(projectId, taskNumbers, status)
      toast(`Moved ${taskNumbers.length} task${taskNumbers.length === 1 ? '' : 's'} to ${label}`, 'success')
    } catch (err) {
      toast(String(err), 'error')
    }
  }

  return (
    <div>
      <div className="group flex items-center gap-2 mb-2">
        <button
          onClick={() => collapsible && setCollapsed(!collapsed)}
          className="flex items-center gap-2 flex-1 text-left"
        >
          {collapsible && (
            collapsed ? <ChevronRight size={14} className="text-[var(--wf-text-muted)]" /> : <ChevronDown size={14} className="text-[var(--wf-text-muted)]" />
          )}
          <span className="w-2 h-2 rounded-full" style={{ backgroundColor: color }} />
          <span className="text-xs font-semibold uppercase tracking-wider text-[var(--wf-text-muted)]">
            {title}
          </span>
          <span className="text-xs text-[var(--wf-text-muted)]">{tasks.length}</span>
        </button>
        {moveTargets && moveTargets.length > 0 && tasks.length > 0 && (
          <div className="relative opacity-0 group-hover:opacity-100 focus-within:opacity-100 transition-opacity">
            <button
              onClick={() => setMenuOpen(!menuOpen)}
              title="Move all tasks to another state"
              className="flex items-center gap-1 px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)] transition-colors"
            >
              <ArrowRightCircle size={12} />
              Move all
            </button>
            {menuOpen && (
              <>
                <div className="fixed inset-0 z-10" onClick={() => setMenuOpen(false)} />
                <div className="absolute right-0 top-full z-20 mt-1 py-1 w-40 bg-[var(--wf-bg-elevated)] border border-[var(--wf-border)] rounded-[var(--wf-radius-md)] shadow-wf">
                  {moveTargets.map((target) => (
                    <button
                      key={target.status + target.label}
                      onClick={() => handleMoveAll(target.status, target.label)}
                      className="flex items-center gap-2 w-full px-3 py-1.5 text-xs text-left text-[var(--wf-text-secondary)] hover:bg-[var(--wf-bg-primary)] transition-colors"
                    >
                      Move all to {target.label}
                    </button>
                  ))}
                </div>
              </>
            )}
          </div>
        )}
      </div>
      {!collapsed && (
        <div className="space-y-1">
          {sortable ? (
            <DndContext
              sensors={sensors}
              collisionDetection={closestCenter}
              onDragEnd={handleDragEnd}
            >
              <SortableContext
                items={tasks.map((t) => String(t.taskNumber))}
                strategy={verticalListSortingStrategy}
              >
                {tasks.map((task) => (
                  <TaskItem
                    key={task.taskNumber}
                    task={task}
                    projectId={projectId}
                    sortable
                  />
                ))}
              </SortableContext>
            </DndContext>
          ) : (
            tasks.map((task) => (
              <TaskItem key={task.taskNumber} task={task} projectId={projectId} />
            ))
          )}
        </div>
      )}
    </div>
  )
}
