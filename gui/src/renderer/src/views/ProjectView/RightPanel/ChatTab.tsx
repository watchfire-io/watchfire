import { useRef, useEffect } from 'react'
import { Play } from 'lucide-react'
import { useAgentStore } from '../../../stores/agent-store'
import { useAgentTerminal } from '../../../hooks/useAgentTerminal'
import { IssueBanner } from '../../../components/IssueBanner'
import { Button } from '../../../components/ui/Button'
import { useToast } from '../../../components/ui/Toast'
import { ModesControl } from '../ModesControl'
import { WildfireControl } from '../WildfireControl'

interface Props {
  projectId: string
}

export function ChatTab({ projectId }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const agentStatus = useAgentStore((s) => s.statuses[projectId])
  const issue = useAgentStore((s) => s.issues[projectId])
  const startAgent = useAgentStore((s) => s.startAgent)

  const resumeAgent = useAgentStore((s) => s.resumeAgent)
  const fetchStatus = useAgentStore((s) => s.fetchStatus)
  const { toast } = useToast()

  const isRunning = agentStatus?.isRunning
  const autoStarted = useRef(false)
  const wasRunning = useRef(false)

  // Reset auto-start flag when switching projects
  useEffect(() => {
    autoStarted.current = false
  }, [projectId])

  // Reset auto-start when agent stops (so chat restarts after wildfire/task ends)
  useEffect(() => {
    if (wasRunning.current && !isRunning) {
      autoStarted.current = false
    }
    wasRunning.current = !!isRunning
  }, [isRunning])

  const { getDimensions } = useAgentTerminal({
    projectId,
    containerRef,
    active: !!isRunning
  })

  useEffect(() => {
    fetchStatus(projectId)
  }, [projectId])

  // Auto-start chat agent on first status fetch (mirrors TUI behavior).
  // Silent: the daemon refuses chat starts that would displace a working
  // agent or a run-all/wildfire chain mid-transition ("agent busy") — for
  // this opportunistic path that refusal is expected and not worth a toast;
  // the next status poll shows what is actually running.
  useEffect(() => {
    if (agentStatus && !isRunning && !autoStarted.current) {
      autoStarted.current = true
      handleStart({ silent: true })
    }
  }, [agentStatus])

  // Poll agent status so ChatTab reacts to agents started externally (e.g. header buttons)
  useEffect(() => {
    const interval = setInterval(() => fetchStatus(projectId), 2000)
    return () => clearInterval(interval)
  }, [projectId])

  const handleStart = async (opts?: { silent?: boolean }) => {
    try {
      const dims = getDimensions()
      await startAgent(projectId, 'chat', { rows: dims.rows, cols: dims.cols })
    } catch (err) {
      if (!opts?.silent) toast(String(err), 'error')
    }
  }

  return (
    <div className="flex flex-col h-full">
      {/* Modes toolbar. Wildfire leads the mode cluster (v10 Torch, task
          0148) with fire-orange emphasis — it's the flagship action; its
          confirm gate is unchanged. Live run state (badge, phase, current
          task, stop) lives in the project header's RunStatusLine, not here —
          duplicated in this toolbar it wrapped into a two-row jumble. */}
      <div className="flex items-center gap-2 px-3 py-2 border-b border-[var(--wf-border)]">
        <div className="flex items-center gap-1">
          <WildfireControl projectId={projectId} />
          <ModesControl projectId={projectId} layout="row" />
        </div>
      </div>

      {/* Issue banner */}
      {issue && (
        <IssueBanner
          issue={issue}
          onResume={() => resumeAgent(projectId)}
        />
      )}

      {/* Terminal — always mounted for dimension measurement */}
      <div className="flex-1 min-w-0 min-h-0 relative">
        <div ref={containerRef} className="absolute inset-0 overflow-hidden bg-charcoal-300 p-1" />

        {/* Overlay when not running */}
        {!isRunning && (
          <div className="absolute inset-0 flex flex-col items-center justify-center gap-4 text-[var(--wf-text-muted)] bg-charcoal-300/90 z-10">
            <p className="text-sm">No agent running</p>
            <Button size="sm" onClick={() => handleStart()}>
              <Play size={14} />
              Start Chat
            </Button>
          </div>
        )}
      </div>
    </div>
  )
}
