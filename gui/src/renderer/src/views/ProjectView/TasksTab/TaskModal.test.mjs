// Unit tests for TaskModal's `initialDetailTab` helper. Mirrors the
// repo convention (settings-search.test.mjs, insights-rollup.test.mjs):
// `node --test` runs without a TS toolchain, so we mirror the helper
// inline. The contract is narrow — the implementation is a single
// ternary on `task.status` — so drift is easy to catch in review.

import { test } from 'node:test'
import assert from 'node:assert/strict'

function initialDetailTab(task) {
  return task?.status === 'done' ? 'inspect' : 'edit'
}

test("task.status === 'done' → returns 'inspect'", () => {
  assert.equal(initialDetailTab({ status: 'done' }), 'inspect')
})

test("task.status === 'done' + success: false (failed task) → returns 'inspect'", () => {
  // Failed-but-done tasks should also land on Inspect — that's where
  // the red "Task failed" banner lives.
  assert.equal(initialDetailTab({ status: 'done', success: false, failureReason: 'boom' }), 'inspect')
})

test("task.status === 'ready' → returns 'edit'", () => {
  assert.equal(initialDetailTab({ status: 'ready' }), 'edit')
})

test("task.status === 'draft' → returns 'edit'", () => {
  assert.equal(initialDetailTab({ status: 'draft' }), 'edit')
})

test('task === undefined (creation flow) → returns "edit"', () => {
  assert.equal(initialDetailTab(undefined), 'edit')
})
