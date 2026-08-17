// Tests for the quick-add preview counter (v10 Torch). React isn't booted
// here (matches the needs-attention / insights-rollup test pattern); the pure
// helper from lib/quickadd.ts is mirrored and its contract asserted: count
// matches the daemon parser's block-splitting rule — top-level bullets, or
// one task for bullet-less non-empty input.

import { test } from 'node:test'
import assert from 'node:assert/strict'

// --- Mirror of lib/quickadd ------------------------------------------------

const BULLET_RE = /^(?:[-*]|\d+[.)])\s+\S/

function countQuickAddTasks(text) {
  const bullets = text
    .replace(/\r\n/g, '\n')
    .split('\n')
    .filter((line) => BULLET_RE.test(line)).length
  if (bullets > 0) return bullets
  return text.trim() === '' ? 0 : 1
}

// ---------------------------------------------------------------------------

test('counts dash, star and numbered top-level bullets', () => {
  assert.equal(countQuickAddTasks('- one\n- two\n- three'), 3)
  assert.equal(countQuickAddTasks('1. one\n2) two\n* three'), 3)
})

test('nested bullets do not add to the count', () => {
  assert.equal(countQuickAddTasks('- top\n  - nested\n  - nested two\n- second'), 2)
})

test('bullet-less non-empty input is one task', () => {
  assert.equal(countQuickAddTasks('just a plain prompt'), 1)
  assert.equal(countQuickAddTasks('two\n\nparagraphs'), 1)
})

test('empty and whitespace-only input is zero', () => {
  assert.equal(countQuickAddTasks(''), 0)
  assert.equal(countQuickAddTasks('   \n\n  '), 0)
})

test('preamble does not count when bullets exist', () => {
  assert.equal(countQuickAddTasks('my list:\n\n- one\n- two'), 2)
})

test('bare markers are not bullets', () => {
  assert.equal(countQuickAddTasks('-\n*'), 1) // falls back to single-task text
})

test('CRLF input counts the same as LF', () => {
  assert.equal(countQuickAddTasks('- one\r\n- two\r\n'), 2)
})
