import { useState, useEffect } from 'react'
import { ListTodo, FileText, Trash2, Settings, GitBranch, Globe, Circle, KeyRound, Sparkles, Maximize2, Minimize2 } from 'lucide-react'
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
import { InsightsTab } from './InsightsTab'
import { RightPanel } from './RightPanel/RightPanel'
import { BottomPanel } from './BottomPanel/BottomPanel'
import { useTerminalStore } from '../../stores/terminal-store'
import { useIntegrationsStore } from '../../stores/integrations-store'
import { WildfireControl } from './WildfireControl'
import { OpenInIDEButton } from './OpenInIDEButton'
import { ExportPill } from '../../components/ExportPill'

// v8 Inferno: the reference region (Tasks/Definition/etc.) now lives in the
// RIGHT pane; the agent chat/terminal is the primary LEFT pane.
type RefTab = 'tasks' | 'definition' | 'insights' | 'secrets' | 'trash' | 'settings'

const REF_TABS: { key: RefTab; icon: typeof ListTodo; label: string }[] = [
  { key: 'tasks', icon: ListTodo, label: 'Tasks' },
  { key: 'definition', icon: FileText, label: 'Definition' },
  { key: 'insights', icon: Sparkles, label: 'Insights' },
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
  // v7.0 Relay: surface an "Auto-PR" pill on the project header when
  // GitHubConfig.AutoPR applies to this project. Empty project_scopes
  // means "all projects", so the pill follows that convention.
  const integrationsConfig = useIntegrationsStore((s) => s.config)
  const fetchIntegrations = useIntegrationsStore((s) => s.fetch)
  useEffect(() => {
    if (integrationsConfig === null) {
      fetchIntegrations()
    }
  }, [integrationsConfig, fetchIntegrations])
  const autoPRApplies = (() => {
    const gh = integrationsConfig?.github
    if (!gh || !gh.enabled || !projectId) return false
    if ((gh.projectScopes ?? []).length === 0) return true
    return gh.projectScopes.includes(projectId)
  })()

  const [refTab, setRefTab] = useState<RefTab>('tasks')
  // v8 Inferno: chatFocus now means "hide the right reference region" so the
  // primary left chat/terminal pane goes full-width.
  const [chatFocus, setChatFocus] = useState(() => {
    return localStorage.getItem(`wf-chat-focus-${projectId ?? ''}`) === 'true'
  })
  // Width of the now-right reference region. We keep the historical
  // `wf-right-panel-width` key (its semantics — the width of the right panel —
  // are unchanged; only the panel's contents flipped).
  const { width: refPanelWidth, handleDragStart } = usePanelResize({
    storageKey: 'wf-right-panel-width',
    defaultWidth: 560,
    minWidth: 360,
    maxWidth: 900
  })

  // Per-project: re-hydrate focus state when the active project changes,
  // and mirror updates back to localStorage.
  useEffect(() => {
    if (!projectId) return
    setChatFocus(localStorage.getItem(`wf-chat-focus-${projectId}`) === 'true')
  }, [projectId])

  useEffect(() => {
    if (projectId) localStorage.setItem(`wf-chat-focus-${projectId}`, String(chatFocus))
  }, [chatFocus, projectId])

  const toggleFocus = () => setChatFocus((v) => !v)

  // Honour tray-driven focus requests: when a focus event lands on this
  // project, surface the Tasks tab — and reveal the reference region if it's
  // currently collapsed behind focus mode.
  const focusRequest = useAppStore((s) => s.focusRequest)
  useEffect(() => {
    if (!focusRequest || focusRequest.projectId !== projectId) return
    if (focusRequest.target === 'tasks' || focusRequest.target === 'task') {
      setRefTab('tasks')
      setChatFocus(false)
    }
  }, [focusRequest, projectId])

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

  // Cmd+` / Ctrl+` to toggle terminal panel.
  // Sessions persist across panel collapse — we only flip visibility, never destroy.
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === '`') {
        e.preventDefault()
        const state = useTerminalStore.getState()
        const projectSessions = state.sessions.filter((s) => s.projectId === projectId)
        if (projectSessions.length === 0 && project) {
          state.createSession(projectId!, project.path)
        } else if (state.panelOpen) {
          state.collapsePanel()
        } else {
          state.expandPanel()
        }
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [projectId, project])

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
            {autoPRApplies && (
              <span
                title="GitHub auto-PR enabled — completed tasks open a PR instead of merging locally"
                className="text-xs px-2 py-0.5 rounded-full bg-fire-500/15 text-fire-500 border border-fire-500/30 font-medium"
              >
                Auto-PR
              </span>
            )}
          </div>
          <div className="flex items-center gap-2">
            <WildfireControl projectId={projectId} />
            <ExportPill scope={{ kind: 'project', projectId }} />
            <OpenInIDEButton projectPath={project.path} />
            <button
              onClick={toggleFocus}
              title={chatFocus ? 'Show reference panel' : 'Focus chat — hide reference panel'}
              className="p-1.5 text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)] transition-colors"
            >
              {chatFocus ? <Minimize2 size={18} /> : <Maximize2 size={18} />}
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
          {/* LEFT (primary): agent chat / terminal — always present, full-width
              when the reference region is collapsed via focus mode. Note:
              RightPanel keeps its historical name but now renders on the left. */}
          <div className="flex-1 flex flex-col overflow-hidden min-w-0">
            <RightPanel projectId={projectId} />
          </div>

          {/* RIGHT: reference region (Tasks/Definition/etc.) — hidden in focus mode */}
          {!chatFocus && (
            <>
              <div
                onMouseDown={handleDragStart}
                onDoubleClick={toggleFocus}
                title="Drag to resize · double-click to focus chat"
                className="shrink-0 w-1 group relative cursor-col-resize"
              >
                <div className="absolute inset-y-0 left-1/2 -translate-x-1/2 w-0.5 opacity-0 group-hover:opacity-100 bg-[var(--wf-accent)] transition-opacity" />
              </div>
              <div
                className="shrink-0 flex flex-col overflow-hidden border-l border-[var(--wf-border)]"
                style={{ width: refPanelWidth }}
              >
                {/* Tab bar */}
                <div className="flex items-center gap-1 px-4 py-1 border-b border-[var(--wf-border)]">
                  {REF_TABS.map((tab) => {
                    const Icon = tab.icon
                    return (
                      <button
                        key={tab.key}
                        onClick={() => setRefTab(tab.key)}
                        className={cn(
                          'flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium rounded-[var(--wf-radius-md)] transition-colors',
                          refTab === tab.key
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
                  {refTab === 'tasks' && <TasksTab projectId={projectId} />}
                  {refTab === 'definition' && <DefinitionTab projectId={projectId} project={project} />}
                  {refTab === 'insights' && <InsightsTab projectId={projectId} />}
                  {refTab === 'secrets' && <SecretsTab projectId={projectId} project={project} />}
                  {refTab === 'trash' && <TrashTab projectId={projectId} />}
                  {refTab === 'settings' && <SettingsTab projectId={projectId} project={project} />}
                </div>
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
