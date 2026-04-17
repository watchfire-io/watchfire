import { useEffect, useRef, useState } from 'react'
import { Sparkles, ListTodo, Play, Flame, Square, ChevronDown } from 'lucide-react'
import { useProjectsStore } from '../../stores/projects-store'
import { useAgentStore } from '../../stores/agent-store'
import { Button } from '../../components/ui/Button'
import { useToast } from '../../components/ui/Toast'
import { cn } from '../../lib/utils'

type Mode = 'generate-definition' | 'generate-tasks' | 'start-all' | 'wildfire'

const MODES: { mode: Mode; label: string; icon: typeof Sparkles; title: string }[] = [
  { mode: 'generate-definition', label: 'Generate', icon: Sparkles, title: 'Generate project definition from codebase' },
  { mode: 'generate-tasks', label: 'Plan', icon: ListTodo, title: 'Generate tasks from project definition' },
  { mode: 'start-all', label: 'Run All', icon: Play, title: 'Run all ready tasks sequentially' },
  { mode: 'wildfire', label: 'Wildfire', icon: Flame, title: 'Autonomous loop: generate, plan, and execute' }
]

interface Props {
  projectId: string
  layout: 'row' | 'menu'
}

export function ModesControl({ projectId, layout }: Props) {
  const agentStatus = useProjectsStore((s) => s.agentStatuses[projectId])
  const fetchAgentStatus = useProjectsStore((s) => s.fetchAgentStatus)
  const startAgent = useAgentStore((s) => s.startAgent)
  const stopAgent = useAgentStore((s) => s.stopAgent)
  const { toast } = useToast()

  const isRunning = !!agentStatus?.isRunning
  const activeMode = agentStatus?.mode
  const stopDisabled = !isRunning || activeMode === 'chat'

  const handleStart = async (mode: Mode) => {
    try {
      if (isRunning) await stopAgent(projectId)
      await startAgent(projectId, mode)
      await fetchAgentStatus(projectId)
    } catch (err) {
      toast(String(err), 'error')
    }
  }

  const handleStop = async () => {
    try {
      await stopAgent(projectId)
      await fetchAgentStatus(projectId)
    } catch (err) {
      toast(String(err), 'error')
    }
  }

  if (layout === 'row') {
    return (
      <div className="flex items-center gap-1 flex-wrap">
        {MODES.map(({ mode, label, icon: Icon, title }) => (
          <Button
            key={mode}
            size="sm"
            variant={isRunning && activeMode === mode ? 'primary' : 'ghost'}
            onClick={() => handleStart(mode)}
            title={title}
          >
            <Icon size={12} />
            {label}
          </Button>
        ))}
        <Button size="sm" variant="danger" onClick={handleStop} disabled={stopDisabled} title="Stop the running agent">
          <Square size={12} />
          Stop
        </Button>
      </div>
    )
  }

  return <ModesMenu isRunning={isRunning} activeMode={activeMode} onStart={handleStart} onStop={handleStop} stopDisabled={stopDisabled} />
}

interface MenuProps {
  isRunning: boolean
  activeMode?: string
  onStart: (mode: Mode) => void
  onStop: () => void
  stopDisabled: boolean
}

function ModesMenu({ isRunning, activeMode, onStart, onStop, stopDisabled }: MenuProps) {
  const [open, setOpen] = useState(false)
  const rootRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])

  // Active agent mode is visible at a glance: show Stop button when running a
  // non-chat mode; fall back to the Run dropdown when idle or in chat.
  const showStop = isRunning && activeMode && activeMode !== 'chat'

  if (showStop) {
    return (
      <Button size="sm" variant="danger" onClick={onStop} disabled={stopDisabled} title="Stop the running agent">
        <Square size={12} />
        Stop {activeMode ? `· ${activeMode}` : ''}
      </Button>
    )
  }

  return (
    <div ref={rootRef} className="relative">
      <Button size="sm" variant="ghost" onClick={() => setOpen((v) => !v)} title="Run an agent mode">
        <Play size={12} />
        Run
        <ChevronDown size={12} />
      </Button>
      {open && (
        <div
          className={cn(
            'absolute right-0 top-full mt-1 z-20 min-w-[180px]',
            'rounded-[var(--wf-radius-md)] border border-[var(--wf-border)] bg-[var(--wf-bg-secondary)] shadow-lg',
            'py-1'
          )}
        >
          {MODES.map(({ mode, label, icon: Icon, title }) => (
            <button
              key={mode}
              onClick={() => { setOpen(false); onStart(mode) }}
              title={title}
              className="flex items-center gap-2 w-full px-3 py-1.5 text-xs text-left text-[var(--wf-text-secondary)] hover:bg-[var(--wf-bg-elevated)] hover:text-[var(--wf-text-primary)] transition-colors"
            >
              <Icon size={12} />
              {label}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
