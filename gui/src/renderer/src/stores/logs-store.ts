import { create } from 'zustand'
import type { LogEntry } from '../generated/watchfire_pb'
import { getLogClient } from '../lib/grpc-client'

interface LogsState {
  logs: Record<string, LogEntry[]>
  selectedLogContent: string | null
  loading: boolean
  error: Record<string, string | null>

  fetchLogs: (projectId: string) => Promise<void>
  getLogContent: (projectId: string, logId: string) => Promise<string>
}

export const useLogsStore = create<LogsState>((set) => ({
  logs: {},
  selectedLogContent: null,
  loading: false,
  error: {},

  fetchLogs: async (projectId) => {
    set({ loading: true })
    try {
      const client = getLogClient()
      const resp = await client.listLogs({ projectId })
      set((s) => ({
        logs: { ...s.logs, [projectId]: resp.logs },
        error: { ...s.error, [projectId]: null },
        loading: false
      }))
    } catch (err) {
      console.error(`[logs-store] fetchLogs failed for project ${projectId}:`, err)
      set((s) => ({
        loading: false,
        error: { ...s.error, [projectId]: String(err) }
      }))
    }
  },

  getLogContent: async (projectId, logId) => {
    try {
      const client = getLogClient()
      const resp = await client.getLog({ projectId, logId })
      const content = resp.content
      set({ selectedLogContent: content })
      return content
    } catch (err) {
      console.error(`[logs-store] getLogContent failed for ${projectId}/${logId}:`, err)
      throw err
    }
  }
}))
