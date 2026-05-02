import { create } from 'zustand'
import { getDaemonClient } from '../lib/grpc-client'
import { FocusTarget } from '../generated/watchfire_pb'
import { useAppStore, type FocusRequestTarget } from './app-store'

// Module-level subscription handle — we never want more than one stream open
// at a time across the renderer's lifetime.
let activeAbort: AbortController | null = null
let restartTimer: ReturnType<typeof setTimeout> | null = null

interface FocusStoreState {
  active: boolean
  start: () => void
  stop: () => void
}

function targetToRequest(t: FocusTarget): FocusRequestTarget {
  switch (t) {
    case FocusTarget.TASKS:
      return 'tasks'
    case FocusTarget.TASK:
      return 'task'
    default:
      return 'main'
  }
}

async function consume(abort: AbortController): Promise<void> {
  const client = getDaemonClient()
  const stream = client.subscribeFocusEvents({}, { signal: abort.signal })
  try {
    for await (const ev of stream) {
      if (abort.signal.aborted) return
      // Bring the GUI window to the foreground on every event. The main
      // process's `focus-window` IPC handles minimised / hidden windows.
      try {
        await window.watchfire.focusWindow()
      } catch {
        /* ignore */
      }
      // No project ID means "open Watchfire / dashboard" — drop the project
      // selection and switch to dashboard view.
      if (!ev.projectId) {
        useAppStore.getState().setView('dashboard')
        continue
      }
      useAppStore.getState().requestFocus({
        projectId: ev.projectId,
        target: targetToRequest(ev.target),
        taskNumber: ev.taskNumber || undefined
      })
    }
  } catch (err) {
    if (abort.signal.aborted) return
    // Daemon went away or stream errored — schedule a reconnect attempt with
    // a short delay so we don't hammer a downed daemon.
    console.warn('focus-stream error, retrying in 2s', err)
    if (!restartTimer) {
      restartTimer = setTimeout(() => {
        restartTimer = null
        if (useFocusStore.getState().active) {
          internalStart()
        }
      }, 2000)
    }
  }
}

function internalStart(): void {
  if (activeAbort) return
  activeAbort = new AbortController()
  void consume(activeAbort)
}

function internalStop(): void {
  if (restartTimer) {
    clearTimeout(restartTimer)
    restartTimer = null
  }
  if (activeAbort) {
    activeAbort.abort()
    activeAbort = null
  }
}

export const useFocusStore = create<FocusStoreState>((set) => ({
  active: false,
  start: () => {
    set({ active: true })
    internalStart()
  },
  stop: () => {
    set({ active: false })
    internalStop()
  }
}))
