import { Plus, X, Terminal, ChevronUp, ChevronDown } from 'lucide-react'
import { useTerminalStore } from '../../../stores/terminal-store'
import { usePanelResizeVertical } from '../../../hooks/usePanelResizeVertical'
import { cn } from '../../../lib/utils'
import { TerminalTab } from './TerminalTab'

interface BottomPanelProps {
  projectId: string
  projectPath: string
}

export function BottomPanel({ projectId, projectPath }: BottomPanelProps) {
  const sessions = useTerminalStore((s) => s.sessions)
  const activeSessionId = useTerminalStore((s) => s.activeSessionId)
  const setActiveSession = useTerminalStore((s) => s.setActiveSession)
  const createSession = useTerminalStore((s) => s.createSession)
  const destroySession = useTerminalStore((s) => s.destroySession)
  const destroyAllSessions = useTerminalStore((s) => s.destroyAllSessions)
  const { height, handleDragStart } = usePanelResizeVertical()

  const hasSessions = sessions.length > 0

  // Collapsed footer — thin bar at the bottom
  if (!hasSessions) {
    return (
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
    )
  }

  // Expanded panel with terminal content
  return (
    <div className="shrink-0 border-t border-[var(--wf-border)] flex flex-col" style={{ height }}>
      {/* Drag handle */}
      <div
        onMouseDown={handleDragStart}
        className="shrink-0 h-1 cursor-row-resize group relative"
      >
        <div className="absolute inset-x-0 top-1/2 -translate-y-1/2 h-0.5 opacity-0 group-hover:opacity-100 bg-[var(--wf-accent)] transition-opacity" />
      </div>

      {/* Tab bar */}
      <div className="shrink-0 flex items-center gap-0.5 px-2 py-1 border-b border-[var(--wf-border)] bg-[var(--wf-bg-primary)]">
        <Terminal size={12} className="text-[var(--wf-text-muted)] mx-1" />
        {sessions.map((session) => (
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
          disabled={sessions.length >= 5}
        >
          <Plus size={14} />
        </button>

        <div className="flex-1" />

        <button
          onClick={destroyAllSessions}
          className="flex items-center gap-1 p-1 text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)] transition-colors"
          title="Close all terminals (⌘`)"
        >
          <ChevronDown size={12} />
        </button>
      </div>

      {/* Terminal content */}
      <div className="flex-1 overflow-hidden bg-[#16181d]">
        {sessions.map((session) => (
          <TerminalTab
            key={session.id}
            sessionId={session.id}
            visible={session.id === activeSessionId}
          />
        ))}
      </div>
    </div>
  )
}
