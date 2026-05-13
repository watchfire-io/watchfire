// Regression test for the #0100 "GUI Chat terminal jumps to start of
// session" bug.
//
// Run with: node --test gui/src/renderer/src/hooks/useAgentTerminal.test.mjs
//
// The original symptom: a transient `getAgentStatus` error briefly flipped
// `isRunning` to false in the agent store, which propagated through the
// `active` prop into the subscribe effect at useAgentTerminal.ts:90-137.
// The effect aborted the subscription, called `term.clear()`, and re-
// subscribed — and on re-subscribe the daemon replayed the full raw
// buffer from byte 0. Net effect: the viewport snapped to the first byte
// of the session whenever the user nudged the mouse wheel near a poll
// boundary.
//
// The fix has two parts. This test mirrors the client-side part (Fix A):
//
//   1. The subscribe effect is idempotent. When `active=true` and a live
//      (unaborted) subscription already exists, re-running the effect
//      must NOT abort, must NOT clear, must NOT issue a new subscribe.
//   2. The unsubscribe is debounced. `active=false` schedules a delayed
//      abort; if `active=true` returns before the timer fires, the abort
//      is cancelled and the subscription survives the flicker.
//   3. The bytesReceived cursor is threaded into subscribeRawOutput on
//      every fresh subscribe so the daemon-side slice (Fix B, proto
//      SubscribeRawOutputRequest.bytes_received) resumes catch-up at the
//      client's last byte.
//
// The hook also touches React + xterm internals; those are out of scope
// for this test. We exercise the state machine as plain functions and
// assert the call counts that the hook's effect would produce.

import { test } from 'node:test'
import assert from 'node:assert/strict'

// --- Logic mirror ---------------------------------------------------------
// Mirrors the subscribe-effect state machine in
// gui/src/renderer/src/hooks/useAgentTerminal.ts. Any change to that
// state machine must be reflected here.

class AgentTerminalHarness {
  constructor({ debounceMs = 3000, now = () => Date.now() } = {}) {
    this.debounceMs = debounceMs
    this.now = now
    this.projectId = null
    this.reconnectKey = 0
    this.abort = null // { aborted: boolean } | null
    this.unsubDelayDeadline = null
    this.bytesReceived = 0
    this.subscribeCalls = []
    this.abortCalls = 0
    this.clearCalls = 0 // legacy: term.clear() — must stay 0 (#0100 invariant)
    this.resetCalls = 0 // #0102: term.reset() — fires on generation change / project switch
    this.prevStartedAtKey = '' // #0101: tracks AgentStatus.startedAt for generation-change detection
    this.agentStoppedWrites = 0 // #0101: [Agent stopped] writes — must stay 0 (banner removed)
  }

  // Mirrors the effect body in useAgentTerminal.ts at the
  // "Manage the raw-output subscription" block.
  update({ projectId, active, reconnectKey, startedAtKey = '' }) {
    const projChanged = this.projectId !== projectId && this.projectId !== null
    const reconnectKeyChanged = this.reconnectKey !== reconnectKey
    this.projectId = projectId
    this.reconnectKey = reconnectKey

    // #0101 generation-change detection — mirrors the effect's
    // startedAtKey logic. The first non-empty observation is NOT a
    // transition; only flip when both sides are set and differ.
    const generationChanged =
      this.prevStartedAtKey !== '' &&
      startedAtKey !== '' &&
      startedAtKey !== this.prevStartedAtKey
    if (startedAtKey) this.prevStartedAtKey = startedAtKey

    if (projChanged) {
      this._hardAbort()
      this.resetCalls++ // term.reset() — fresh emulator for the new project
      this.bytesReceived = 0
      this.prevStartedAtKey = startedAtKey
    } else if (generationChanged) {
      // #0102: new daemon Process. Reset xterm AND cursor so the
      // daemon's full buffer replay lands on a fresh emulator state.
      // Without term.reset(), absolute cursor-positioning escapes
      // from the new agent's UI redraw collide with xterm's existing
      // bytes — that's the stacked-banner garbage symptom.
      this._hardAbort()
      this.resetCalls++
      this.bytesReceived = 0
    } else if (reconnectKeyChanged) {
      // Same Process, transient blip. Preserve cursor, do NOT reset
      // xterm — the user's view and scroll position survive intact.
      this._hardAbort()
    }

    if (active) {
      this.unsubDelayDeadline = null // cancel pending unsub
      if (this.abort && !this.abort.aborted) return // idempotent

      const cursor = this.bytesReceived
      this.subscribeCalls.push({ projectId, bytesReceived: cursor })
      this.abort = { aborted: false }
      return
    }

    // active=false: schedule delayed unsub
    if (!this.abort || this.abort.aborted) return
    if (this.unsubDelayDeadline !== null) return
    this.unsubDelayDeadline = this.now() + this.debounceMs
  }

