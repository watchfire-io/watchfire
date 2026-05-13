import type { AgentStatus } from '../generated/watchfire_pb'

/**
 * Returns true if two agent statuses are equivalent (no meaningful change).
 * Used to skip redundant state updates in stores.
 */
export function agentStatusEqual(a: AgentStatus | undefined, b: AgentStatus): boolean {
  if (!a) return false
  // started_at is part of the equality check so a kill+restart in the same
  // mode (e.g. wildfire phase transition, GUI-driven `startAgent` for a
  // chat that was already running) propagates to subscribers — otherwise
  // useAgentTerminal's generation-change cursor reset can't see it.
  const aStart = a.startedAt
  const bStart = b.startedAt
  const startEqual =
    (!aStart && !bStart) ||
    (!!aStart && !!bStart && aStart.seconds === bStart.seconds && aStart.nanos === bStart.nanos)
  return (
    a.isRunning === b.isRunning &&
    a.mode === b.mode &&
    a.taskNumber === b.taskNumber &&
    a.taskTitle === b.taskTitle &&
    a.wildfirePhase === b.wildfirePhase &&
    startEqual
  )
}

/**
 * Returns true if the agent is doing autonomous work (not idle chat).
 * Used to decide whether status dots should pulse.
 */
export function isAgentWorking(status: AgentStatus | undefined): boolean {
  return !!status?.isRunning && status.mode !== 'chat'
}
