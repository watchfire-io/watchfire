import { create } from 'zustand'
import { useSettingsStore } from './settings-store'
import { getTransport } from '../lib/grpc-client'
import { createClient } from '@connectrpc/connect'
import {
  NotificationService,
  NotificationKind as PbNotificationKind
} from '../generated/watchfire_pb'
import {
  DEFAULT_SOUND_PREFS,
  playSoundForKind,
  shouldPlaySound,
  type NotificationKind,
  type PlayableAudio
} from './notifications-sound'

// Vite serves the renderer's `public/` dir at the root of the bundled output,
// so these absolute URLs resolve to the bundled WAV files at runtime in both
// dev and production. The canonical source-of-truth copies live in the repo
// root at `assets/sounds/` — see `assets/sounds/README.md` for the duplication
// rationale (Vite's `?url` import doesn't reach files outside the renderer
// root, and the WAVs are tiny + never change).
const taskDoneUrl = '/sounds/task-done.wav'
const taskFailedUrl = '/sounds/task-failed.wav'

export { DEFAULT_SOUND_PREFS, shouldPlaySound, playSoundForKind, type NotificationKind }

interface NotificationRecord {
  id: string
  kind: NotificationKind
  projectId: string
  taskNumber: number
  title: string
  body: string
  emittedAt: number // ms since epoch
}

interface NotificationsStoreState {
  // Ring-buffered last 50 notifications, newest first. Populated from the
  // gRPC stream; consumers (in-app notification center, future) read it
  // directly.
  recent: NotificationRecord[]
  active: boolean
  notify: (kind: NotificationKind, record?: Partial<NotificationRecord>) => Promise<boolean>
  start: () => void
  stop: () => void
}

const RECENT_CAP = 50

// Module-level subscription handle — at most one stream open across the
// renderer's lifetime. Mirrors the focus-store's restart pattern: a transient
// stream error (daemon flap, network blip) schedules a reconnect with a short
// delay so we don't hammer a downed daemon.
let activeAbort: AbortController | null = null
let restartTimer: ReturnType<typeof setTimeout> | null = null

// Module-level audio elements. Constructed lazily on first access so node
// --test environments without `window.Audio` don't crash on import; in the
// real renderer they're warmed up at store creation time so the first
// playback isn't laggy.
let cachedAudios: { taskFailed: PlayableAudio; runComplete: PlayableAudio } | null = null

function getAudios(): { taskFailed: PlayableAudio; runComplete: PlayableAudio } | null {
  if (cachedAudios) return cachedAudios
  if (typeof Audio === 'undefined') return null
  const taskFailed = new Audio(taskFailedUrl)
  const runComplete = new Audio(taskDoneUrl)
  // preload="auto" hints the renderer to fetch the bytes immediately so the
  // first play() doesn't pay the network/disk round-trip latency.
  taskFailed.preload = 'auto'
  runComplete.preload = 'auto'
  // Touch load() defensively — some Electron versions need an explicit kick
  // on a freshly-constructed Audio to actually start fetching.
  try {
    taskFailed.load()
    runComplete.load()
  } catch {
    /* ignore */
  }
  cachedAudios = { taskFailed, runComplete }
  return cachedAudios
}

function pbKindToString(k: PbNotificationKind): NotificationKind {
  switch (k) {
    case PbNotificationKind.RUN_COMPLETE:
      return 'RUN_COMPLETE'
    case PbNotificationKind.WEEKLY_DIGEST:
      return 'WEEKLY_DIGEST'
    case PbNotificationKind.TASK_FAILED:
    default:
      return 'TASK_FAILED'
  }
}

async function consume(abort: AbortController): Promise<void> {
  const client = createClient(NotificationService, getTransport())
  const stream = client.subscribe({}, { signal: abort.signal })
  try {
    for await (const ev of stream) {
      if (abort.signal.aborted) return
      const kind = pbKindToString(ev.kind)
      const emittedAtMs = ev.emittedAt
        ? Number(ev.emittedAt.seconds) * 1000 + Math.floor(ev.emittedAt.nanos / 1_000_000)
        : Date.now()
      void useNotificationsStore.getState().notify(kind, {
        id: ev.id,
        title: ev.title,
        body: ev.body,
        projectId: ev.projectId,
        taskNumber: ev.taskNumber,
        emittedAt: emittedAtMs
      })

      // Hand off to the main process so Electron can show a native OS
      // Notification (with silent: true — the renderer plays its own
      // sound via notify() above when the user has enabled it).
      try {
        await window.watchfire.emitNotification({
          id: ev.id,
          kind,
          projectId: ev.projectId,
          taskNumber: ev.taskNumber,
          title: ev.title,
          body: ev.body
        })
      } catch (err) {
        console.warn('emitNotification IPC failed', err)
      }
    }
  } catch (err) {
    if (abort.signal.aborted) return
    console.warn('notifications stream error, retrying in 2s', err)
    if (!restartTimer) {
      restartTimer = setTimeout(() => {
        restartTimer = null
        if (useNotificationsStore.getState().active) {
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

export const useNotificationsStore = create<NotificationsStoreState>((set, get) => {
  // Warm up the audio elements at store creation so the first incoming
  // notification doesn't pay the constructor cost.
  getAudios()
  return {
    recent: [],
    active: false,
    notify: async (kind, record) => {
      const now = Date.now()
      const entry: NotificationRecord = {
        id: record?.id ?? `${kind}-${now}`,
        kind,
        projectId: record?.projectId ?? '',
        taskNumber: record?.taskNumber ?? 0,
        title: record?.title ?? '',
        body: record?.body ?? '',
        emittedAt: record?.emittedAt ?? now
      }
      const next = [entry, ...get().recent].slice(0, RECENT_CAP)
      set({ recent: next })

      const audios = getAudios()
      if (!audios) return false
      const prefs = useSettingsStore.getState().settings?.defaults?.notifications?.sounds
      return playSoundForKind(kind, prefs, audios)
    },
    start: () => {
      set({ active: true })
      internalStart()
    },
    stop: () => {
      set({ active: false })
      internalStop()
    }
  }
})