  // Simulate time advancing — fires the pending unsubscribe if its
  // deadline has elapsed.
  tick(ms) {
    const target = this.now() + ms
    if (this.unsubDelayDeadline !== null && target >= this.unsubDelayDeadline) {
      this._hardAbort()
    }
    this.now = () => target
  }

  // Simulate a chunk of raw bytes arriving on the live channel. Mirrors
  // the onData callback's bytesReceivedRef increment.
  receive(byteLength) {
    if (!this.abort || this.abort.aborted) {
      throw new Error('cannot receive bytes without a live subscription')
    }
    this.bytesReceived += byteLength
  }

  // Simulate the gRPC stream closing (server returned nil, AbortError, or
  // a transport error). Mirrors the onEnd callback in the hook: do NOT
  // reset the cursor (#0102 — resetting it caused the daemon to replay
  // its full buffer and overlap with xterm's existing state), do NOT
  // write a visible marker, and signal a reconnect when the agent is
  // still running. The effect itself decides whether to reset xterm
  // and/or the cursor when it re-runs, based on whether startedAt has
  // changed in the store by then.
  endStream({ agentStillRunning } = { agentStillRunning: true }) {
    if (this.abort) this.abort.aborted = true
    // Cursor stays at its current value — same-Process catch-up.
    if (agentStillRunning) this.reconnectKey++
  }

  _hardAbort() {
    if (this.abort && !this.abort.aborted) {
      this.abort.aborted = true
      this.abortCalls++
    }
    this.unsubDelayDeadline = null
  }
}

// --- Tests ---------------------------------------------------------------

test('idempotent: re-running effect with active=true and a live subscription is a no-op', () => {
  const h = new AgentTerminalHarness()
  h.update({ projectId: 'p1', active: true, reconnectKey: 0 })
  assert.equal(h.subscribeCalls.length, 1)
  assert.equal(h.subscribeCalls[0].bytesReceived, 0)

  // Effect re-runs (e.g. React's StrictMode double-invoke, or some
  // other dep flagged as changed at the same time) — must NOT subscribe
  // again and must NOT abort the live one.
  h.update({ projectId: 'p1', active: true, reconnectKey: 0 })
  h.update({ projectId: 'p1', active: true, reconnectKey: 0 })
  assert.equal(h.subscribeCalls.length, 1, 'idempotent re-runs must not re-subscribe')
  assert.equal(h.abortCalls, 0, 'idempotent re-runs must not abort')
  assert.equal(h.clearCalls, 0, 'no term.clear() at any point')
})

test('flicker: active true → false → true within debounce window keeps the subscription', () => {
  const h = new AgentTerminalHarness({ debounceMs: 3000 })
  h.update({ projectId: 'p1', active: true, reconnectKey: 0 })
  assert.equal(h.subscribeCalls.length, 1)

  // Simulate the agent emitting some output before the flicker.
  h.receive(420)
  assert.equal(h.bytesReceived, 420)

  // Status-poll error flips isRunning briefly to false.
  h.update({ projectId: 'p1', active: false, reconnectKey: 0 })
  // 500 ms later, next poll succeeds and isRunning flips back to true.
  h.tick(500)
  h.update({ projectId: 'p1', active: true, reconnectKey: 0 })

  assert.equal(h.subscribeCalls.length, 1, 'flicker must NOT cause a new subscribe')
  assert.equal(h.abortCalls, 0, 'flicker must NOT abort the live subscription')
  assert.equal(h.bytesReceived, 420, 'cursor unchanged across flicker')
})

