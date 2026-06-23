import { useState } from 'react'
import { Flame, Square } from 'lucide-react'
import { useProjectsStore } from '../../stores/projects-store'
import { useAgentStore } from '../../stores/agent-store'
import { Button } from '../../components/ui/Button'
import { Modal } from '../../components/ui/Modal'
import { useToast } from '../../components/ui/Toast'
import { WildfirePhaseBadge } from '../../components/WildfirePhaseBadge'
import { formatTaskNumber } from '../../lib/utils'

interface Props {
  projectId: string
}

/**
 * Dedicated wildfire start/stop control for the ProjectView header. Wildfire is
 * already driven over gRPC (StartAgent(mode="wildfire") / StopAgent); this is
 * pure GUI wiring against the existing generated client.
 *
 * - Idle → a "Wildfire" button that opens a confirm-before-start modal
 *   (wildfire is autonomous and spends tokens unattended).
 * - Running → a live phase stepper (Execute → Refine → Generate) plus the
 *   current task being worked, and a Stop control.
 */
export function WildfireControl({ projectId }: Props) {
  const agentStatus = useProjectsStore((s) => s.agentStatuses[projectId])
  const fetchAgentStatus = useProjectsStore((s) => s.fetchAgentStatus)
  const startAgent = useAgentStore((s) => s.startAgent)
  const stopAgent = useAgentStore((s) => s.stopAgent)
  const { toast } = useToast()

  const [confirmOpen, setConfirmOpen] = useState(false)
  const [busy, setBusy] = useState(false)

  const isWildfire = !!agentStatus?.isRunning && agentStatus.mode === 'wildfire'

  const start = async () => {
    setConfirmOpen(false)
    setBusy(true)
    try {
      // Let the daemon's StartAgent do the atomic kill+restart of any
      // previously running agent (manager.go) — same path ModesControl uses.
      await startAgent(projectId, 'wildfire')
      await fetchAgentStatus(projectId)
    } catch (err) {
      toast(String(err), 'error')
    } finally {
      setBusy(false)
    }
  }

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

  if (isWildfire) {
    return (
      <div className="flex items-center gap-1.5">
        <WildfirePhaseBadge phase={agentStatus.wildfirePhase} />
        {agentStatus.taskNumber > 0 && (
          <span
            className="text-xs text-[var(--wf-text-muted)] max-w-[180px] truncate"
            title={agentStatus.taskTitle || undefined}
          >
            T{formatTaskNumber(agentStatus.taskNumber)}
            {agentStatus.taskTitle ? ` · ${agentStatus.taskTitle}` : ''}
          </span>
        )}
        <Button size="sm" variant="danger" onClick={stop} disabled={busy} title="Stop wildfire">
          <Square size={12} />
          Stop
        </Button>
      </div>
    )
  }

  return (
    <>
      <Button
        size="sm"
        variant="ghost"
        onClick={() => setConfirmOpen(true)}
        disabled={busy}
        title="Start the autonomous wildfire loop (execute → refine → generate)"
      >
        <Flame size={12} />
        Wildfire
      </Button>
      <Modal
        open={confirmOpen}
        onClose={() => setConfirmOpen(false)}
        title="Start Wildfire?"
        footer={
          <>
            <Button variant="ghost" size="sm" onClick={() => setConfirmOpen(false)}>
              Cancel
            </Button>
            <Button variant="primary" size="sm" onClick={start} disabled={busy}>
              <Flame size={12} />
              Start Wildfire
            </Button>
          </>
        }
      >
        <p className="text-sm text-[var(--wf-text-secondary)]">
          Wildfire runs an <strong>autonomous loop</strong>: it executes ready tasks,
          refines the backlog, and generates new tasks — repeating until there is
          nothing left to do.
        </p>
        <p className="text-sm text-[var(--wf-text-secondary)] mt-2">
          It runs unattended and <strong>spends tokens continuously</strong>. Starting
          wildfire replaces any agent currently running on this project. You can stop it
          at any time from the header.
        </p>
      </Modal>
    </>
  )
}
