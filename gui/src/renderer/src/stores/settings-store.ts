import { create } from 'zustand'
import type { Settings } from '../generated/watchfire_pb'
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
      defaultBranch?: string
      defaultSandbox?: string
      defaultAgent?: string
    }
    updates?: {
      checkOnStartup?: boolean
      checkFrequency?: string
      autoDownload?: boolean
    }
    appearance?: {
      theme?: string
    }
  }) => Promise<void>
}

export const useSettingsStore = create<SettingsState>((set, get) => ({
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
      defaults: updates.defaults,
      updates: updates.updates,
      appearance: updates.appearance
    })
    set({ settings })
  }
}))