test('genuine stop: active false past the debounce window aborts the subscription', () => {
  const h = new AgentTerminalHarness({ debounceMs: 3000 })
  h.update({ projectId: 'p1', active: true, reconnectKey: 0 })
  h.receive(100)
  h.update({ projectId: 'p1', active: false, reconnectKey: 0 })
  h.tick(3500)
  assert.equal(h.abortCalls, 1, 'persistent active=false past debounce must abort')

  // Restart later: cursor stays where it was (bytesReceived = 100),
  // so the next subscribe resumes from that offset.
  h.update({ projectId: 'p1', active: true, reconnectKey: 0 })
  assert.equal(h.subscribeCalls.length, 2)
  assert.equal(h.subscribeCalls[1].bytesReceived, 100, 'restart resumes at cursor')
})

test('reconnectKey bump aborts and re-subscribes, preserving the cursor', () => {
  const h = new AgentTerminalHarness()
  h.update({ projectId: 'p1', active: true, reconnectKey: 0 })
  h.receive(900)

  // onEnd → reconnectTimer → setReconnectKey(k=>k+1) fires.
  h.update({ projectId: 'p1', active: true, reconnectKey: 1 })

  assert.equal(h.abortCalls, 1, 'reconnect must abort the prior subscription')
  assert.equal(h.subscribeCalls.length, 2, 'reconnect must issue a new subscribe')
  assert.equal(h.subscribeCalls[1].bytesReceived, 900, 'daemon resumes catch-up at cursor')
})

test('project switch aborts, resets the cursor, and subscribes fresh', () => {
  const h = new AgentTerminalHarness()
  h.update({ projectId: 'p1', active: true, reconnectKey: 0 })
  h.receive(750)

  // Navigate to a different project — same hook instance, new projectId.
  h.update({ projectId: 'p2', active: true, reconnectKey: 0 })

  assert.equal(h.abortCalls, 1)
  assert.equal(h.subscribeCalls.length, 2)
  assert.equal(h.subscribeCalls[1].projectId, 'p2')
  assert.equal(h.subscribeCalls[1].bytesReceived, 0, 'cursor must reset on project switch')
})

test('30 rapid status polls produce exactly one subscribe', () => {
  const h = new AgentTerminalHarness()
  h.update({ projectId: 'p1', active: true, reconnectKey: 0 })
  // Each poll re-runs the effect. Many of them will be identical
  // (active=true, same deps) — none should re-subscribe.
  for (let i = 0; i < 30; i++) {
    h.update({ projectId: 'p1', active: true, reconnectKey: 0 })
  }
  assert.equal(h.subscribeCalls.length, 1)
  assert.equal(h.abortCalls, 0)
})

test('term.clear() is never invoked by the subscribe state machine', () => {
  // The historical bug was the explicit `term.clear()` at the top of the
  // subscribe effect. Removing it is the load-bearing change; lock that
  // in by exercising every transition the state machine supports and
  // asserting the clear-call counter stays at zero.
  const h = new AgentTerminalHarness({ debounceMs: 100 })
  h.update({ projectId: 'p1', active: true, reconnectKey: 0 })
  h.receive(50)
  h.update({ projectId: 'p1', active: false, reconnectKey: 0 })
  h.tick(50)
  h.update({ projectId: 'p1', active: true, reconnectKey: 0 }) // cancel
  h.update({ projectId: 'p1', active: true, reconnectKey: 1 }) // reconnect
  h.update({ projectId: 'p2', active: true, reconnectKey: 0 }) // project switch
  h.update({ projectId: 'p2', active: false, reconnectKey: 0 })
  h.tick(200)
  assert.equal(h.clearCalls, 0)
})

// --- Structural assertions ------------------------------------------------
// Lock in the load-bearing source-level invariants — these are the
// things a future refactor could quietly regress.

