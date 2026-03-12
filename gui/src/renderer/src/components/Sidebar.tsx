import { useState, useRef, useEffect } from 'react'
import { LayoutDashboard, Plus, Settings, PanelLeftClose, PanelLeft, Wifi, WifiOff, Trash2 } from 'lucide-react'
import { DndContext, closestCenter, type DragEndEvent, PointerSensor, useSensor, useSensors } from '@dnd-kit/core'
import { SortableContext, verticalListSortingStrategy, useSortable } from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import { useAppStore } from '../stores/app-store'
import { useProjectsStore } from '../stores/projects-store'
import { StatusDot } from './StatusDot'
import { isAgentWorking } from '../lib/agent-utils'
import { cn } from '../lib/utils'
import { Modal } from './ui/Modal'
import watchfireIcon from '../assets/watchfire-icon.svg'

export function Sidebar() {
  const view = useAppStore((s) => s.view)
  const selectedProjectId = useAppStore((s) => s.selectedProjectId)
  const collapsed = useAppStore((s) => s.sidebarCollapsed)
  const connected = useAppStore((s) => s.connected)
  const setView = useAppStore((s) => s.setView)
  const selectProject = useAppStore((s) => s.selectProject)
  const toggleSidebar = useAppStore((s) => s.toggleSidebar)

  const projects = useProjectsStore((s) => s.projects)
  const agentStatuses = useProjectsStore((s) => s.agentStatuses)
  const reorderProjects = useProjectsStore((s) => s.reorderProjects)
  const removeProject = useProjectsStore((s) => s.removeProject)

  const [contextMenu, setContextMenu] = useState<{ x: number; y: number; projectId: string; projectName: string } | null>(null)
  const [confirmRemove, setConfirmRemove] = useState<{ projectId: string; projectName: string } | null>(null)
  const contextMenuRef = useRef<HTMLDivElement>(null)

  // Close context menu on outside click
  useEffect(() => {
    if (!contextMenu) return
    const handler = (e: MouseEvent) => {
      if (contextMenuRef.current && !contextMenuRef.current.contains(e.target as Node)) {
        setContextMenu(null)
      }
    }
    window.addEventListener('mousedown', handler)
    return () => window.removeEventListener('mousedown', handler)
  }, [contextMenu])

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

  return (
    <aside
      className={cn(
        'flex flex-col h-full bg-[var(--wf-bg-secondary)] border-r border-[var(--wf-border)] transition-all duration-200',
        collapsed ? 'w-14' : 'w-48'
      )}
    >
      {/* Banner / drag area */}
      <div className="titlebar-drag shrink-0">
        {/* Space for macOS traffic lights */}
        <div className="h-10" />
        {!collapsed ? (
          <div className="flex items-center gap-2 px-4 pb-3 titlebar-no-drag">
            <img src={watchfireIcon} alt="Watchfire" className="w-6 h-6 shrink-0" />
            <span className="font-heading font-semibold text-sm tracking-tight text-[var(--wf-text-primary)]">
              watchfire
            </span>
          </div>
        ) : (
          <div className="flex justify-center pb-3 titlebar-no-drag">
            <img src={watchfireIcon} alt="Watchfire" className="w-6 h-6" />
          </div>
        )}
      </div>

      {/* Nav items */}
      <nav className="flex-1 flex flex-col gap-0.5 px-2 py-1 overflow-y-auto">
        <SidebarItem
          icon={<LayoutDashboard size={16} />}
          label="Dashboard"
          active={view === 'dashboard'}
          collapsed={collapsed}
          onClick={() => setView('dashboard')}
        />

        {/* Project list */}
        {!collapsed && projects.length > 0 && (
          <div className="mt-3 mb-1 px-2">
            <span className="text-[10px] font-semibold uppercase tracking-wider text-[var(--wf-text-muted)]">
              Projects
            </span>
          </div>
        )}

        <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
          <SortableContext items={projects.map((p) => p.projectId)} strategy={verticalListSortingStrategy}>
            {projects.map((p) => {
              const agentStatus = agentStatuses[p.projectId]
              return (
                <SortableProjectItem
                  key={p.projectId}
                  id={p.projectId}
                  icon={<StatusDot color={p.color || '#e07040'} pulsing={isAgentWorking(agentStatus)} size="sm" />}
                  label={p.name}
                  active={view === 'project' && selectedProjectId === p.projectId}
                  collapsed={collapsed}
                  onClick={() => selectProject(p.projectId)}
                  onContextMenu={(e) => {
                    e.preventDefault()
                    setContextMenu({ x: e.clientX, y: e.clientY, projectId: p.projectId, projectName: p.name })
                  }}
                />
              )
            })}
          </SortableContext>
        </DndContext>

        <SidebarItem
          icon={<Plus size={16} />}
          label="Add Project"
          active={view === 'add-project'}
          collapsed={collapsed}
          onClick={() => setView('add-project')}
        />
      </nav>

      {/* Footer */}
      <div className="flex flex-col gap-0.5 px-2 py-2 border-t border-[var(--wf-border)]">
        <SidebarItem
          icon={<Settings size={16} />}
          label="Settings"
          active={view === 'settings'}
          collapsed={collapsed}
          onClick={() => setView('settings')}
        />

        <div className="flex items-center justify-between px-2 py-1">
          {!collapsed && (
            <span className="flex items-center gap-1.5 text-[11px] text-[var(--wf-text-muted)]">
              {connected ? (
                <>
                  <Wifi size={11} className="text-[var(--wf-success)] shrink-0" />
                  Connected
                </>
              ) : (
                <>
                  <WifiOff size={11} className="text-[var(--wf-error)] shrink-0" />
                  Disconnected
                </>
              )}
            </span>
          )}
          <button
            onClick={toggleSidebar}
            className="p-1 text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)] transition-colors"
          >
            {collapsed ? <PanelLeft size={14} /> : <PanelLeftClose size={14} />}
          </button>
        </div>
      </div>
      {/* Context menu */}
      {contextMenu && (
        <div
          ref={contextMenuRef}
          className="fixed z-[300] min-w-[160px] bg-[var(--wf-bg-elevated)] border border-[var(--wf-border)] rounded-[var(--wf-radius-md)] shadow-wf-lg py-1"
          style={{ left: contextMenu.x, top: contextMenu.y }}
        >
          <button
            className="flex items-center gap-2 w-full px-3 py-1.5 text-sm text-[var(--wf-error)] hover:bg-[var(--wf-bg-secondary)] transition-colors"
            onClick={() => {
              setConfirmRemove({ projectId: contextMenu.projectId, projectName: contextMenu.projectName })
              setContextMenu(null)
            }}
          >
            <Trash2 size={14} />
            Remove Project
          </button>
        </div>
      )}

      {/* Remove confirmation modal */}
      <Modal
        open={!!confirmRemove}
        onClose={() => setConfirmRemove(null)}
        title="Remove Project"
        footer={
          <>
            <button
              className="px-3 py-1.5 text-sm rounded-[var(--wf-radius-md)] text-[var(--wf-text-secondary)] hover:bg-[var(--wf-bg-elevated)] transition-colors"
              onClick={() => setConfirmRemove(null)}
            >
              Cancel
            </button>
            <button
              className="px-3 py-1.5 text-sm rounded-[var(--wf-radius-md)] bg-[var(--wf-error)] text-white hover:opacity-90 transition-colors"
              onClick={async () => {
                if (confirmRemove) {
                  await removeProject(confirmRemove.projectId)
                  setConfirmRemove(null)
                  setView('dashboard')
                }
              }}
            >
              Remove
            </button>
          </>
        }
      >
        <p className="text-sm text-[var(--wf-text-secondary)]">
          This will remove <strong>{confirmRemove?.projectName}</strong> from Watchfire. No files will be deleted — you can re-add the project folder later.
        </p>
      </Modal>
    </aside>
  )
}

