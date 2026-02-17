import { useRef, useEffect } from 'react'
import { Play, Square } from 'lucide-react'
import { useAgentStore } from '../../../stores/agent-store'
import { useAgentTerminal } from '../../../hooks/useAgentTerminal'
import { IssueBanner } from '../../../components/IssueBanner'
import { AgentBadge } from '../../../components/AgentBadge'
import { Button } from '../../../components/ui/Button'
import { useToast } from '../../../components/ui/Toast'

interface Props {
  projectId: string
}

export function ChatTab({ projectId }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const agentStatus = useAgentStore((s) => s.statuses[projectId])
  const issue = useAgentStore((s) => s.issues[projectId])
  const startAgent = useAgentStore((s) => s.startAgent)
  const stopAgent = useAgentStore((s) => s.stopAgent)
  const resumeAgent = useAgentStore((s) => s.resumeAgent)
  const fetchStatus = useAgentStore((s) => s.fetchStatus)
  const { toast } = useToast()

  const isRunning = agentStatus?.isRunning

  useAgentTerminal({
    projectId,
    containerRef,
    enabled: !!isRunning
  })

  useEffect(() => {
    fetchStatus(projectId)
  }, [projectId])

  // Auto-start chat agent when tab mounts and no agent is running
  const autoStarted = useRef(false)
  useEffect(() => {
    if (!autoStarted.current && agentStatus !== undefined && !agentStatus?.isRunning) {
      autoStarted.current = true
      startAgent(projectId, 'chat').catch(() => {})
    }
  }, [projectId, agentStatus])

  const handleStart = async () => {
    try {
      await startAgent(projectId, 'chat')
    } catch (err) {
      toast(String(err), 'error')
    }
  }

  const handleStop = async () => {
    try {
      await stopAgent(projectId)
    } catch (err) {
      toast(String(err), 'error')
    }
  }

  if (!isRunning) {
    return (
      <div className="flex flex-col items-center justify-center h-full gap-4 text-[var(--wf-text-muted)]">
        <p className="text-sm">No agent running</p>
        <Button size="sm" onClick={handleStart}>
          <Play size={14} />
          Start Chat
        </Button>
      </div>
    )
  }

  return (
    <div className="flex flex-col h-full">
      {/* Header */}
      <div className="flex items-center justify-between px-3 py-2 border-b border-[var(--wf-border)]">
        <AgentBadge status={agentStatus} />
        <Button size="sm" variant="danger" onClick={handleStop}>
          <Square size={12} />
          Stop
        </Button>
      </div>

      {/* Issue banner */}
      {issue && (
        <IssueBanner
          issue={issue}
          onResume={() => resumeAgent(projectId)}
        />
      )}

      {/* Terminal */}
      <div ref={containerRef} className="flex-1 bg-charcoal-300 p-1" />
    </div>
  )
}
