import { create } from 'zustand'
import type { Project, AgentStatus } from '../generated/watchfire_pb'
import { getProjectClient, getAgentClient } from '../lib/grpc-client'
import { agentStatusEqual } from '../lib/agent-utils'
import { useTerminalStore } from './terminal-store'

interface ProjectsState {
  projects: Project[]
  agentStatuses: Record<string, AgentStatus>
  loading: boolean
  error: string | null

  fetchProjects: () => Promise<void>
  fetchAgentStatus: (projectId: string) => Promise<void>
  fetchAllAgentStatuses: () => Promise<void>
  updateProjectLocal: (projectId: string, updates: Partial<Project>) => void
  reorderProjects: (projectIds: string[]) => Promise<void>
  removeProject: (projectId: string) => Promise<void>
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
      const existing = get().agentStatuses[projectId]
      if (agentStatusEqual(existing, status)) return
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
  },

  updateProjectLocal: (projectId, updates) => {
    set((s) => ({
      projects: s.projects.map((p) =>
        p.projectId === projectId ? { ...p, ...updates } : p
      )
    }))
  },

  reorderProjects: async (projectIds) => {
    // Optimistic local reorder
    const { projects } = get()
    const projectMap = new Map(projects.map((p) => [p.projectId, p]))
    const reordered = projectIds
      .map((id) => projectMap.get(id))
      .filter((p): p is Project => !!p)
    // Append any not in the list
    for (const p of projects) {
      if (!projectIds.includes(p.projectId)) reordered.push(p)
    }
    set({ projects: reordered })

    try {
      const client = getProjectClient()
      const resp = await client.reorderProjects({ projectIds })
      set({ projects: resp.projects })
    } catch {
      // Revert on error
      set({ projects })
    }
  },

  removeProject: async (projectId) => {
    try {
      const client = getProjectClient()
      await client.deleteProject({ projectId })
      // Destroy any terminal sessions tied to this project so they don't
      // linger as orphans in the global pool with a stale projectId.
      await useTerminalStore.getState().destroyProjectSessions(projectId)
      await get().fetchProjects()
    } catch (err) {
      set({ error: String(err) })
    }
  }
}))
