// v6.0 Ember diff cache.
//
// One zustand selector caches each `(projectId, taskNumber)` pair so the
// InspectTab can mount/unmount without re-issuing the gRPC call. The
// daemon also caches under `~/.watchfire/diff-cache/`, so even a cache
// miss here is cheap — this just keeps the active session snappy.

import { create } from 'zustand'
import { create as createMessage } from '@bufbuild/protobuf'
import {
  GetTaskDiffRequestSchema,
  type FileDiffSet
} from '../generated/watchfire_pb'
import { getInsightsClient } from '../lib/grpc-client'

interface DiffEntry {
  loading: boolean
  error: string | null
  data: FileDiffSet | null
}

interface DiffStore {
  entries: Record<string, DiffEntry>
  fetch: (projectId: string, taskNumber: number, force?: boolean) => Promise<void>
}

const cacheKey = (projectId: string, taskNumber: number) =>
  `${projectId}:${taskNumber}`

export const useDiffStore = create<DiffStore>((set, get) => ({
  entries: {},

  fetch: async (projectId, taskNumber, force = false) => {
    if (!projectId || taskNumber <= 0) return
    const key = cacheKey(projectId, taskNumber)
    const existing = get().entries[key]
    // Hit the renderer-side cache unless caller forced a refresh.
    if (!force && existing && !existing.loading && existing.data) return

    set((s) => ({
      entries: {
        ...s.entries,
        [key]: { loading: true, error: null, data: existing?.data ?? null }
      }
    }))

    try {
      const req = createMessage(GetTaskDiffRequestSchema)
      req.projectId = projectId
      req.taskNumber = taskNumber
      const client = getInsightsClient()
      const data = await client.getTaskDiff(req)
      set((s) => ({
        entries: {
          ...s.entries,
          [key]: { loading: false, error: null, data }
        }
      }))
    } catch (err) {
      set((s) => ({
        entries: {
          ...s.entries,
          [key]: {
            loading: false,
            error: err instanceof Error ? err.message : String(err),
            data: null
          }
        }
      }))
    }
  }
}))

export function selectDiff(
  projectId: string,
  taskNumber: number
): (state: DiffStore) => DiffEntry | undefined {
  const key = cacheKey(projectId, taskNumber)
  return (state) => state.entries[key]
}
