import { create } from 'zustand'
import type { LogEntry } from '../generated/watchfire_pb'
import { getLogClient } from '../lib/grpc-client'

interface LogsState {
  logs: Record<string, LogEntry[]>
  selectedLogContent: string | null
  loading: boolean

  fetchLogs: (projectId: string) => Promise<void>
  getLogContent: (projectId: string, logId: string) => Promise<string>
}

export const useLogsStore = create<LogsState>((set) => ({
  logs: {},
  selectedLogContent: null,
  loading: false,

  fetchLogs: async (projectId) => {
    set({ loading: true })
    try {
      const client = getLogClient()
      const resp = await client.listLogs({ projectId })
      set((s) => ({
        logs: { ...s.logs, [projectId]: resp.logs },
        loading: false
      }))
    } catch {
      set({ loading: false })
    }
  },

  getLogContent: async (projectId, logId) => {
    const client = getLogClient()
    const resp = await client.getLog({ projectId, logId })
    const content = resp.content
    set({ selectedLogContent: content })
    return content
  }
}))
