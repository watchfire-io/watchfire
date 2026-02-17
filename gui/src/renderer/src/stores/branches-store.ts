import { create } from 'zustand'
import type { Branch } from '../generated/watchfire_pb'
import { getBranchClient } from '../lib/grpc-client'

interface BranchesState {
  branches: Record<string, Branch[]>
  loading: boolean

  fetchBranches: (projectId: string) => Promise<void>
  mergeBranch: (projectId: string, branchName: string, deleteAfter?: boolean) => Promise<void>
  deleteBranch: (projectId: string, branchName: string) => Promise<void>
  pruneBranches: (projectId: string) => Promise<void>
}

export const useBranchesStore = create<BranchesState>((set, get) => ({
  branches: {},
  loading: false,

  fetchBranches: async (projectId) => {
    set({ loading: true })
    try {
      const client = getBranchClient()
      const resp = await client.listBranches({ projectId })
      set((s) => ({
        branches: { ...s.branches, [projectId]: resp.branches },
        loading: false
      }))
    } catch {
      set({ loading: false })
    }
  },

  mergeBranch: async (projectId, branchName, deleteAfter = false) => {
    const client = getBranchClient()
    await client.mergeBranch({ projectId, branchName, deleteAfterMerge: deleteAfter })
    get().fetchBranches(projectId)
  },

  deleteBranch: async (projectId, branchName) => {
    const client = getBranchClient()
    await client.deleteBranch({ projectId, branchName })
    get().fetchBranches(projectId)
  },

  pruneBranches: async (projectId) => {
    const client = getBranchClient()
    await client.pruneBranches({ projectId })
    get().fetchBranches(projectId)
  }
}))