interface SidebarItemProps {
  icon: React.ReactNode
  label: string
  active?: boolean
  collapsed?: boolean
  onClick: () => void
}

function SidebarItem({ icon, label, active, collapsed, onClick }: SidebarItemProps) {
  return (
    <button
      onClick={onClick}
      title={collapsed ? label : undefined}
      className={cn(
        'flex items-center gap-2 px-2.5 py-1.5 rounded-[var(--wf-radius-md)] text-sm transition-colors w-full text-left',
        active
          ? 'bg-[var(--wf-bg-elevated)] text-[var(--wf-fire)]'
          : 'text-[var(--wf-text-secondary)] hover:bg-[var(--wf-bg-elevated)] hover:text-[var(--wf-text-primary)]',
        collapsed && 'justify-center px-0'
      )}
    >
      <span className="shrink-0 flex items-center justify-center w-4 h-4">{icon}</span>
      {!collapsed && <span className="truncate text-sm">{label}</span>}
    </button>
  )
}

interface SortableProjectItemProps extends SidebarItemProps {
  id: string
  onContextMenu?: (e: React.MouseEvent) => void
}

function SortableProjectItem({ id, icon, label, active, collapsed, onClick, onContextMenu }: SortableProjectItemProps) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id })

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.5 : 1,
  }

  return (
    <div ref={setNodeRef} style={style} {...attributes} {...listeners} onContextMenu={onContextMenu}>
      <SidebarItem
        icon={icon}
        label={label}
        active={active}
        collapsed={collapsed}
        onClick={onClick}
      />
    </div>
  )
}
