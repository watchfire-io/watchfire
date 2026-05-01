import { useState } from 'react'
import {
  DndContext,
  closestCenter,
  type DragEndEvent,
  PointerSensor,
  useSensor,
  useSensors
} from '@dnd-kit/core'
import {
  SortableContext,
  rectSortingStrategy,
  verticalListSortingStrategy,
  useSortable
} from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import { LayoutGrid, Rows3 } from 'lucide-react'
import { useProjectsStore } from '../../stores/projects-store'
import { ProjectCard } from './ProjectCard'
import { ProjectRow } from './ProjectRow'
import { EmptyState } from './EmptyState'
import { cn } from '../../lib/utils'
import type { Project } from '../../generated/watchfire_pb'

type DashboardLayout = 'grid' | 'list'

const LAYOUT_KEY = 'wf-dashboard-layout'

function readSavedLayout(): DashboardLayout {
  try {
    const saved = localStorage.getItem(LAYOUT_KEY)
    return saved === 'list' ? 'list' : 'grid'
  } catch {
    return 'grid'
  }
}

function saveLayout(layout: DashboardLayout): void {
  try {
    localStorage.setItem(LAYOUT_KEY, layout)
  } catch {
    /* storage unavailable — ignore */
  }
}

export function Dashboard() {
  const projects = useProjectsStore((s) => s.projects)
  const loading = useProjectsStore((s) => s.loading)
  const reorderProjects = useProjectsStore((s) => s.reorderProjects)
  const [layout, setLayout] = useState<DashboardLayout>(readSavedLayout)

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } })
  )

  const handleDragEnd = (event: DragEndEvent) => {
    const { active, over } = event
    if (!over || active.id === over.id) return

    const ids = projects.map((p) => p.projectId)
    const oldIndex = ids.indexOf(String(active.id))
    const newIndex = ids.indexOf(String(over.id))
    if (oldIndex === -1 || newIndex === -1) return

    const newIds = [...ids]
    newIds.splice(oldIndex, 1)
    newIds.splice(newIndex, 0, String(active.id))
    reorderProjects(newIds)
  }

  const updateLayout = (next: DashboardLayout) => {
    setLayout(next)
    saveLayout(next)
  }

  if (loading && projects.length === 0) {
    return (
      <div className="flex-1 flex items-center justify-center">
        <div className="w-6 h-6 border-2 border-[var(--wf-fire)] border-t-transparent rounded-full animate-spin" />
      </div>
    )
  }

  if (projects.length === 0) {
    return <EmptyState />
  }

  return (
    <div className="flex-1 overflow-y-auto p-6">
      <div className="max-w-5xl mx-auto">
        <div className="mb-6 flex items-start justify-between gap-4">
          <div>
            <h2 className="font-heading text-xl font-semibold text-[var(--wf-text-primary)]">
              Dashboard
            </h2>
            <p className="text-sm text-[var(--wf-text-muted)] mt-1">
              Overview of all your projects and their current status.
            </p>
          </div>
          <LayoutToggle layout={layout} onChange={updateLayout} />
        </div>
        <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
          {layout === 'grid' ? (
            <SortableContext items={projects.map((p) => p.projectId)} strategy={rectSortingStrategy}>
              <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
                {projects.map((p) => (
                  <SortableProjectCard key={p.projectId} project={p} />
                ))}
              </div>
            </SortableContext>
          ) : (
            <SortableContext items={projects.map((p) => p.projectId)} strategy={verticalListSortingStrategy}>
              <div className="flex flex-col gap-2">
                {projects.map((p) => (
                  <SortableProjectRow key={p.projectId} project={p} />
                ))}
              </div>
            </SortableContext>
          )}
        </DndContext>
      </div>
    </div>
  )
}

interface LayoutToggleProps {
  layout: DashboardLayout
  onChange: (layout: DashboardLayout) => void
}

function LayoutToggle({ layout, onChange }: LayoutToggleProps) {
  const buttonBase =
    'p-1.5 rounded-[var(--wf-radius-md)] transition-colors'
  const active =
    'bg-[var(--wf-bg-elevated)] text-[var(--wf-text-primary)]'
  const inactive =
    'text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)] hover:bg-[var(--wf-bg-elevated)]'

  return (
    <div
      role="group"
      aria-label="Dashboard layout"
      className="inline-flex items-center gap-0.5 p-0.5 rounded-[var(--wf-radius-md)] border border-[var(--wf-border)] bg-[var(--wf-bg-secondary)] shrink-0"
    >
      <button
        type="button"
        title="Grid view"
        aria-label="Grid view"
        aria-pressed={layout === 'grid'}
        onClick={() => onChange('grid')}
        className={cn(buttonBase, layout === 'grid' ? active : inactive)}
      >
        <LayoutGrid size={14} />
      </button>
      <button
        type="button"
        title="List view"
        aria-label="List view"
        aria-pressed={layout === 'list'}
        onClick={() => onChange('list')}
        className={cn(buttonBase, layout === 'list' ? active : inactive)}
      >
        <Rows3 size={14} />
      </button>
    </div>
  )
}

function SortableProjectCard({ project }: { project: Project }) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: project.projectId
  })

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.5 : 1,
  }

  return (
    <div ref={setNodeRef} style={style} {...attributes} {...listeners}>
      <ProjectCard project={project} />
    </div>
  )
}

function SortableProjectRow({ project }: { project: Project }) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: project.projectId
  })

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.5 : 1,
  }

  return (
    <div ref={setNodeRef} style={style} {...attributes} {...listeners}>
      <ProjectRow project={project} />
    </div>
  )
}