test('useAgentTerminal does NOT call term.clear() anywhere', async () => {
  const { readFile } = await import('node:fs/promises')
  const { fileURLToPath } = await import('node:url')
  const { dirname, resolve } = await import('node:path')
  const here = dirname(fileURLToPath(import.meta.url))
  const hookPath = resolve(here, 'useAgentTerminal.ts')
  const src = await readFile(hookPath, 'utf8')
  // Strip line comments and block comments so we only inspect real
  // code — the file mentions term.clear() in a comment explaining why
  // it was removed, and that mention should NOT trigger the regression.
  const stripped = src.replace(/\/\/[^\n]*/g, '').replace(/\/\*[\s\S]*?\*\//g, '')
  assert.equal(
    /\bterm\.clear\(\)/.test(stripped),
    false,
    'term.clear() in the subscribe effect is the original #0100 bug — must stay removed'
  )
})

test('useAgentTerminal passes a 10 000-line scrollback to the Terminal constructor', async () => {
  const { readFile } = await import('node:fs/promises')
  const { fileURLToPath } = await import('node:url')
  const { dirname, resolve } = await import('node:path')
  const here = dirname(fileURLToPath(import.meta.url))
  const hookPath = resolve(here, 'useAgentTerminal.ts')
  const src = await readFile(hookPath, 'utf8')
  assert.match(src, /scrollback:\s*10000/, 'default 1000 is too low for agent sessions')
})

test('useAgentTerminal threads bytesReceived through subscribeRawOutput', async () => {
  const { readFile } = await import('node:fs/promises')
  const { fileURLToPath } = await import('node:url')
  const { dirname, resolve } = await import('node:path')
  const here = dirname(fileURLToPath(import.meta.url))
  const hookPath = resolve(here, 'useAgentTerminal.ts')
  const src = await readFile(hookPath, 'utf8')
  assert.match(src, /bytesReceivedRef\.current\s*\+=\s*data\.byteLength/)
  assert.match(src, /bytesReceivedRef\.current\s*\)/, 'cursor passed to subscribeRawOutput')
})

// --- #0101 regression tests -----------------------------------------------
// The v7.0.0 cursor work (#0100) eliminated the "snap to byte 0" symptom
// but introduced three new regressions when the daemon Process restarts
// out from under a live subscription: stale cursors skipped the new
// agent's initial prompt, the [Agent stopped] marker stamped the xterm
// once per stream-close cascade, and ChatTab's auto-restart cascade
// amplified the whole thing into line-stepping while typing. These
// tests lock in v7.1.0 Forge's fix.

test('#0101 generation change: new started_at resets cursor so new agent prompt isn’t skipped', () => {
  const h = new AgentTerminalHarness()
  // Session 1 — a chat agent has been running for a while.
  h.update({ projectId: 'p1', active: true, reconnectKey: 0, startedAtKey: 't1' })
  h.receive(50000)
  assert.equal(h.bytesReceived, 50000)

  // Click "Run All" — daemon kills the chat Process, spawns a task-mode
  // one. The new Process's rawTotalBytes starts at 0; if we reconnect
  // with the stale 50000 cursor, SubscribeRawFrom returns an empty
  // snapshot and the daemon-sent initial prompt ("Implement Task #0001…")
  // is dropped on the floor.
  h.update({ projectId: 'p1', active: true, reconnectKey: 0, startedAtKey: 't2' })

  assert.equal(h.abortCalls, 1, 'generation change aborts the stale subscription')
  assert.equal(h.subscribeCalls.length, 2, 'generation change forces a fresh subscribe')
  assert.equal(
    h.subscribeCalls[1].bytesReceived,
    0,
    'cursor must reset on generation change so daemon sends the full new-Process buffer'
  )
})

test('#0101 first started_at observation is NOT a generation change', () => {
  const h = new AgentTerminalHarness()
  // Initial mount: AgentStatus arrives with a startedAt. This is the
  // first time we've ever seen one — it's not a transition, the agent
  // didn't restart, so we must NOT reset the cursor or abort.
  h.update({ projectId: 'p1', active: true, reconnectKey: 0, startedAtKey: '' })
  h.receive(120)
  h.update({ projectId: 'p1', active: true, reconnectKey: 0, startedAtKey: 't1' })

  assert.equal(h.abortCalls, 0, 'first started_at observation must not abort')
  assert.equal(h.subscribeCalls.length, 1, 'first started_at observation must not re-subscribe')
  assert.equal(h.bytesReceived, 120, 'cursor preserved across first started_at observation')
})

test('#0101 stable started_at across many polls is a no-op', () => {
  const h = new AgentTerminalHarness()
  h.update({ projectId: 'p1', active: true, reconnectKey: 0, startedAtKey: 't1' })
  h.receive(500)
  for (let i = 0; i < 30; i++) {
    h.update({ projectId: 'p1', active: true, reconnectKey: 0, startedAtKey: 't1' })
  }
  assert.equal(h.subscribeCalls.length, 1, 'identical started_at must not re-subscribe')
  assert.equal(h.abortCalls, 0)
  assert.equal(h.bytesReceived, 500, 'cursor preserved')
})

