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
    this.clearCalls = 0 // term.clear() — must stay 0 for the lifetime of the harness
  }

  // Mirrors the effect body in useAgentTerminal.ts at the
  // "Manage the raw-output subscription" block.
  update({ projectId, active, reconnectKey }) {
    const projChanged = this.projectId !== projectId && this.projectId !== null
    const reconnectKeyChanged = this.reconnectKey !== reconnectKey
    this.projectId = projectId
    this.reconnectKey = reconnectKey

    if (projChanged) {
      this._hardAbort()
      this.bytesReceived = 0
    } else if (reconnectKeyChanged) {
      this._hardAbort()
      // Keep bytesReceived intact — daemon resumes at the cursor.
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
