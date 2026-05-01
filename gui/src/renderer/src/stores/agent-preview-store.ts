import { create } from 'zustand'
import { getAgentClient } from '../lib/grpc-client'

const THROTTLE_MS = 250
const MAX_LINE_CHARS = 500

interface PreviewState {
  previews: Record<string, string>
  setPreview: (projectId: string, value: string) => void
  clearPreview: (projectId: string) => void
}

export const useAgentPreviewStore = create<PreviewState>((set) => ({
  previews: {},
  setPreview: (projectId, value) =>
    set((s) =>
      s.previews[projectId] === value
        ? s
        : { previews: { ...s.previews, [projectId]: value } }
    ),
  clearPreview: (projectId) =>
    set((s) => {
      if (!(projectId in s.previews)) return s
      const next = { ...s.previews }
      delete next[projectId]
      return { previews: next }
    })
}))

interface SubscriptionEntry {
  abort: AbortController
  refCount: number
  lastEmitAt: number
  pendingValue: string | null
  pendingTimer: ReturnType<typeof setTimeout> | null
}

const subscriptions = new Map<string, SubscriptionEntry>()

function lastNonBlank(lines: string[]): string {
  for (let i = lines.length - 1; i >= 0; i--) {
    const line = lines[i] ?? ''
    if (line.trim().length === 0) continue
    const trimmed = line.trim()
    return trimmed.length > MAX_LINE_CHARS ? trimmed.slice(0, MAX_LINE_CHARS) : trimmed
  }
  return ''
}

function flushEmit(projectId: string): void {
  const entry = subscriptions.get(projectId)
  if (!entry) return
  entry.pendingTimer = null
  entry.lastEmitAt = Date.now()
  const pending = entry.pendingValue
  entry.pendingValue = null
  if (pending !== null) {
    useAgentPreviewStore.getState().setPreview(projectId, pending)
  }
}

function scheduleEmit(projectId: string, value: string): void {
  const entry = subscriptions.get(projectId)
  if (!entry) return
  const now = Date.now()
  const elapsed = now - entry.lastEmitAt
  if (elapsed >= THROTTLE_MS) {
    entry.lastEmitAt = now
    entry.pendingValue = null
    if (entry.pendingTimer) {
      clearTimeout(entry.pendingTimer)
      entry.pendingTimer = null
    }
    useAgentPreviewStore.getState().setPreview(projectId, value)
    return
  }
  entry.pendingValue = value
  if (entry.pendingTimer) return
  entry.pendingTimer = setTimeout(() => flushEmit(projectId), THROTTLE_MS - elapsed)
}

function openStream(projectId: string): SubscriptionEntry {
  const abort = new AbortController()
  const entry: SubscriptionEntry = {
    abort,
    refCount: 0,
    lastEmitAt: 0,
    pendingValue: null,
    pendingTimer: null
  }
  subscriptions.set(projectId, entry)

  void (async () => {
    const client = getAgentClient()
    try {
      for await (const buf of client.subscribeScreen(
        { projectId },
        { signal: abort.signal }
      )) {
        scheduleEmit(projectId, lastNonBlank(buf.lines))
      }
    } catch (err: unknown) {
      if (err instanceof Error && err.name !== 'AbortError') {
        console.error('Agent preview subscription error:', err)
      }
    } finally {
      const e = subscriptions.get(projectId)
      if (e?.pendingTimer) clearTimeout(e.pendingTimer)
      subscriptions.delete(projectId)
      useAgentPreviewStore.getState().clearPreview(projectId)
    }
  })()

  return entry
}

/**
 * Acquires a shared, ref-counted subscription to the agent's screen buffer for `projectId`.
 * The first caller opens the underlying gRPC stream; subsequent callers reuse it. Returns
 * a release function — the stream is aborted only when the last consumer releases.
 *
 * Updates to `useAgentPreviewStore.previews[projectId]` are throttled to <= 4 Hz.
 */
export function acquireAgentPreview(projectId: string): () => void {
  let entry = subscriptions.get(projectId)
  if (!entry) entry = openStream(projectId)
  entry.refCount++

  let released = false
  return () => {
    if (released) return
    released = true
    const e = subscriptions.get(projectId)
    if (!e) return
    e.refCount--
    if (e.refCount <= 0) e.abort.abort()
  }
}
