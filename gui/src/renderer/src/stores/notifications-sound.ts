// Pure helpers for the renderer's notification-sound playback. Lives in its
// own file (no Vite-specific `?url` imports) so node --test can exercise the
// preference-gating + volume logic without spinning up the renderer bundle.
import type { NotificationsSounds } from '../generated/watchfire_pb'

// NotificationKind mirrors the daemon's `proto/watchfire.proto` enum. Defined
// locally for now because the proto / generated TS bindings land alongside
// the `NotificationService` stream in task 0049 — once that ships, this type
// switches to importing the generated enum and the string literals stay
// compatible at the runtime level.
export type NotificationKind = 'TASK_FAILED' | 'RUN_COMPLETE'

// Defaults match `internal/models/settings.go:DefaultNotifications`. Used
// when the settings RPC reply hasn't landed yet OR fields are absent in an
// older settings.yaml — task 0053's settings UI persists explicit values,
// but if the renderer plays a sound before that happens we still want sane
// behaviour without rewriting the YAML.
export const DEFAULT_SOUND_PREFS: NotificationsSounds = {
  $typeName: 'watchfire.NotificationsSounds',
  enabled: true,
  taskFailed: true,
  runComplete: true,
  volume: 0.6
} as unknown as NotificationsSounds

/**
 * Pure decision: should the renderer play its own sound for `kind`?
 *
 * The OS Notification's `silent` flag mirrors this exact answer (passed as
 * `silent: !shouldPlaySound(kind, prefs)`) so we never double-quiet — the OS
 * stays silent precisely when the renderer would have played its own audio,
 * and vice versa.
 */
export function shouldPlaySound(
  kind: NotificationKind,
  prefs: NotificationsSounds | undefined
): boolean {
  const sounds = prefs ?? DEFAULT_SOUND_PREFS
  if (!sounds.enabled) return false
  switch (kind) {
    case 'TASK_FAILED':
      return sounds.taskFailed ?? true
    case 'RUN_COMPLETE':
      return sounds.runComplete ?? true
    default:
      return false
  }
}

export function clampVolume(v: number | undefined): number {
  if (typeof v !== 'number' || Number.isNaN(v)) return DEFAULT_SOUND_PREFS.volume
  if (v < 0) return 0
  if (v > 1) return 1
  return v
}

// Minimal interface that matches HTMLAudioElement's surface used by playback,
// so tests can mock it without constructing a real Audio.
export interface PlayableAudio {
  volume: number
  currentTime: number
  play(): Promise<void> | void
}

/**
 * Plays the sound for a notification kind, if enabled. Pure-ish: takes the
 * audio element to play, the prefs, and the audio pair (one per kind). Lifted
 * out of the store so it's directly testable without spinning up a zustand
 * instance + DOM Audio constructor.
 *
 * Returns true if a play attempt was made, false if the prefs gated it out.
 * The returned promise resolves either way; play() rejections are swallowed
 * because Chrome/Electron block autoplay until first user interaction (in
 * practice the user has interacted by the time a notification fires).
 */
export async function playSoundForKind(
  kind: NotificationKind,
  prefs: NotificationsSounds | undefined,
  audios: { taskFailed: PlayableAudio; runComplete: PlayableAudio }
): Promise<boolean> {
  if (!shouldPlaySound(kind, prefs)) return false
  const audio = kind === 'TASK_FAILED' ? audios.taskFailed : audios.runComplete
  audio.volume = clampVolume(prefs?.volume)
  try {
    audio.currentTime = 0
  } catch {
    // currentTime can throw on a not-yet-loaded element in some browsers;
    // play() will still work and start from 0 in that case.
  }
  try {
    await audio.play()
  } catch {
    // Autoplay policy: rejections are silently swallowed.
  }
  return true
}
