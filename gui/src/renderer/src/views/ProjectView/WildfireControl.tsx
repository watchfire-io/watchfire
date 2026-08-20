import { useState } from 'react'
import { Flame } from 'lucide-react'
import { useProjectsStore } from '../../stores/projects-store'
import { useAgentStore } from '../../stores/agent-store'
import { Button } from '../../components/ui/Button'
import { Modal } from '../../components/ui/Modal'
import { useToast } from '../../components/ui/Toast'

interface Props {
  projectId: string
}

/**
 * Dedicated wildfire start/stop control. Since v10 Torch (task 0148) it leads
 * the mode cluster in the chat toolbar (first, fire-orange) so it reads as the
 * flagship action next to Generate/Plan/Run All. Wildfire is already driven
 * over gRPC (StartAgent(mode="wildfire") / StopAgent); this is pure GUI wiring
 * against the existing generated client.
 *
 * - Idle → a "Wildfire" button that opens a confirm-before-start modal
 *   (wildfire is autonomous and spends tokens unattended).
 * - Running → nothing here; the live phase stepper, current task, and Stop
 *   render as the project header's RunStatusLine.
 */
export function WildfireControl({ projectId }: Props) {
  const agentStatus = useProjectsStore((s) => s.agentStatuses[projectId])
  const fetchAgentStatus = useProjectsStore((s) => s.fetchAgentStatus)
  const startAgent = useAgentStore((s) => s.startAgent)
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

  // While wildfire runs, its live state (phase stepper, current task, stop)
  // renders as the project header's RunStatusLine — repeating it here made
  // the chat toolbar wrap into two rows. The toolbar only carries the start
  // affordance.
  if (isWildfire) return null

  return (
    <>
      <Button
        size="sm"
        variant="primary"
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
