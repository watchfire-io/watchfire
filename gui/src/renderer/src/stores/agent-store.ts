import { create } from 'zustand'
import type { AgentStatus, AgentIssue } from '../generated/watchfire_pb'
import { getAgentClient } from '../lib/grpc-client'

interface AgentState {
  statuses: Record<string, AgentStatus>
  issues: Record<string, AgentIssue | null>
  screenAborts: Record<string, AbortController>
  issueAborts: Record<string, AbortController>

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
    onUpdate: (ansiContent: string) => void,
    onEnd?: () => void
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
  issueAborts: {},

  fetchStatus: async (projectId) => {
    try {
      const client = getAgentClient()
      const status = await client.getAgentStatus({ projectId })
      set((s) => ({ statuses: { ...s.statuses, [projectId]: status } }))
    } catch {
      // not running — set explicit idle status so consumers know the fetch completed
      set((s) => ({
        statuses: { ...s.statuses, [projectId]: { isRunning: false } as AgentStatus }
      }))
    }
  },

  startAgent: async (projectId, mode, opts = {}) => {
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
  },

  stopAgent: async (projectId) => {
    const client = getAgentClient()
    await client.stopAgent({ projectId })
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
          onUpdate(buf.ansiContent)
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
    get().issueAborts[projectId]?.abort()
  }
}))
