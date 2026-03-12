import type { AgentStatus } from '../generated/watchfire_pb'

/**
 * Returns true if two agent statuses are equivalent (no meaningful change).
 * Used to skip redundant state updates in stores.
 */
export function agentStatusEqual(a: AgentStatus | undefined, b: AgentStatus): boolean {
  if (!a) return false
  return (
    a.isRunning === b.isRunning &&
    a.mode === b.mode &&
    a.taskNumber === b.taskNumber &&
    a.taskTitle === b.taskTitle &&
    a.wildfirePhase === b.wildfirePhase
  )
}

/**
 * Returns true if the agent is doing autonomous work (not idle chat).
 * Used to decide whether status dots should pulse.
 */
export function isAgentWorking(status: AgentStatus | undefined): boolean {
  return !!status?.isRunning && status.mode !== 'chat'
}
