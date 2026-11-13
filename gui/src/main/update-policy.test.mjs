// Regression tests for the GUI update policy (issue #61: updates were
// downloaded even with every update option switched off).
//
// Run with: node --test gui/src/main/update-policy.test.mjs
//
// The helpers live in update-policy.ts but are re-implemented here as JS so
// the test can run without a TypeScript/Electron toolchain (same convention
// as version.test.mjs / shell-command.test.mjs). Keep this file in sync with
// update-policy.ts — any change there must be mirrored below.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { parse } from 'yaml'

// --- mirror of update-policy.ts --------------------------------------------

const DEFAULT_UPDATE_POLICY = {
  checkOnStartup: true,
  autoDownload: true,
  checkFrequency: 'every_launch'
}

const DAY_MS = 24 * 60 * 60 * 1000

function parseUpdatePolicy(doc) {
  const policy = { ...DEFAULT_UPDATE_POLICY }
  const updates = doc?.updates
  if (!updates || typeof updates !== 'object') return policy
  if (typeof updates.check_on_startup === 'boolean') policy.checkOnStartup = updates.check_on_startup
  if (typeof updates.auto_download === 'boolean') policy.autoDownload = updates.auto_download
  const freq = updates.check_frequency
  if (freq === 'every_launch' || freq === 'daily' || freq === 'weekly') policy.checkFrequency = freq
  return policy
}

function shouldCheckOnStartup(policy, lastCheckedMs, nowMs) {
  if (!policy.checkOnStartup) return false
  if (lastCheckedMs === null || lastCheckedMs > nowMs) return true
  const since = nowMs - lastCheckedMs
  if (policy.checkFrequency === 'daily') return since >= DAY_MS
  if (policy.checkFrequency === 'weekly') return since >= 7 * DAY_MS
  return true
}

// --- tests ------------------------------------------------------------------

const NOW = Date.UTC(2026, 10, 13, 12, 0, 0)

test('issue #61: everything switched off means no startup check and no auto-download', () => {
  // The shape the daemon writes to ~/.watchfire/settings.yaml.
  const doc = parse(
    [
      'version: 1',
      'updates:',
      '  check_on_startup: false',
      '  check_frequency: every_launch',
      '  auto_download: false',
      '  last_checked: 2026-11-12T16:00:00Z',
      ''
    ].join('\n')
  )
  const policy = parseUpdatePolicy(doc)
  assert.equal(policy.checkOnStartup, false)
  assert.equal(policy.autoDownload, false)
  assert.equal(shouldCheckOnStartup(policy, null, NOW), false)
  assert.equal(shouldCheckOnStartup(policy, NOW - 30 * DAY_MS, NOW), false)
})

test('auto-download off still allows the startup check (which only notifies)', () => {
  const policy = parseUpdatePolicy({ updates: { check_on_startup: true, auto_download: false } })
  assert.equal(policy.autoDownload, false)
  assert.equal(shouldCheckOnStartup(policy, null, NOW), true)
})

test('missing file, empty document or missing section fall back to the daemon defaults', () => {
  for (const doc of [null, undefined, {}, { updates: null }, { defaults: {} }, 'garbage']) {
    assert.deepEqual(parseUpdatePolicy(doc), DEFAULT_UPDATE_POLICY)
  }
})

test('only real booleans and known frequencies override a default', () => {
  const policy = parseUpdatePolicy({
    updates: { check_on_startup: 'no', auto_download: 0, check_frequency: 'hourly' }
  })
  assert.deepEqual(policy, DEFAULT_UPDATE_POLICY)
})

test('every_launch always checks', () => {
  const policy = parseUpdatePolicy({ updates: { check_frequency: 'every_launch' } })
  assert.equal(shouldCheckOnStartup(policy, NOW - 1000, NOW), true)
})

test('daily and weekly skip a check inside their window', () => {
  const daily = parseUpdatePolicy({ updates: { check_frequency: 'daily' } })
  assert.equal(shouldCheckOnStartup(daily, null, NOW), true)
  assert.equal(shouldCheckOnStartup(daily, NOW - DAY_MS + 1, NOW), false)
  assert.equal(shouldCheckOnStartup(daily, NOW - DAY_MS, NOW), true)

  const weekly = parseUpdatePolicy({ updates: { check_frequency: 'weekly' } })
  assert.equal(shouldCheckOnStartup(weekly, NOW - 6 * DAY_MS, NOW), false)
  assert.equal(shouldCheckOnStartup(weekly, NOW - 7 * DAY_MS, NOW), true)
})

test('a last-checked stamp from the future (clock change) does not suppress checks forever', () => {
  const weekly = parseUpdatePolicy({ updates: { check_frequency: 'weekly' } })
  assert.equal(shouldCheckOnStartup(weekly, NOW + 365 * DAY_MS, NOW), true)
})