test('#0102 onEnd preserves the cursor for same-Process reconnects (no full-buffer replay)', () => {
  const h = new AgentTerminalHarness()
  h.update({ projectId: 'p1', active: true, reconnectKey: 0, startedAtKey: 't1' })
  h.receive(8888)
  assert.equal(h.bytesReceived, 8888)

  // Stream closes (transient blip on the SAME Process — startedAt
  // unchanged). The earlier #0101 fix reset the cursor here, which
  // caused the daemon to replay its full buffer on top of xterm's
  // existing state — that produced the stacked-banner garbage. The
  // correct behaviour: leave the cursor alone, let the daemon do its
  // job and only send bytes past the cursor.
  h.endStream({ agentStillRunning: true })
  assert.equal(h.bytesReceived, 8888, 'onEnd must NOT zero the cursor on transient blip')

  // Effect re-runs because reconnectKey bumped (from endStream).
  // startedAt unchanged → reconnectKey branch → cursor preserved,
  // xterm NOT reset (user view intact).
  h.update({ projectId: 'p1', active: true, reconnectKey: h.reconnectKey, startedAtKey: 't1' })
  assert.equal(
    h.subscribeCalls[h.subscribeCalls.length - 1].bytesReceived,
    8888,
    'reconnect with same startedAt uses preserved cursor (incremental catch-up)'
  )
  assert.equal(h.resetCalls, 0, 'same-Process reconnect must NOT term.reset() — destroys scrollback')
})

test('#0102 wildfire phase transition (new started_at) resets xterm + cursor → no overlap', () => {
  const h = new AgentTerminalHarness()
  // Wildfire execute phase running.
  h.update({ projectId: 'p1', active: true, reconnectKey: 0, startedAtKey: 'execute' })
  h.receive(12345)

  // Phase ends — daemon's old Process exits, gRPC stream closes.
  h.endStream({ agentStillRunning: true })

  // Effect re-runs from the reconnect setTimeout. The onEnd's
  // fetchStatus has updated the store with the refine-phase Process's
  // new startedAt; the effect sees the generation change and resets.
  h.update({ projectId: 'p1', active: true, reconnectKey: h.reconnectKey, startedAtKey: 'refine' })

  assert.equal(h.resetCalls, 1, 'generation change must term.reset() — fresh emulator for new agent')
  assert.equal(h.subscribeCalls.length, 2, 'generation change forces a fresh subscribe')
  assert.equal(h.subscribeCalls[1].bytesReceived, 0, 'refine-phase prompt arrives via cursor=0 snapshot')
})

test('#0102 project switch resets xterm + cursor (full reset for new project)', () => {
  const h = new AgentTerminalHarness()
  h.update({ projectId: 'p1', active: true, reconnectKey: 0, startedAtKey: 't1' })
  h.receive(2000)
  assert.equal(h.resetCalls, 0)

  // Navigate to a different project.
  h.update({ projectId: 'p2', active: true, reconnectKey: 0, startedAtKey: 'q1' })

  assert.equal(h.resetCalls, 1, 'project switch must term.reset()')
  assert.equal(h.subscribeCalls[1].projectId, 'p2')
  assert.equal(h.subscribeCalls[1].bytesReceived, 0, 'cursor reset on project switch')
})

test('#0102 stable same-Process stream blip-and-recover preserves view + cursor', () => {
  const h = new AgentTerminalHarness()
  h.update({ projectId: 'p1', active: true, reconnectKey: 0, startedAtKey: 't1' })
  h.receive(420)

  // Five blip-and-recover cycles on the same Process — each ends the
  // stream (reconnectKey++) and immediately the effect re-runs with
  // same startedAt. The user's xterm should never reset.
  for (let i = 0; i < 5; i++) {
    h.endStream({ agentStillRunning: true })
    h.update({ projectId: 'p1', active: true, reconnectKey: h.reconnectKey, startedAtKey: 't1' })
    h.receive(80) // some more bytes arrive after each reconnect
  }

  assert.equal(h.resetCalls, 0, 'same-Process blip cycle must never term.reset()')
  assert.equal(h.bytesReceived, 420 + 5 * 80, 'cursor accumulates across blips')
  // Final subscribe uses the final cursor — daemon sends only new bytes.
  const last = h.subscribeCalls[h.subscribeCalls.length - 1]
  assert.ok(
    last.bytesReceived > 0,
    'final subscribe cursor must be > 0 (preserved across blips)'
  )
})

