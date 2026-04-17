import { create } from 'zustand'
import type { AgentInfo } from '../generated/watchfire_pb'
import { getSettingsClient } from '../lib/grpc-client'

interface AgentsState {
  agents: AgentInfo[]
  loaded: boolean
  loading: boolean
  error: string | null

  ensureLoaded: () => Promise<void>
}

export const useAgentsStore = create<AgentsState>((set, get) => ({
  agents: [],
  loaded: false,
  loading: false,
  error: null,

  ensureLoaded: async () => {
    const { loaded, loading } = get()
    if (loaded || loading) return
    set({ loading: true, error: null })
    try {
      const res = await getSettingsClient().listAgents({})
      set({ agents: res.agents, loaded: true, loading: false })
    } catch (err) {
      set({ error: String(err), loading: false, loaded: true })
    }
  }
}))
