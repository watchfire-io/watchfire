import { useState } from 'react'
import { ChevronDown, ChevronRight } from 'lucide-react'
import type { Task } from '../../../generated/watchfire_pb'
import { TaskItem } from './TaskItem'

interface Props {
  title: string
  tasks: Task[]
  projectId: string
  color: string
  collapsible?: boolean
}

export function TaskGroup({ title, tasks, projectId, color, collapsible }: Props) {
  const [collapsed, setCollapsed] = useState(false)

  return (
    <div>
      <button
        onClick={() => collapsible && setCollapsed(!collapsed)}
        className="flex items-center gap-2 mb-2 w-full text-left"
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
      {!collapsed && (
        <div className="space-y-1">
          {tasks.map((task) => (
            <TaskItem key={task.taskNumber} task={task} projectId={projectId} />
          ))}
        </div>
      )}
    </div>
  )
}
