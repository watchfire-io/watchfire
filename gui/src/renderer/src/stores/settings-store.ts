import { create } from 'zustand'
import type { NotificationsConfig, Settings } from '../generated/watchfire_pb'
import { getSettingsClient } from '../lib/grpc-client'

interface SettingsState {
  settings: Settings | null
  loading: boolean

  fetchSettings: () => Promise<void>
  updateSettings: (updates: {
    defaults?: {
      autoMerge?: boolean
      autoDeleteBranch?: boolean
      autoStartTasks?: boolean
      defaultSandbox?: string
      defaultAgent?: string
      notifications?: NotificationsConfig
      terminalShell?: string
    }
    updates?: {
      checkOnStartup?: boolean
      checkFrequency?: string
      autoDownload?: boolean
    }
    appearance?: {
      theme?: string
    }
    agents?: { [key: string]: { path: string } }
  }) => Promise<void>
}

export const useSettingsStore = create<SettingsState>((set) => ({
  settings: null,
  loading: false,

  fetchSettings: async () => {
    set({ loading: true })
    try {
      const client = getSettingsClient()
      const settings = await client.getSettings({})
      set({ settings, loading: false })
    } catch {
      set({ loading: false })
    }
  },

  updateSettings: async (updates) => {
    const client = getSettingsClient()
    const settings = await client.updateSettings({
      defaults: updates.defaults as never,
      updates: updates.updates as never,
      appearance: updates.appearance as never,
      agents: updates.agents
    })
    set({ settings })
  }
}))
