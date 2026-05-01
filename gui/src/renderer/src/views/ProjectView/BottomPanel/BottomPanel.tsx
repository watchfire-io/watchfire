import { useEffect, useState } from 'react'
import { Plus, X, Terminal, ChevronUp, ChevronDown } from 'lucide-react'
import { useTerminalStore } from '../../../stores/terminal-store'
import { usePanelResizeVertical } from '../../../hooks/usePanelResizeVertical'
import { cn } from '../../../lib/utils'
import { TerminalTab } from './TerminalTab'

interface BottomPanelProps {
  projectId: string
  projectPath: string
}

const PULSE_WINDOW_MS = 2000

export function BottomPanel({ projectId, projectPath }: BottomPanelProps) {
  const sessions = useTerminalStore((s) => s.sessions)
  const activeSessionId = useTerminalStore((s) => s.activeSessionId)
  const panelOpen = useTerminalStore((s) => s.panelOpen)
  const setActiveSession = useTerminalStore((s) => s.setActiveSession)
  const createSession = useTerminalStore((s) => s.createSession)
  const destroySession = useTerminalStore((s) => s.destroySession)
  const expandPanel = useTerminalStore((s) => s.expandPanel)
  const collapsePanel = useTerminalStore((s) => s.collapsePanel)
  const { height, handleDragStart } = usePanelResizeVertical()

  const projectSessions = sessions.filter((s) => s.projectId === projectId)
  const hasSessions = projectSessions.length > 0
  const liveCount = projectSessions.filter((s) => !s.exited).length
  const showExpanded = panelOpen && hasSessions

  // If the active session belongs to a different project (e.g. user switched
  // projects), pick a session from this project to show by default.
  useEffect(() => {
    if (!hasSessions) return
    const active = sessions.find((s) => s.id === activeSessionId)
    if (!active || active.projectId !== projectId) {
      setActiveSession(projectSessions[projectSessions.length - 1].id)
    }
  }, [projectId, hasSessions, activeSessionId])

  // Pulse the indicator chip when any session in this project produced output
  // recently. Re-evaluate every second while collapsed.
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (panelOpen || !hasSessions) return
    const t = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(t)
  }, [panelOpen, hasSessions])
  const pulsing =
    !panelOpen &&
    projectSessions.some((s) => !s.exited && now - s.lastOutputAt < PULSE_WINDOW_MS)

  return (
    <>
      {/* Expanded panel — always mounted so xterm Terminal instances for every
          session (including ones in other projects) stay alive across project
          switches and panel collapse. CSS hides it in States A/B. The tab bar
          shows only this project's sessions. */}
      <div
        className={cn(
          'shrink-0 border-t border-[var(--wf-border)] flex flex-col',
          showExpanded ? '' : 'hidden'
        )}
        style={{ height: showExpanded ? height : undefined }}
      >
        {/* Drag handle */}
        <div
          onMouseDown={handleDragStart}
          className="shrink-0 h-1 cursor-row-resize group relative"
        >
          <div className="absolute inset-x-0 top-1/2 -translate-y-1/2 h-0.5 opacity-0 group-hover:opacity-100 bg-[var(--wf-accent)] transition-opacity" />
        </div>

        {/* Tab bar — only this project's sessions */}
        <div className="shrink-0 flex items-center gap-0.5 px-2 py-1 border-b border-[var(--wf-border)] bg-[var(--wf-bg-primary)]">
          <Terminal size={12} className="text-[var(--wf-text-muted)] mx-1" />
          {projectSessions.map((session) => (
            <button
              key={session.id}
              onClick={() => setActiveSession(session.id)}
              className={cn(
                'flex items-center gap-1.5 px-2.5 py-1 text-xs font-medium rounded-[var(--wf-radius-md)] transition-colors group',
                activeSessionId === session.id
                  ? 'bg-[var(--wf-bg-elevated)] text-[var(--wf-text-primary)]'
                  : 'text-[var(--wf-text-muted)] hover:text-[var(--wf-text-secondary)]'
              )}
            >
              <span>{session.label}</span>
              {session.exited && <span className="text-[10px] opacity-50">(exited)</span>}
              <span
                onClick={(e) => {
                  e.stopPropagation()
                  destroySession(session.id)
                }}
                className="opacity-0 group-hover:opacity-100 hover:text-[var(--wf-text-primary)] transition-opacity ml-0.5"
              >
                <X size={10} />
              </span>
            </button>
          ))}
          <button
            onClick={() => createSession(projectId, projectPath)}
            className="p-1 text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)] transition-colors"
            title="New terminal"
            disabled={projectSessions.length >= 5}
          >
            <Plus size={14} />
          </button>

          <div className="flex-1" />

          <button
            onClick={collapsePanel}
            className="flex items-center gap-1 p-1 text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)] transition-colors"
            title="Collapse panel (⌘`)"
          >
            <ChevronDown size={12} />
          </button>
        </div>

        {/* Terminal content — render every session globally so xterm instances
            persist across project switches. Visibility flag picks the one to
            show: must be the active session AND belong to the current project. */}
        <div className="flex-1 overflow-hidden bg-[#16181d]">
          {sessions.map((session) => (
            <TerminalTab
              key={session.id}
              sessionId={session.id}
              visible={session.id === activeSessionId && session.projectId === projectId}
            />
          ))}
        </div>
      </div>

      {/* Collapsed footer — shown in States A and B. Sessions are NOT remounted
          here because they live in the always-mounted expanded panel above
          (just hidden via CSS). */}
      {!showExpanded && !hasSessions && (
        <div className="shrink-0 border-t border-[var(--wf-border)] flex items-center justify-end gap-2 px-4 py-2 bg-[var(--wf-bg-primary)]">
          <button
            onClick={() => createSession(projectId, projectPath)}
            className="flex items-center gap-2 text-sm text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)] transition-colors"
            title="New terminal (⌘`)"
          >
            <Terminal size={14} />
            <span>Terminal</span>
            <ChevronUp size={12} />
          </button>
        </div>
      )}

      {!showExpanded && hasSessions && (
        <div className="shrink-0 border-t border-[var(--wf-border)] flex items-center justify-end gap-2 px-4 py-2 bg-[var(--wf-bg-primary)]">
          <button
            onClick={expandPanel}
            className="flex items-center gap-2 text-sm text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)] transition-colors"
            title={`${liveCount} running shell${liveCount === 1 ? '' : 's'} (⌘\`)`}
          >
            <Terminal size={14} />
            <span
              className={cn(
                'inline-block w-2 h-2 rounded-full',
                liveCount > 0 ? 'bg-[var(--wf-success)]' : 'bg-[var(--wf-text-muted)]',
                pulsing && 'animate-pulse'
              )}
            />
            <span>
              {projectSessions.length} {projectSessions.length === 1 ? 'shell' : 'shells'}
            </span>
            <ChevronUp size={12} />
          </button>
        </div>
      )}
    </>
  )
}
