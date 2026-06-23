// Tests for the v8 Inferno mission-control needs-attention aggregation.
//
// React isn't booted here (matches the insights-rollup / useExportReport test
// pattern); instead we mirror the pure helpers from lib/needs-attention.ts and
// assert their contract: agent issues come first, then failed + merge-failed
// tasks, deleted/healthy tasks are ignored, and click-through targets are right.

import { test } from 'node:test'
import assert from 'node:assert/strict'

// --- Mirror of lib/needs-attention ---------------------------------------

function failedTasks(tasks) {
  if (!tasks) return []
  return tasks.filter(
    (t) =>
      t.status === 'done' &&
      !t.deletedAt &&
      (t.success === false || (t.mergeFailureReason ?? '') !== '')
  )
}

function issueKindLabel(issueType) {
  switch (issueType) {
    case 'auth_required':
      return { kind: 'auth_required', label: 'Auth required' }
    case 'rate_limited':
      return { kind: 'rate_limited', label: 'Rate limited' }
    default:
      return { kind: 'agent_issue', label: 'Agent issue' }
  }
}

function buildAttentionEntries(projects, tasksByProjectId, issuesByProjectId) {
  const issueEntries = []
  const taskEntries = []

  for (const project of projects) {
    const { projectId, name } = project
    const projectName = name || projectId

    const issue = issuesByProjectId[projectId]
    if (issue && issue.issueType !== '') {
      const { kind, label } = issueKindLabel(issue.issueType)
      issueEntries.push({
        id: `issue:${projectId}`,
        projectId,
        projectName,
        kind,
        label,
        detail: issue.message || 'Agent is paused',
        target: 'main'
      })
    }

    for (const task of failedTasks(tasksByProjectId[projectId])) {
      const isMergeFailure = task.success !== false && (task.mergeFailureReason ?? '') !== ''
      taskEntries.push({
        id: `task:${projectId}:${task.taskNumber}`,
        projectId,
        projectName,
        kind: isMergeFailure ? 'merge_failed' : 'task_failed',
        label: isMergeFailure ? 'Merge failed' : 'Task failed',
        detail: task.title || `Task #${task.taskNumber}`,
        taskNumber: task.taskNumber,
        target: 'tasks'
      })
    }
  }

  return [...issueEntries, ...taskEntries]
}

// --- failedTasks ----------------------------------------------------------

test('failedTasks: picks agent failures and merge failures, ignores rest', () => {
  const tasks = [
    { taskNumber: 1, status: 'done', success: false },
    { taskNumber: 2, status: 'done', success: true, mergeFailureReason: 'conflict' },
    { taskNumber: 3, status: 'done', success: true },
    { taskNumber: 4, status: 'ready', success: false },
    { taskNumber: 5, status: 'done', success: false, deletedAt: { seconds: 1n } }
  ]
  const got = failedTasks(tasks).map((t) => t.taskNumber)
  assert.deepEqual(got, [1, 2])
})

test('failedTasks: undefined / empty is empty', () => {
  assert.deepEqual(failedTasks(undefined), [])
  assert.deepEqual(failedTasks([]), [])
})

// --- buildAttentionEntries ------------------------------------------------

test('buildAttentionEntries: issues first, then tasks; right targets', () => {
  const projects = [
    { projectId: 'a', name: 'Alpha' },
    { projectId: 'b', name: 'Beta' }
  ]
  const tasks = {
    a: [{ taskNumber: 7, status: 'done', success: false, title: 'broke build' }],
    b: [{ taskNumber: 9, status: 'done', success: true, mergeFailureReason: 'conflict', title: 'add x' }]
  }
  const issues = {
    b: { issueType: 'rate_limited', message: 'reset in 5m' }
  }

  const entries = buildAttentionEntries(projects, tasks, issues)
  assert.equal(entries.length, 3)

  // Issue entries lead.
  assert.equal(entries[0].kind, 'rate_limited')
  assert.equal(entries[0].label, 'Rate limited')
  assert.equal(entries[0].projectName, 'Beta')
  assert.equal(entries[0].target, 'main')
  assert.equal(entries[0].taskNumber, undefined)

  // Then task-derived, in project order.
  assert.equal(entries[1].kind, 'task_failed')
  assert.equal(entries[1].label, 'Task failed')
  assert.equal(entries[1].taskNumber, 7)
  assert.equal(entries[1].target, 'tasks')
  assert.equal(entries[1].detail, 'broke build')

  assert.equal(entries[2].kind, 'merge_failed')
  assert.equal(entries[2].label, 'Merge failed')
  assert.equal(entries[2].taskNumber, 9)
})

test('buildAttentionEntries: empty issueType is ignored; name falls back to id', () => {
  const projects = [{ projectId: 'a', name: '' }]
  const issues = { a: { issueType: '', message: '' } }
  const entries = buildAttentionEntries(projects, {}, issues)
  assert.deepEqual(entries, [])

  const projects2 = [{ projectId: 'xyz', name: '' }]
  const issues2 = { xyz: { issueType: 'auth_required', message: '' } }
  const e2 = buildAttentionEntries(projects2, {}, issues2)
  assert.equal(e2[0].projectName, 'xyz')
  assert.equal(e2[0].label, 'Auth required')
  assert.equal(e2[0].detail, 'Agent is paused')
})

test('buildAttentionEntries: nothing wrong → empty (clean state)', () => {
  const projects = [{ projectId: 'a', name: 'Alpha' }]
  const tasks = { a: [{ taskNumber: 1, status: 'done', success: true }] }
  assert.deepEqual(buildAttentionEntries(projects, tasks, {}), [])
})
