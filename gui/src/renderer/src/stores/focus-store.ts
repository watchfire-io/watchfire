import { create } from 'zustand'
import { getDaemonClient } from '../lib/grpc-client'
import { FocusTarget } from '../generated/watchfire_pb'
import { useAppStore, type FocusRequestTarget } from './app-store'
import { useDigestStore } from './digest-store'

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
      // v8 Inferno — tray → open/focus the relevant project window. Only the
      // home window subscribes to this stream (App.tsx gates `startFocus()` on
      // `isHomeWindow()`, mirroring the D1 single-notifier election), so it
      // owns routing for the whole app and delegates window targeting to the
      // main process, which owns the window registry. Each branch surfaces the
      // OS window the event is about rather than a generic first/most-recent
      // window.

      // v6.0 Ember — DIGEST target is global: surface the home window and open
      // the digest modal with the named date (the modal lives in this window).
      if (ev.target === FocusTarget.DIGEST) {
        void window.watchfire.openHomeWindow()
        const date = ev.digestDate
        if (date) void useDigestStore.getState().open(date)
        continue
      }
      // No project ID means "open Watchfire / dashboard" — focus the home
      // window and switch it to the dashboard view.
      if (!ev.projectId) {
        void window.watchfire.openHomeWindow()
        useAppStore.getState().setView('dashboard')
        continue
      }
      // A project event opens (or focuses) that project's own window and routes
      // its renderer to the relevant tab/task. The main process creates the
      // window if it isn't open yet and defers the routing message until the
      // renderer has loaded.
      void window.watchfire.focusProjectWindow(
        ev.projectId,
        targetToRequest(ev.target),
        ev.taskNumber || undefined
      )
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
