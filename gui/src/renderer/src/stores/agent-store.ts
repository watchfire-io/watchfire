import { create } from 'zustand'
import type { AgentStatus, AgentIssue } from '../generated/watchfire_pb'
import { getAgentClient } from '../lib/grpc-client'
import { agentStatusEqual } from '../lib/agent-utils'

interface AgentState {
  statuses: Record<string, AgentStatus>
  issues: Record<string, AgentIssue | null>
  screenAborts: Record<string, AbortController>
  rawAborts: Record<string, AbortController>
  issueAborts: Record<string, AbortController>
  // Per-project guard: set while a startAgent RPC is in flight. The
  // daemon's StartAgent atomically kills the previous agent and spawns
  // the new one (manager.go:165-189), and there is a tiny window between
  // "previous removed from m.agents" and "new agent inserted" where
  // GetAgentStatus legitimately returns {isRunning:false}. A fetchStatus
  // landing in that window would flip our cached status to false,
  // ChatTab would auto-restart `chat`, and that racing startAgent would
  // immediately kill the just-spawned generate-tasks / start-all /
  // wildfire / generate-definition agent — the user observes "only chat
  // ever starts". The flag short-circuits fetchStatus during this window.
  startAgentInFlight: Record<string, boolean>

  fetchStatus: (projectId: string) => Promise<void>
  startAgent: (projectId: string, mode: string, opts?: {
    taskNumber?: number
    rows?: number
    cols?: number
  }) => Promise<AgentStatus>
  stopAgent: (projectId: string) => Promise<void>
  resumeAgent: (projectId: string) => Promise<void>
  sendInput: (projectId: string, data: Uint8Array) => Promise<void>
  resize: (projectId: string, rows: number, cols: number) => Promise<void>

  subscribeScreen: (
    projectId: string,
    onUpdate: (ansiContent: string, cols: number, rows: number) => void,
    onEnd?: () => void
  ) => AbortController
  subscribeRawOutput: (
    projectId: string,
    onData: (data: Uint8Array) => void,
    onEnd?: () => void,
    bytesReceived?: number
  ) => AbortController
  subscribeIssues: (
    projectId: string,
    onIssue: (issue: AgentIssue | null) => void
  ) => AbortController

  cleanupSubscriptions: (projectId: string) => void
}

