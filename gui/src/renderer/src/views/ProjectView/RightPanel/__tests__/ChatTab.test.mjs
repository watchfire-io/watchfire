// Regression test for the v5.0 "GUI chat-mode UI repetition" bug.
//
// Run with: node --test gui/src/renderer/src/views/ProjectView/RightPanel/__tests__/ChatTab.test.mjs
//
// The original bug (spec: .watchfire/specs/v5.md § "GUI chat-mode panel
// renders exactly once") lived in `ProjectView/ChatPanel.tsx`, where a
// missing `chatModeActive` dep on a `useEffect` caused the chat overlay
// to render multiple times when a project with cycled-through tasks
// auto-routed to chat mode.
//
// The chat surface has since moved to `RightPanel/ChatTab.tsx`, which is
// a TAB (not a project mode). It mounts via a single conditional render
// (`{tab === 'chat' && <ChatTab .../>}`) so the multiple-panel symptom
// is structurally impossible. What can still regress is the auto-start
// guard: if `autoStarted.current` were ever reset on the wrong edge, or
// if the projectId-change reset stopped firing, the user would see
// `startAgent` called many times on a single navigation transition.
//
// This test mirrors the guard logic from ChatTab.tsx (the three useEffect
// blocks at lines 30-40 and 53-58) as plain functions and asserts that
// `startAgent` is invoked at most once per project-switch transition,
// even under rapid status polling.

import { test } from 'node:test'
import assert from 'node:assert/strict'

// --- Logic mirror ----------------------------------------------------------
// Mirrors the three guard-bearing useEffects in
// gui/src/renderer/src/views/ProjectView/RightPanel/ChatTab.tsx.
// Any change to those effects must be reflected here.

class ChatTabAutoStartHarness {
  constructor() {
    this.projectId = null
    this.autoStarted = false
    this.wasRunning = false
    this.startCalls = []
  }

  // Mirrors: useEffect(() => { autoStarted.current = false }, [projectId])
  setProjectId(projectId) {
    if (this.projectId !== projectId) {
      this.autoStarted = false
      this.projectId = projectId
    }
  }

  // Mirrors: useEffect(() => {
  //   if (wasRunning.current && !isRunning) autoStarted.current = false
  //   wasRunning.current = !!isRunning
  // }, [isRunning])
  observeIsRunning(isRunning) {
    if (this.wasRunning && !isRunning) {
      this.autoStarted = false
    }
    this.wasRunning = !!isRunning
  }

  // Mirrors: useEffect(() => {
  //   if (agentStatus && !isRunning && !autoStarted.current) {
  //     autoStarted.current = true
  //     handleStart()
  //   }
  // }, [agentStatus])
  onStatusUpdate(agentStatus) {
    const isRunning = !!agentStatus?.isRunning
    this.observeIsRunning(isRunning)
    if (agentStatus && !isRunning && !this.autoStarted) {
      this.autoStarted = true
      this.startCalls.push({ projectId: this.projectId })
    }
  }
}

// --- Tests -----------------------------------------------------------------

test('autoStart fires exactly once on first idle status arrival', () => {
  const h = new ChatTabAutoStartHarness()
  h.setProjectId('proj-A')
  h.onStatusUpdate({ isRunning: false })
  assert.equal(h.startCalls.length, 1)
  assert.equal(h.startCalls[0].projectId, 'proj-A')
})

test('autoStart does NOT fire on undefined status (pre-fetch)', () => {
  const h = new ChatTabAutoStartHarness()
  h.setProjectId('proj-A')
  h.onStatusUpdate(undefined)
  assert.equal(h.startCalls.length, 0)
})

test('rapid status polls in same project produce at most one start', () => {
  const h = new ChatTabAutoStartHarness()
  h.setProjectId('proj-A')
  // Simulate 30 polls at 2s intervals — first one fires the start, rest
  // see autoStarted=true OR isRunning=true and short-circuit.
  for (let i = 0; i < 30; i++) {
    h.onStatusUpdate({ isRunning: i > 0 })
  }
  assert.equal(h.startCalls.length, 1)
})

test('project switch resets the guard so the new project gets exactly one start', () => {
  const h = new ChatTabAutoStartHarness()
  h.setProjectId('proj-A')
  h.onStatusUpdate({ isRunning: false })
  // proj-A's agent is now running.
  h.onStatusUpdate({ isRunning: true })

  // Navigate to proj-B. Component re-renders with new projectId.
  h.setProjectId('proj-B')
  // Status for proj-B has not been fetched yet — selector returns undefined.
  h.onStatusUpdate(undefined)
  // First poll for proj-B returns idle.
  h.onStatusUpdate({ isRunning: false })

  assert.equal(h.startCalls.length, 2)
  assert.equal(h.startCalls[0].projectId, 'proj-A')
  assert.equal(h.startCalls[1].projectId, 'proj-B')
})

test('agent stop in same project re-arms the guard for the next start', () => {
  const h = new ChatTabAutoStartHarness()
  h.setProjectId('proj-A')
  h.onStatusUpdate({ isRunning: false }) // start #1
  h.onStatusUpdate({ isRunning: true })  // running
  h.onStatusUpdate({ isRunning: false }) // wildfire phase ended → re-arm + start #2
  assert.equal(h.startCalls.length, 2)
})

test('rapid back-and-forth project switches do not duplicate starts within a project', () => {
  const h = new ChatTabAutoStartHarness()

  // Mount on A, agent starts and is running.
  h.setProjectId('proj-A')
  h.onStatusUpdate({ isRunning: false })
  h.onStatusUpdate({ isRunning: true })

  // Navigate to B (no agent yet) and back to A (agent still running).
  h.setProjectId('proj-B')
  h.onStatusUpdate(undefined)
  h.setProjectId('proj-A')
  // Status selector returns A's cached running status.
  h.onStatusUpdate({ isRunning: true })

  // No new start for A — it's still running.
  assert.equal(h.startCalls.length, 1)
  assert.equal(h.startCalls[0].projectId, 'proj-A')
})

// --- Structural assertion --------------------------------------------------

test('ChatTab is mounted via single conditional render in RightPanel', async () => {
  // The original bug let multiple panels stack because the renderer had
  // a per-event remount path. The new architecture renders the chat
  // surface via `{tab === 'chat' && <ChatTab projectId={projectId} />}`
  // — exactly one mount at a time. Lock that in by reading the file.
  const { readFile } = await import('node:fs/promises')
  const { fileURLToPath } = await import('node:url')
  const { dirname, resolve } = await import('node:path')
  const here = dirname(fileURLToPath(import.meta.url))
  const rightPanelPath = resolve(here, '..', 'RightPanel.tsx')
  const src = await readFile(rightPanelPath, 'utf8')
  // There must be exactly one ChatTab JSX mount-point and it must be
  // gated by an equality check on the active tab key.
  const mountMatches = src.match(/<ChatTab\s/g) || []
  assert.equal(mountMatches.length, 1, 'ChatTab should be mounted exactly once in RightPanel.tsx')
  assert.match(src, /tab\s*===\s*['"]chat['"]\s*&&\s*<ChatTab/)
})
