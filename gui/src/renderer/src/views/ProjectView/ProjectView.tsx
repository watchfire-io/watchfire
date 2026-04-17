import { useState, useEffect } from 'react'
import { ListTodo, FileText, Trash2, Settings, GitBranch, Globe, Circle, PanelRightClose, PanelRight, KeyRound } from 'lucide-react'
import { useAppStore } from '../../stores/app-store'
import { useProjectsStore } from '../../stores/projects-store'
import { useTasksStore } from '../../stores/tasks-store'
import { useGitStore } from '../../stores/git-store'
import { StatusDot } from '../../components/StatusDot'
import { isAgentWorking } from '../../lib/agent-utils'
import { AgentBadge } from '../../components/AgentBadge'
import { cn } from '../../lib/utils'
import { usePanelResize } from '../../hooks/usePanelResize'
import { TasksTab } from './TasksTab/TasksTab'
import { DefinitionTab } from './DefinitionTab'
import { SecretsTab } from './SecretsTab'
import { TrashTab } from './TrashTab'
import { SettingsTab } from './SettingsTab'
import { RightPanel } from './RightPanel/RightPanel'
import { BottomPanel } from './BottomPanel/BottomPanel'
import { useTerminalStore } from '../../stores/terminal-store'
import { ModesControl } from './ModesControl'

type CenterTab = 'tasks' | 'definition' | 'secrets' | 'trash' | 'settings'

const CENTER_TABS: { key: CenterTab; icon: typeof ListTodo; label: string }[] = [
  { key: 'tasks', icon: ListTodo, label: 'Tasks' },
  { key: 'definition', icon: FileText, label: 'Definition' },
  { key: 'secrets', icon: KeyRound, label: 'Secrets' },
  { key: 'trash', icon: Trash2, label: 'Trash' },
  { key: 'settings', icon: Settings, label: 'Settings' }
]

export function ProjectView() {
  const projectId = useAppStore((s) => s.selectedProjectId)
  const projects = useProjectsStore((s) => s.projects)
  const agentStatus = useProjectsStore((s) => s.agentStatuses[projectId || ''])
  const fetchAgentStatus = useProjectsStore((s) => s.fetchAgentStatus)
  const fetchTasks = useTasksStore((s) => s.fetchTasks)
  const gitInfo = useGitStore((s) => s.gitInfo[projectId || ''])
  const fetchGitInfo = useGitStore((s) => s.fetchGitInfo)

  const [centerTab, setCenterTab] = useState<CenterTab>('tasks')
  const [rightPanelOpen, setRightPanelOpen] = useState(() => {
    const saved = localStorage.getItem('wf-right-panel-open')
    return saved !== null ? saved === 'true' : true
  })
  const { width: rightPanelWidth, handleDragStart } = usePanelResize({
    storageKey: 'wf-right-panel-width',
    defaultWidth: 520,
    minWidth: 350,
    maxWidth: 800
  })

  useEffect(() => {
    localStorage.setItem('wf-right-panel-open', String(rightPanelOpen))
  }, [rightPanelOpen])

  const project = projects.find((p) => p.projectId === projectId)
  const isAgentRunning = agentStatus?.isRunning

  useEffect(() => {
    if (projectId) fetchTasks(projectId, true)
  }, [projectId])

  // Poll tasks regularly (every 3s when agent running, every 5s otherwise)
  useEffect(() => {
    if (!projectId) return
    const interval = setInterval(() => fetchTasks(projectId), isAgentRunning ? 3000 : 5000)
    return () => clearInterval(interval)
  }, [projectId, isAgentRunning])

  // Poll agent status every 5s to detect external changes
  useEffect(() => {
    if (!projectId) return
    const interval = setInterval(() => fetchAgentStatus(projectId), 5000)
    return () => clearInterval(interval)
  }, [projectId])

  // Fetch git info on mount and poll every 10s
  useEffect(() => {
    if (!projectId) return
    fetchGitInfo(projectId)
    const interval = setInterval(() => fetchGitInfo(projectId), 10000)
    return () => clearInterval(interval)
  }, [projectId])

  // Cmd+` / Ctrl+` to toggle terminal
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === '`') {
        e.preventDefault()
        const state = useTerminalStore.getState()
        if (state.sessions.length === 0 && project) {
          state.createSession(projectId!, project.path)
        } else if (state.sessions.length > 0) {
          state.destroyAllSessions()
        }
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [projectId, project])

  // Cleanup terminal sessions on project switch
  useEffect(() => {
    return () => {
      useTerminalStore.getState().destroyAllSessions()
    }
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
      <div className="px-6 py-3 border-b border-[var(--wf-border)] flex flex-col gap-2">
        {/* Row 1: Project name + status + panel toggle */}
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-3">
            <StatusDot color={project.color || '#e07040'} pulsing={isAgentWorking(agentStatus)} />
            <h2 className="font-heading text-base font-semibold">{project.name}</h2>
            {isAgentRunning && <AgentBadge status={agentStatus} />}
          </div>
          <div className="flex items-center gap-2">
            {!rightPanelOpen && <ModesControl projectId={projectId} layout="menu" />}
            <button
              onClick={() => setRightPanelOpen(!rightPanelOpen)}
              className="p-1.5 text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)] transition-colors"
            >
              {rightPanelOpen ? <PanelRightClose size={18} /> : <PanelRight size={18} />}
            </button>
          </div>
        </div>

        {/* Row 2: Git info */}
        <div className="flex items-center gap-2 text-xs text-[var(--wf-text-muted)]">
          {gitInfo?.currentBranch && (
            <>
              <GitBranch size={12} />
              <span>{gitInfo.currentBranch}</span>
            </>
          )}
          {gitInfo?.remoteUrl && (
            <>
              <span className="opacity-40">·</span>
              <Globe size={12} />
              <span>{gitInfo.remoteUrl}</span>
            </>
          )}
          {gitInfo?.isDirty && (
            <>
              <span className="opacity-40">·</span>
              <Circle size={10} className="text-yellow-500 fill-yellow-500" />
              <span>{gitInfo.uncommittedCount} {gitInfo.uncommittedCount === 1 ? 'change' : 'changes'}</span>
            </>
          )}
          {(gitInfo?.ahead > 0 || gitInfo?.behind > 0) && (
            <>
              <span className="opacity-40">·</span>
              {gitInfo.ahead > 0 && <span>{gitInfo.ahead}↑</span>}
              {gitInfo.behind > 0 && <span>{gitInfo.behind}↓</span>}
            </>
          )}
        </div>

      </div>

      {/* Content area */}
      <div className="flex-1 flex flex-col overflow-hidden">
        <div className="flex-1 flex overflow-hidden min-h-0">
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
              {centerTab === 'secrets' && <SecretsTab projectId={projectId} project={project} />}
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

        {/* Bottom panel — integrated terminal */}
        <BottomPanel projectId={projectId} projectPath={project.path} />
      </div>
    </div>
  )
}
