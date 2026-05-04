// Unit tests for TaskStatusBadge's tooltip helper. Following the
// repo-wide convention (settings-search.test.mjs, insights-rollup.test.mjs):
// `node --test` has no TS toolchain in the loop, so we mirror the helper
// logic in plain JS and pin its semantics here. If the .tsx helper drifts,
// review will catch it — the file is small and the contract is narrow.

import { test } from 'node:test'
import assert from 'node:assert/strict'

const TOOLTIP_MAX_RUNES = 500

function truncate(s, max) {
  const runes = Array.from(s)
  if (runes.length <= max) return s
  return runes.slice(0, max - 1).join('') + '…'
}

function computeBadgeTooltip(opts) {
  const merge = (opts.mergeFailureReason ?? '').trim()
  const agent = (opts.failureReason ?? '').trim()
  if (opts.isMergeFailed && merge !== '') {
    return `Merge failed: ${truncate(merge, TOOLTIP_MAX_RUNES - 'Merge failed: '.length)}`
  }
  if (opts.isAgentFailed && agent !== '') {
    return `Failed: ${truncate(agent, TOOLTIP_MAX_RUNES - 'Failed: '.length)}`
  }
  return undefined
}

test('agent-failed task with non-empty failureReason → tooltip starts with "Failed: " and contains the reason', () => {
  const tip = computeBadgeTooltip({
    isAgentFailed: true,
    isMergeFailed: false,
    failureReason: 'something exploded'
  })
  assert.equal(typeof tip, 'string')
  assert.ok(tip.startsWith('Failed: '), `expected "Failed: " prefix, got ${JSON.stringify(tip)}`)
  assert.ok(tip.includes('something exploded'), `expected reason in tooltip, got ${JSON.stringify(tip)}`)
})

test('agent-failed task with empty failureReason → tooltip is undefined', () => {
  const tip = computeBadgeTooltip({
    isAgentFailed: true,
    isMergeFailed: false,
    failureReason: ''
  })
  assert.equal(tip, undefined)
})

test('agent-failed task with whitespace-only failureReason → tooltip is undefined', () => {
  const tip = computeBadgeTooltip({
    isAgentFailed: true,
    isMergeFailed: false,
    failureReason: '   \n  '
  })
  assert.equal(tip, undefined)
})

test('merge-failed task with both reasons set → tooltip starts with "Merge failed: " (merge wins)', () => {
  const tip = computeBadgeTooltip({
    isAgentFailed: false,
    isMergeFailed: true,
    failureReason: 'agent reason',
    mergeFailureReason: 'conflict on file.go'
  })
  assert.equal(typeof tip, 'string')
  assert.ok(tip.startsWith('Merge failed: '), `expected "Merge failed: " prefix, got ${JSON.stringify(tip)}`)
  assert.ok(tip.includes('conflict on file.go'))
  assert.ok(!tip.includes('agent reason'), 'merge tooltip should not leak agent reason')
})

test('input longer than 500 runes → tooltip is exactly 500 runes (truncated with trailing "…")', () => {
  const longReason = 'x'.repeat(1000)
  const tip = computeBadgeTooltip({
    isAgentFailed: true,
    isMergeFailed: false,
    failureReason: longReason
  })
  assert.equal(typeof tip, 'string')
  const runeCount = Array.from(tip).length
  assert.equal(runeCount, TOOLTIP_MAX_RUNES, `expected tooltip rune length ${TOOLTIP_MAX_RUNES}, got ${runeCount}`)
  assert.ok(tip.endsWith('…'), 'expected trailing ellipsis on truncated tooltip')
  assert.ok(tip.startsWith('Failed: '))
})

test('non-failed task → tooltip is undefined', () => {
  const tip = computeBadgeTooltip({
    isAgentFailed: false,
    isMergeFailed: false,
    failureReason: 'should be ignored',
    mergeFailureReason: 'should also be ignored'
  })
  assert.equal(tip, undefined)
})