// --- Structural assertions (#0101) ----------------------------------------

test('#0101 [Agent stopped] marker is not written by the hook', async () => {
  const { readFile } = await import('node:fs/promises')
  const { fileURLToPath } = await import('node:url')
  const { dirname, resolve } = await import('node:path')
  const here = dirname(fileURLToPath(import.meta.url))
  const hookPath = resolve(here, 'useAgentTerminal.ts')
  const src = await readFile(hookPath, 'utf8')
  // Strip line + block comments so an explanatory mention in a comment
  // doesn't trigger the regression — only real code is inspected.
  const stripped = src.replace(/\/\/[^\n]*/g, '').replace(/\/\*[\s\S]*?\*\//g, '')
  assert.equal(
    /\bterm\.write\([^)]*\[Agent stopped\]/i.test(stripped),
    false,
    '[Agent stopped] marker write is the original #0101 line-stepping symptom — must stay removed'
  )
})

test('#0102 onEnd does NOT zero bytesReceivedRef (same-Process replay was the bug)', async () => {
  const { readFile } = await import('node:fs/promises')
  const { fileURLToPath } = await import('node:url')
  const { dirname, resolve } = await import('node:path')
  const here = dirname(fileURLToPath(import.meta.url))
  const hookPath = resolve(here, 'useAgentTerminal.ts')
  const src = await readFile(hookPath, 'utf8')
  // Reach into the onEnd callback by anchoring on fetchStatus(projectId)
  // (only present in onEnd) and the matching setReconnectKey just after
  // it. Anything between those two markers is the onEnd body — it must
  // NOT contain `bytesReceivedRef.current = 0`.
  const fetchIdx = src.indexOf('fetchStatus(projectId)')
  assert.ok(fetchIdx !== -1, 'onEnd must refresh status before scheduling reconnect')
  const setKeyIdx = src.indexOf('setReconnectKey(', fetchIdx)
  assert.ok(setKeyIdx !== -1, 'onEnd must schedule setReconnectKey after fetchStatus resolves')
  const onEndBody = src.slice(fetchIdx, setKeyIdx)
  assert.equal(
    /bytesReceivedRef\.current\s*=\s*0/.test(onEndBody),
    false,
    'onEnd resetting cursor caused the daemon to replay its buffer onto stale xterm state (#0102)'
  )
})

test('#0102 term.reset() is called on generation change AND project switch', async () => {
  const { readFile } = await import('node:fs/promises')
  const { fileURLToPath } = await import('node:url')
  const { dirname, resolve } = await import('node:path')
  const here = dirname(fileURLToPath(import.meta.url))
  const hookPath = resolve(here, 'useAgentTerminal.ts')
  const src = await readFile(hookPath, 'utf8')
  // Strip comments — the file mentions term.reset() in commentary and
  // we only want to count actual code-level calls.
  const stripped = src.replace(/\/\/[^\n]*/g, '').replace(/\/\*[\s\S]*?\*\//g, '')
  const matches = stripped.match(/\bterm\.reset\(\)/g) || []
  assert.ok(
    matches.length >= 2,
    `expected at least 2 term.reset() calls (projChanged + generationChanged), found ${matches.length}`
  )
})

test('#0102 reconnect delay is 200 ms (post-fetchStatus, localhost-tight)', async () => {
  const { readFile } = await import('node:fs/promises')
  const { fileURLToPath } = await import('node:url')
  const { dirname, resolve } = await import('node:path')
  const here = dirname(fileURLToPath(import.meta.url))
  const hookPath = resolve(here, 'useAgentTerminal.ts')
  const src = await readFile(hookPath, 'utf8')
  const callIdx = src.lastIndexOf('setReconnectKey(')
  assert.ok(callIdx !== -1)
  const tail = src.slice(callIdx)
  const m = tail.match(/\}\s*,\s*(\d+)\s*\)/)
  assert.ok(m, 'could not locate setTimeout delay literal')
  assert.equal(
    m[1],
    '200',
    'fetchStatus already pre-resolves the startedAt race; 200 ms is just a yield before re-subscribing'
  )
})
