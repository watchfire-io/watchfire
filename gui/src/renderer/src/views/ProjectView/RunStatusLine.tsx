import { useState } from 'react'
import { Square } from 'lucide-react'
import { useProjectsStore } from '../../stores/projects-store'
import { useAgentStore } from '../../stores/agent-store'
import { Button } from '../../components/ui/Button'
import { useToast } from '../../components/ui/Toast'
import { formatTaskNumber } from '../../lib/utils'

interface Props {
  projectId: string
}

/**
 * Live run state as its own project-header line, under the title and git
 * rows: the running mode's current task and a Stop control. (The mode
 * itself — including the wildfire phase — is already on the header's
 * AgentBadge, so no separate phase stepper.) This used to live inside the
 * chat toolbar next to the mode buttons, where at typical pane widths it
 * wrapped into a two-row jumble. Renders nothing when idle or when the
 * plain chat agent is running — chat needs no supervision line.
 */
export function RunStatusLine({ projectId }: Props) {
  const agentStatus = useProjectsStore((s) => s.agentStatuses[projectId])
  const fetchAgentStatus = useProjectsStore((s) => s.fetchAgentStatus)
  const stopAgent = useAgentStore((s) => s.stopAgent)
  const { toast } = useToast()
  const [busy, setBusy] = useState(false)

  if (!agentStatus?.isRunning || agentStatus.mode === 'chat') return null

  const stop = async () => {
    setBusy(true)
    try {
      await stopAgent(projectId)
      await fetchAgentStatus(projectId)
    } catch (err) {
      toast(String(err), 'error')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex items-center gap-2 min-w-0">
      {agentStatus.taskNumber > 0 && (
        <span
          className="text-xs text-[var(--wf-text-muted)] truncate"
          title={agentStatus.taskTitle || undefined}
        >
          T{formatTaskNumber(agentStatus.taskNumber)}
          {agentStatus.taskTitle ? ` · ${agentStatus.taskTitle}` : ''}
        </span>
      )}
      <Button
        size="sm"
        variant="danger"
        onClick={stop}
        disabled={busy}
        title="Stop the running agent"
        className="ml-auto shrink-0"
      >
        <Square size={12} />
        Stop
      </Button>
    </div>
  )
}
