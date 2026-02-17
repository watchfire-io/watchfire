import { create } from 'zustand'
import type { Project, AgentStatus } from '../generated/watchfire_pb'
import { getProjectClient, getAgentClient } from '../lib/grpc-client'

interface ProjectsState {
  projects: Project[]
  agentStatuses: Record<string, AgentStatus>
  loading: boolean
  error: string | null

  fetchProjects: () => Promise<void>
  fetchAgentStatus: (projectId: string) => Promise<void>
  fetchAllAgentStatuses: () => Promise<void>
}

export const useProjectsStore = create<ProjectsState>((set, get) => ({
  projects: [],
  agentStatuses: {},
  loading: false,
  error: null,

  fetchProjects: async () => {
    set({ loading: true, error: null })
    try {
      const client = getProjectClient()
      const resp = await client.listProjects({})
      set({ projects: resp.projects, loading: false })
      // Fetch agent statuses for all projects
      get().fetchAllAgentStatuses()
    } catch (err) {
      set({ error: String(err), loading: false })
    }
  },

  fetchAgentStatus: async (projectId) => {
    try {
      const client = getAgentClient()
      const status = await client.getAgentStatus({ projectId })
      set((s) => ({
        agentStatuses: { ...s.agentStatuses, [projectId]: status }
      }))
    } catch {
      // Agent not running is not an error
    }
  },

  fetchAllAgentStatuses: async () => {
    const { projects } = get()
    for (const p of projects) {
      get().fetchAgentStatus(p.projectId)
    }
  }
}))
