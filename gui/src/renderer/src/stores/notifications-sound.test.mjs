// Renderer unit test for the notification-sound playback gating logic.
//
// Run with: node --test gui/src/renderer/src/stores/notifications-sound.test.mjs
//
// The helpers live in notifications-sound.ts but are re-implemented here as JS
// so the test runs without a TypeScript loader. Keep in sync with the .ts —
// any change to `shouldPlaySound`, `clampVolume`, or `playSoundForKind` must
// be mirrored below (and the matching test case added).

import { test } from 'node:test'
import assert from 'node:assert/strict'

const DEFAULT_SOUND_PREFS = {
  enabled: true,
  taskFailed: true,
  runComplete: true,
  volume: 0.6
}

function shouldPlaySound(kind, prefs) {
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

function clampVolume(v) {
  if (typeof v !== 'number' || Number.isNaN(v)) return DEFAULT_SOUND_PREFS.volume
  if (v < 0) return 0
  if (v > 1) return 1
  return v
}

async function playSoundForKind(kind, prefs, audios) {
  if (!shouldPlaySound(kind, prefs)) return false
  const audio = kind === 'TASK_FAILED' ? audios.taskFailed : audios.runComplete
  audio.volume = clampVolume(prefs?.volume)
  try {
    audio.currentTime = 0
  } catch {
    /* ignore */
  }
  try {
    await audio.play()
  } catch {
    /* ignore */
  }
  return true
}

function makeMockAudio() {
  return {
    volume: 1,
    currentTime: 1.5,
    playCalls: 0,
    play() {
      this.playCalls++
      return Promise.resolve()
    }
  }
}

function makeMockAudios() {
  return { taskFailed: makeMockAudio(), runComplete: makeMockAudio() }
}

// ---------- shouldPlaySound -------------------------------------------------

test('shouldPlaySound: master + per-event both on → plays', () => {
  const prefs = { enabled: true, taskFailed: true, runComplete: true, volume: 0.6 }
  assert.equal(shouldPlaySound('TASK_FAILED', prefs), true)
  assert.equal(shouldPlaySound('RUN_COMPLETE', prefs), true)
})

test('shouldPlaySound: master off → never plays', () => {
  const prefs = { enabled: false, taskFailed: true, runComplete: true, volume: 0.6 }
  assert.equal(shouldPlaySound('TASK_FAILED', prefs), false)
  assert.equal(shouldPlaySound('RUN_COMPLETE', prefs), false)
})

test('shouldPlaySound: per-event off but master on → that event silenced', () => {
  const prefs = { enabled: true, taskFailed: false, runComplete: true, volume: 0.6 }
  assert.equal(shouldPlaySound('TASK_FAILED', prefs), false)
  assert.equal(shouldPlaySound('RUN_COMPLETE', prefs), true)
})

test('shouldPlaySound: missing prefs → defaults apply (everything plays)', () => {
  assert.equal(shouldPlaySound('TASK_FAILED', undefined), true)
  assert.equal(shouldPlaySound('RUN_COMPLETE', undefined), true)
})

// ---------- clampVolume -----------------------------------------------------

test('clampVolume: in-range value passes through', () => {
  assert.equal(clampVolume(0.6), 0.6)
  assert.equal(clampVolume(0), 0)
  assert.equal(clampVolume(1), 1)
})

test('clampVolume: out-of-range clips to [0, 1]', () => {
  assert.equal(clampVolume(-0.5), 0)
  assert.equal(clampVolume(1.7), 1)
})

test('clampVolume: missing / NaN falls back to default 0.6', () => {
  assert.equal(clampVolume(undefined), 0.6)
  assert.equal(clampVolume(NaN), 0.6)
})

// ---------- playSoundForKind ------------------------------------------------

test('playSoundForKind: TASK_FAILED with master+event on → play() called on taskFailed', async () => {
  const audios = makeMockAudios()
  const prefs = { enabled: true, taskFailed: true, runComplete: true, volume: 0.4 }
  const played = await playSoundForKind('TASK_FAILED', prefs, audios)
  assert.equal(played, true)
  assert.equal(audios.taskFailed.playCalls, 1)
  assert.equal(audios.runComplete.playCalls, 0)
})

test('playSoundForKind: RUN_COMPLETE with master+event on → play() called on runComplete', async () => {
  const audios = makeMockAudios()
  const prefs = { enabled: true, taskFailed: true, runComplete: true, volume: 0.4 }
  const played = await playSoundForKind('RUN_COMPLETE', prefs, audios)
  assert.equal(played, true)
  assert.equal(audios.runComplete.playCalls, 1)
  assert.equal(audios.taskFailed.playCalls, 0)
})

test('playSoundForKind: master off → play() NOT called for either kind', async () => {
  const audios = makeMockAudios()
  const prefs = { enabled: false, taskFailed: true, runComplete: true, volume: 0.5 }
  assert.equal(await playSoundForKind('TASK_FAILED', prefs, audios), false)
  assert.equal(await playSoundForKind('RUN_COMPLETE', prefs, audios), false)
  assert.equal(audios.taskFailed.playCalls, 0)
  assert.equal(audios.runComplete.playCalls, 0)
})

test('playSoundForKind: per-event off but master on → that event NOT played, the other still plays', async () => {
  const audios = makeMockAudios()
  const prefs = { enabled: true, taskFailed: false, runComplete: true, volume: 0.5 }
  assert.equal(await playSoundForKind('TASK_FAILED', prefs, audios), false)
  assert.equal(audios.taskFailed.playCalls, 0)
  assert.equal(await playSoundForKind('RUN_COMPLETE', prefs, audios), true)
  assert.equal(audios.runComplete.playCalls, 1)
})

test('playSoundForKind: configured volume is applied to the played audio', async () => {
  const audios = makeMockAudios()
  const prefs = { enabled: true, taskFailed: true, runComplete: true, volume: 0.25 }
  await playSoundForKind('TASK_FAILED', prefs, audios)
  assert.equal(audios.taskFailed.volume, 0.25)
})

test('playSoundForKind: out-of-range volume is clipped before being applied', async () => {
  const audios = makeMockAudios()
  await playSoundForKind(
    'RUN_COMPLETE',
    { enabled: true, taskFailed: true, runComplete: true, volume: 9.9 },
    audios
  )
  assert.equal(audios.runComplete.volume, 1)
})

test('playSoundForKind: rewinds currentTime to 0 before playing so a rapid second notification restarts the sound', async () => {
  const audios = makeMockAudios()
  audios.taskFailed.currentTime = 0.4
  const prefs = { enabled: true, taskFailed: true, runComplete: true, volume: 0.6 }
  await playSoundForKind('TASK_FAILED', prefs, audios)
  assert.equal(audios.taskFailed.currentTime, 0)
})

test('playSoundForKind: a play() rejection is swallowed (autoplay-policy compliance)', async () => {
  const audios = makeMockAudios()
  audios.taskFailed.play = () => Promise.reject(new Error('NotAllowedError'))
  const prefs = { enabled: true, taskFailed: true, runComplete: true, volume: 0.6 }
  // Must NOT throw. Returns true because the gating decision was "play it".
  const played = await playSoundForKind('TASK_FAILED', prefs, audios)
  assert.equal(played, true)
})
