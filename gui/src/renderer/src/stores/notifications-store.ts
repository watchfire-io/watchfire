import { create } from 'zustand'
import { useSettingsStore } from './settings-store'
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
  title: string
  body: string
  emittedAt: number // ms since epoch
}

interface NotificationsStoreState {
  // Ring-buffered last 50 notifications, newest first. 0049 populates this
  // from the gRPC stream; consumers (in-app notification center, future) read
  // it directly.
  recent: NotificationRecord[]
  notify: (kind: NotificationKind, record?: Partial<NotificationRecord>) => Promise<boolean>
}

const RECENT_CAP = 50

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

export const useNotificationsStore = create<NotificationsStoreState>((set, get) => {
  // Warm up the audio elements at store creation so the first incoming
  // notification doesn't pay the constructor cost.
  getAudios()
  return {
    recent: [],
    notify: async (kind, record) => {
      const now = Date.now()
      const entry: NotificationRecord = {
        id: record?.id ?? `${kind}-${now}`,
        kind,
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
    }
  }
})
