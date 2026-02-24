import { useState, useEffect, useCallback, useRef } from 'react'
import { ListTodo, FileText, Trash2, Settings, MessageSquare, GitBranch, ScrollText, PanelRightClose, PanelRight } from 'lucide-react'
import { useAppStore } from '../../stores/app-store'
import { useProjectsStore } from '../../stores/projects-store'
import { useTasksStore } from '../../stores/tasks-store'
import { StatusDot } from '../../components/StatusDot'
import { AgentBadge } from '../../components/AgentBadge'
import { cn } from '../../lib/utils'
import { TasksTab } from './TasksTab/TasksTab'
import { DefinitionTab } from './DefinitionTab'
import { TrashTab } from './TrashTab'
import { SettingsTab } from './SettingsTab'
import { RightPanel } from './RightPanel/RightPanel'

type CenterTab = 'tasks' | 'definition' | 'trash' | 'settings'

const CENTER_TABS: { key: CenterTab; icon: typeof ListTodo; label: string }[] = [
  { key: 'tasks', icon: ListTodo, label: 'Tasks' },
  { key: 'definition', icon: FileText, label: 'Definition' },
  { key: 'trash', icon: Trash2, label: 'Trash' },
  { key: 'settings', icon: Settings, label: 'Settings' }
]

export function ProjectView() {
  const projectId = useAppStore((s) => s.selectedProjectId)
  const projects = useProjectsStore((s) => s.projects)
  const agentStatus = useProjectsStore((s) => s.agentStatuses[projectId || ''])
  const fetchTasks = useTasksStore((s) => s.fetchTasks)

  const [centerTab, setCenterTab] = useState<CenterTab>('tasks')
  const [rightPanelOpen, setRightPanelOpen] = useState(true)
  const [rightPanelWidth, setRightPanelWidth] = useState(() => {
    const saved = localStorage.getItem('wf-right-panel-width')
    return saved ? Number(saved) : 520
  })
  const isDragging = useRef(false)

  const handleDragStart = useCallback((e: React.MouseEvent) => {
    e.preventDefault()
    isDragging.current = true
    const startX = e.clientX
    const startWidth = rightPanelWidth

    const onMouseMove = (ev: MouseEvent) => {
      const delta = startX - ev.clientX
      const newWidth = Math.min(800, Math.max(350, startWidth + delta))
      setRightPanelWidth(newWidth)
    }

    const onMouseUp = () => {
      isDragging.current = false
      document.removeEventListener('mousemove', onMouseMove)
      document.removeEventListener('mouseup', onMouseUp)
      document.body.style.cursor = ''
      document.body.style.userSelect = ''
    }

    document.body.style.cursor = 'col-resize'
    document.body.style.userSelect = 'none'
    document.addEventListener('mousemove', onMouseMove)
    document.addEventListener('mouseup', onMouseUp)
  }, [rightPanelWidth])

  useEffect(() => {
    localStorage.setItem('wf-right-panel-width', String(rightPanelWidth))
  }, [rightPanelWidth])

  const project = projects.find((p) => p.projectId === projectId)

  useEffect(() => {
    if (projectId) fetchTasks(projectId, true)
  }, [projectId])

  if (!project || !projectId) {
    return (
      <div className="flex-1 flex items-center justify-center text-[var(--wf-text-muted)]">
        Project not found
      </div>
    )
  }

  return (
    <div className="flex-1 flex flex-col overflow-hidden">
      {/* Project header */}
      <div className="flex items-center justify-between px-6 py-3 border-b border-[var(--wf-border)]">
        <div className="flex items-center gap-3">
          <StatusDot color={project.color || '#e07040'} pulsing={agentStatus?.isRunning} />
          <h2 className="font-heading text-base font-semibold">{project.name}</h2>
          {agentStatus?.isRunning && <AgentBadge status={agentStatus} />}
        </div>
        <button
          onClick={() => setRightPanelOpen(!rightPanelOpen)}
          className="p-1.5 text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)] transition-colors"
        >
          {rightPanelOpen ? <PanelRightClose size={18} /> : <PanelRight size={18} />}
        </button>
      </div>

      {/* Content area */}
      <div className="flex-1 flex overflow-hidden">
        {/* Center panel */}
        <div className="flex-1 flex flex-col overflow-hidden">
          {/* Tab bar */}
          <div className="flex items-center gap-1 px-4 py-1 border-b border-[var(--wf-border)]">
            {CENTER_TABS.map((tab) => {
              const Icon = tab.icon
              return (
                <button
                  key={tab.key}
                  onClick={() => setCenterTab(tab.key)}
                  className={cn(
                    'flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium rounded-[var(--wf-radius-md)] transition-colors',
                    centerTab === tab.key
                      ? 'bg-[var(--wf-bg-elevated)] text-[var(--wf-text-primary)]'
                      : 'text-[var(--wf-text-muted)] hover:text-[var(--wf-text-secondary)]'
                  )}
                >
                  <Icon size={14} />
                  {tab.label}
                </button>
              )
            })}
          </div>

          {/* Tab content */}
          <div className="flex-1 flex flex-col overflow-hidden">
            {centerTab === 'tasks' && <TasksTab projectId={projectId} />}
            {centerTab === 'definition' && <DefinitionTab projectId={projectId} project={project} />}
            {centerTab === 'trash' && <TrashTab projectId={projectId} />}
            {centerTab === 'settings' && <SettingsTab projectId={projectId} project={project} />}
          </div>
        </div>

        {/* Right panel */}
        {rightPanelOpen && (
          <>
            <div
              onMouseDown={handleDragStart}
              className="shrink-0 w-1 cursor-col-resize group relative"
            >
              <div className="absolute inset-y-0 left-1/2 -translate-x-1/2 w-0.5 opacity-0 group-hover:opacity-100 bg-[var(--wf-accent)] transition-opacity" />
            </div>
            <div className="shrink-0 overflow-hidden border-l border-[var(--wf-border)]" style={{ width: rightPanelWidth }}>
              <RightPanel projectId={projectId} />
            </div>
          </>
        )}
      </div>
    </div>
  )
}