export const useAgentStore = create<AgentState>((set, get) => ({
  statuses: {},
  issues: {},
  screenAborts: {},
  rawAborts: {},
  issueAborts: {},
  startAgentInFlight: {},

  fetchStatus: async (projectId) => {
    // Skip while a startAgent is in flight (#0104). The daemon's atomic
    // kill+restart has a small window where m.agents[projectId] is empty;
    // a fetchStatus landing there returns {isRunning:false}, which would
    // make ChatTab auto-restart `chat` and race-kill the just-spawned
    // special-mode agent. The flag is cleared by startAgent's finally,
    // so an RPC failure (e.g. the daemon's 10 s polling timeout) still
    // unblocks subsequent polls — they then drive the normal "agent is
    // gone, auto-start chat" recovery path.
    if (get().startAgentInFlight[projectId]) return
    try {
      const client = getAgentClient()
      const status = await client.getAgentStatus({ projectId })
      const existing = get().statuses[projectId]
      if (agentStatusEqual(existing, status)) return
      set((s) => ({ statuses: { ...s.statuses, [projectId]: status } }))
    } catch {
      // The daemon returns a clean {isRunning:false} via the happy path
      // (agent_service.go:247-256) when no agent is running. Reaching here
      // means a real transport error — a network blip or gRPC-Web framing
      // hiccup while the daemon is busy streaming raw output to the same
      // client. Preserve the last-known status: fabricating isRunning=false
      // is what made ChatTab auto-restart healthy chat agents in v7.0.0,
      // cascading into [Agent stopped] floods and line-stepping in the
      // chat xterm (#0101).
    }
  },

  startAgent: async (projectId, mode, opts = {}) => {
    set((s) => ({ startAgentInFlight: { ...s.startAgentInFlight, [projectId]: true } }))
    try {
      const client = getAgentClient()
      const status = await client.startAgent({
        projectId,
        mode,
        taskNumber: opts.taskNumber || 0,
        rows: opts.rows || 24,
        cols: opts.cols || 80
      })
      set((s) => ({ statuses: { ...s.statuses, [projectId]: status } }))
      return status
    } finally {
      set((s) => {
        const next = { ...s.startAgentInFlight }
        delete next[projectId]
        return { startAgentInFlight: next }
      })
    }
  },

  stopAgent: async (projectId) => {
    const client = getAgentClient()
    try {
      await client.stopAgent({ projectId })
    } catch {
      // Agent may already be stopped — ignore errors
    }
    set((s) => {
      const { [projectId]: _, ...rest } = s.statuses
      return { statuses: rest }
    })
  },

  resumeAgent: async (projectId) => {
    const client = getAgentClient()
    const status = await client.resumeAgent({ projectId })
    set((s) => ({
      statuses: { ...s.statuses, [projectId]: status },
      issues: { ...s.issues, [projectId]: null }
    }))
  },

  sendInput: async (projectId, data) => {
    const client = getAgentClient()
    await client.sendInput({ projectId, data })
  },

  resize: async (projectId, rows, cols) => {
    const client = getAgentClient()
    await client.resize({ projectId, rows, cols })
  },

  subscribeScreen: (projectId, onUpdate, onEnd) => {
    // Cancel existing subscription
    get().screenAborts[projectId]?.abort()

    const abort = new AbortController()
    set((s) => ({ screenAborts: { ...s.screenAborts, [projectId]: abort } }))

    const client = getAgentClient()
    ;(async () => {
      try {
        for await (const buf of client.subscribeScreen(
          { projectId },
          { signal: abort.signal }
        )) {
          onUpdate(buf.ansiContent, buf.cols, buf.rows)
        }
      } catch (err: unknown) {
        if (err instanceof Error && err.name !== 'AbortError') {
          console.error('Screen subscription error:', err)
        }
      } finally {
        onEnd?.()
      }
    })()

    return abort
  },

  subscribeRawOutput: (projectId, onData, onEnd, bytesReceived) => {
    // Cancel existing subscription (use rawAborts, not screenAborts)
    get().rawAborts[projectId]?.abort()

    const abort = new AbortController()
    set((s) => ({ rawAborts: { ...s.rawAborts, [projectId]: abort } }))

    const client = getAgentClient()
    // bytes_received (#0100) is the resume cursor — the daemon slices its
    // catch-up snapshot so only bytes past this offset arrive. Passing the
    // count the client has already written preserves scroll position on
    // reconnect (otherwise the GUI chat terminal snaps to byte 0).
    const cursor = BigInt(bytesReceived ?? 0)
    ;(async () => {
      try {
        for await (const chunk of client.subscribeRawOutput(
          { projectId, bytesReceived: cursor },
          { signal: abort.signal }
        )) {
          onData(chunk.data)
        }
      } catch (err: unknown) {
        if (err instanceof Error && err.name !== 'AbortError') {
          console.error('Raw output subscription error:', err)
        }
      } finally {
        onEnd?.()
      }
    })()

    return abort
  },

  subscribeIssues: (projectId, onIssue) => {
    get().issueAborts[projectId]?.abort()

    const abort = new AbortController()
    set((s) => ({ issueAborts: { ...s.issueAborts, [projectId]: abort } }))

    const client = getAgentClient()
    ;(async () => {
      try {
        for await (const issue of client.subscribeAgentIssues(
          { projectId },
          { signal: abort.signal }
        )) {
          const resolved = issue.issueType === '' ? null : issue
          onIssue(resolved)
          set((s) => ({ issues: { ...s.issues, [projectId]: resolved } }))
        }
      } catch (err: unknown) {
        if (err instanceof Error && err.name !== 'AbortError') {
          console.error('Issue subscription error:', err)
        }
      }
    })()

    return abort
  },

  cleanupSubscriptions: (projectId) => {
    get().screenAborts[projectId]?.abort()
    get().rawAborts[projectId]?.abort()
    get().issueAborts[projectId]?.abort()
  }
}))
