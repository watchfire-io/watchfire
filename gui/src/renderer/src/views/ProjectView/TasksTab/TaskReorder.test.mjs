// Unit tests for the v7 drag-to-reorder helpers used by TasksTab/TaskGroup.
// Same structure as TaskModal.test.mjs: `node --test` runs without a TS
// toolchain, so we mirror the helper inline. The contracts here are narrow
// and stable — the drag-end handler in TaskGroup.tsx and the optimistic
// reducer in tasks-store.ts both flow through these shapes.

import { test } from 'node:test'
import assert from 'node:assert/strict'

// --- Helpers mirrored from production ---------------------------------------

// Mirrors @dnd-kit/sortable's `arrayMove`.
function arrayMove(arr, from, to) {
  const copy = arr.slice()
  const [item] = copy.splice(from, 1)
  copy.splice(to, 0, item)
  return copy
}

// Mirrors TaskGroup.handleDragEnd: given the dragged group's current order
// plus every task in the project, build the flat task-number list to send
// to TaskService.ReorderTasks. Dragged group first (in new order), then
// everything else preserving current relative order.
function buildReorderPayload(groupTasks, allTasks, activeId, overId) {
  if (!overId || activeId === overId) return null
  const ids = groupTasks.map((t) => String(t.taskNumber))
  const oldIndex = ids.indexOf(String(activeId))
  const newIndex = ids.indexOf(String(overId))
  if (oldIndex === -1 || newIndex === -1) return null
  const newGroupOrder = arrayMove(groupTasks, oldIndex, newIndex).map(
    (t) => t.taskNumber
  )
  const groupSet = new Set(newGroupOrder)
  const everythingElse = allTasks
    .filter((t) => !groupSet.has(t.taskNumber))
    .map((t) => t.taskNumber)
  return [...newGroupOrder, ...everythingElse]
}

// Mirrors tasks-store.reorderTasks's optimistic local reorder: tasks named
// in taskNumbers come first in that order; tasks not in the list keep their
// current relative position at the tail.
function optimisticReorder(previous, taskNumbers) {
  const byNumber = new Map(previous.map((t) => [t.taskNumber, t]))
  return [
    ...taskNumbers.map((n) => byNumber.get(n)).filter(Boolean),
    ...previous.filter((t) => !taskNumbers.includes(t.taskNumber))
  ]
}

// Mirrors the store's failure path: reverts to the snapshot taken before
// the optimistic update.
function revertOnError(previous) {
  return previous.slice()
}

// --- Test fixtures ----------------------------------------------------------

const ready = [
  { taskNumber: 10, status: 'ready', title: 'r-a' },
  { taskNumber: 11, status: 'ready', title: 'r-b' },
  { taskNumber: 12, status: 'ready', title: 'r-c' },
  { taskNumber: 13, status: 'ready', title: 'r-d' }
]
const draft = [
  { taskNumber: 20, status: 'draft', title: 'd-a' },
  { taskNumber: 21, status: 'draft', title: 'd-b' }
]
const done = [
  { taskNumber: 99, status: 'done', title: 'old', success: true }
]
const allActive = [...ready, ...draft]

// --- Happy-path drag --------------------------------------------------------

test('drag bottom ready task to the top → flat payload starts with new group order', () => {
  // Drag task 13 onto task 10 inside the "ready" group.
  const payload = buildReorderPayload(ready, allActive, '13', '10')
  assert.deepEqual(payload, [13, 10, 11, 12, 20, 21])
})

test('drag middle ready task one slot up → minimal swap reflected', () => {
  const payload = buildReorderPayload(ready, allActive, '12', '11')
  assert.deepEqual(payload, [10, 12, 11, 13, 20, 21])
})

test('drag within draft group → ready order untouched, draft reordered', () => {
  const payload = buildReorderPayload(draft, allActive, '21', '20')
  assert.deepEqual(payload, [21, 20, 10, 11, 12, 13])
})

// --- No-op guards -----------------------------------------------------------

test('drop on self (active === over) → null (caller early-returns)', () => {
  assert.equal(buildReorderPayload(ready, allActive, '11', '11'), null)
})

test('over is null (drag-end on vanished row mid-status-change) → null', () => {
  assert.equal(buildReorderPayload(ready, allActive, '11', null), null)
})

test('active id not in current group (cross-group drag) → null', () => {
  // Task 20 is a draft; if it surfaces as `active` while we're handling the
  // ready group's DndContext, the early-return prevents a bogus reorder.
  assert.equal(buildReorderPayload(ready, allActive, '20', '11'), null)
})

// --- Done tasks are excluded from sortable groups --------------------------

test('done tasks never appear in a sortable group payload', () => {
  // The ready group passes `allActive` (no done tasks), so the payload only
  // covers active tasks — done tasks are reordered by the server only via
  // their own status-group logic, not by this drag.
  const payload = buildReorderPayload(ready, allActive, '13', '10')
  for (const n of payload) {
    assert.notEqual(n, done[0].taskNumber)
  }
})

// --- Store: optimistic update -----------------------------------------------

test('optimistic reorder applies new order in-memory before the RPC resolves', () => {
  const optimistic = optimisticReorder(allActive, [13, 10, 11, 12, 20, 21])
  assert.deepEqual(
    optimistic.map((t) => t.taskNumber),
    [13, 10, 11, 12, 20, 21]
  )
})

test('optimistic reorder keeps tasks not named in the payload at the tail', () => {
  // Caller passed only the ready group's new order — the draft tasks should
  // still appear, in their original relative order, after the named tasks.
  const optimistic = optimisticReorder(allActive, [13, 10, 11, 12])
  assert.deepEqual(
    optimistic.map((t) => t.taskNumber),
    [13, 10, 11, 12, 20, 21]
  )
})

// --- Store: failure path → revert ------------------------------------------

test('revertOnError restores the exact pre-drag order from the snapshot', () => {
  const snapshot = allActive.slice()
  // Simulate the failed-RPC path: optimistic mutation, then revert.
  const reverted = revertOnError(snapshot)
  assert.deepEqual(
    reverted.map((t) => t.taskNumber),
    [10, 11, 12, 13, 20, 21]
  )
})

test('failure path: caller receives the rejection so a toast can fire', async () => {
  // Mirrors the store contract: on rpc rejection, the store re-throws so
  // the TaskGroup handler can call toast(...). We model that here with a
  // tiny harness that mimics the store's try/catch.
  const previous = allActive.slice()
  let state = optimisticReorder(previous, [13, 10, 11, 12, 20, 21])

  const rpc = () => Promise.reject(new Error('boom'))

  let toasted = null
  try {
    await rpc()
  } catch (err) {
    state = revertOnError(previous)
    toasted = String(err)
  }

  assert.deepEqual(
    state.map((t) => t.taskNumber),
    [10, 11, 12, 13, 20, 21]
  )
  assert.match(toasted, /boom/)
})
