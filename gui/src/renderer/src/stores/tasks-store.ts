import { create } from 'zustand'
import type { Task } from '../generated/watchfire_pb'
import { getTaskClient } from '../lib/grpc-client'

interface TasksState {
  tasks: Record<string, Task[]>
  loading: boolean
  error: string | null

  fetchTasks: (projectId: string, includeDeleted?: boolean) => Promise<void>
  createTask: (projectId: string, title: string, prompt: string, opts?: {
    acceptanceCriteria?: string
    status?: string
    position?: number
  }) => Promise<Task>
  updateTask: (projectId: string, taskNumber: number, updates: {
    title?: string
    prompt?: string
    acceptanceCriteria?: string
    status?: string
    position?: number
  }) => Promise<void>
  deleteTask: (projectId: string, taskNumber: number) => Promise<void>
  restoreTask: (projectId: string, taskNumber: number) => Promise<void>
  emptyTrash: (projectId: string) => Promise<void>
  reorderTasks: (projectId: string, taskNumbers: number[]) => Promise<void>
}

export const useTasksStore = create<TasksState>((set, get) => ({
  tasks: {},
  loading: false,
  error: null,

  fetchTasks: async (projectId, includeDeleted = false) => {
    set({ loading: true, error: null })
    try {
      const client = getTaskClient()
      const resp = await client.listTasks({ projectId, includeDeleted })
      set((s) => ({
        tasks: { ...s.tasks, [projectId]: resp.tasks },
        loading: false
      }))
    } catch (err) {
      set({ error: String(err), loading: false })
    }
  },

  createTask: async (projectId, title, prompt, opts = {}) => {
    const client = getTaskClient()
    const task = await client.createTask({
      projectId,
      title,
      prompt,
      status: opts.status || 'draft',
      acceptanceCriteria: opts.acceptanceCriteria,
      position: opts.position
    })
    get().fetchTasks(projectId)
    return task
  },

  updateTask: async (projectId, taskNumber, updates) => {
    const client = getTaskClient()
    await client.updateTask({
      projectId,
      taskNumber,
      ...updates
    })
    get().fetchTasks(projectId)
  },

  deleteTask: async (projectId, taskNumber) => {
    const client = getTaskClient()
    await client.deleteTask({ projectId, taskNumber })
    get().fetchTasks(projectId, true)
  },

  restoreTask: async (projectId, taskNumber) => {
    const client = getTaskClient()
    await client.restoreTask({ projectId, taskNumber })
    get().fetchTasks(projectId, true)
  },

  emptyTrash: async (projectId) => {
    const client = getTaskClient()
    await client.emptyTrash({ projectId })
    get().fetchTasks(projectId, true)
  },

  reorderTasks: async (projectId, taskNumbers) => {
    const client = getTaskClient()
    await client.reorderTasks({ projectId, taskNumbers })
    get().fetchTasks(projectId)
  }
}))
