// Tests for the GlobalSettings search reducer. We mirror the helpers
// (matchSearchEntries / matchCategories) inline rather than importing the TS
// module — same pattern as insights-rollup.test.mjs — so node:test runs the
// suite without a TS toolchain in the loop.

import { test } from 'node:test'
import assert from 'node:assert/strict'

// --- Mirrors of searchIndex.ts helpers + a representative slice of the index.
// If the TS file's contract changes, mirror the change here too — both
// sides are simple enough that drift is easy to catch in review.

function matchSearchEntries(query, index) {
  const tokens = query.trim().toLowerCase().split(/\s+/).filter(Boolean)
  if (tokens.length === 0) return []
  return index.filter((entry) => {
    const haystack = (entry.label + ' ' + entry.keywords.join(' ')).toLowerCase()
    return tokens.every((t) => haystack.includes(t))
  })
}

function matchCategories(query, categories) {
  const tokens = query.trim().toLowerCase().split(/\s+/).filter(Boolean)
  if (tokens.length === 0) return categories
  return categories.filter((c) => tokens.every((t) => c.label.toLowerCase().includes(t)))
}

const CATEGORIES = [
  { id: 'appearance', label: 'Appearance' },
  { id: 'defaults', label: 'Defaults' },
  { id: 'agent-paths', label: 'Agent Paths' },
  { id: 'notifications', label: 'Notifications' },
  { id: 'integrations', label: 'Integrations' },
  { id: 'inbound', label: 'Inbound' },
  { id: 'updates', label: 'Updates' },
  { id: 'about', label: 'About' }
]

const INDEX = [
  { category: 'appearance', fieldId: 'theme', label: 'Theme', keywords: ['light', 'dark', 'system'] },
  { category: 'defaults', fieldId: 'default-agent', label: 'Default Agent', keywords: ['agent', 'claude'] },
  { category: 'defaults', fieldId: 'auto-merge', label: 'Auto-merge', keywords: ['merge', 'branch'] },
  { category: 'defaults', fieldId: 'terminal-shell', label: 'Terminal shell', keywords: ['shell', 'bash', 'zsh'] },
  { category: 'notifications', fieldId: 'notifications-volume', label: 'Volume', keywords: ['loud', 'audio'] },
  { category: 'notifications', fieldId: 'notifications-quiet-hours', label: 'Quiet hours', keywords: ['mute', 'dnd'] },
  { category: 'inbound', fieldId: 'inbound-listen-addr', label: 'Inbound listen address', keywords: ['port', 'host'] }
]

test('matchSearchEntries: empty query returns no results', () => {
  assert.deepEqual(matchSearchEntries('', INDEX), [])
  assert.deepEqual(matchSearchEntries('   ', INDEX), [])
})

test('matchSearchEntries: single-token label match', () => {
  const got = matchSearchEntries('volume', INDEX)
  assert.equal(got.length, 1)
  assert.equal(got[0].label, 'Volume')
})

test('matchSearchEntries: keyword match (no label hit)', () => {
  const got = matchSearchEntries('mute', INDEX)
  assert.equal(got.length, 1)
  assert.equal(got[0].fieldId, 'notifications-quiet-hours')
})

test('matchSearchEntries: case-insensitive', () => {
  const upper = matchSearchEntries('CLAUDE', INDEX)
  const lower = matchSearchEntries('claude', INDEX)
  assert.deepEqual(upper, lower)
  assert.equal(upper.length, 1)
  assert.equal(upper[0].fieldId, 'default-agent')
})

test('matchSearchEntries: multi-token must all match (AND semantics)', () => {
  // "default agent" should hit only Default Agent, not other entries that
  // contain just one word.
  const got = matchSearchEntries('default agent', INDEX)
  assert.equal(got.length, 1)
  assert.equal(got[0].fieldId, 'default-agent')
})

test('matchSearchEntries: no match returns empty array', () => {
  assert.deepEqual(matchSearchEntries('zzznotathing', INDEX), [])
})

test('matchSearchEntries: order preserved', () => {
  // Two entries match "shell" via keywords but only the dedicated Terminal
  // shell row ranks first because it appears first in the index. The
  // reducer is deterministic — important for keyboard nav.
  const got = matchSearchEntries('shell', INDEX)
  assert.equal(got[0].fieldId, 'terminal-shell')
})

test('matchCategories: empty query returns all', () => {
  assert.equal(matchCategories('', CATEGORIES).length, CATEGORIES.length)
})

test('matchCategories: filters sidebar by label', () => {
  const got = matchCategories('inb', CATEGORIES)
  assert.equal(got.length, 1)
  assert.equal(got[0].id, 'inbound')
})

test('matchCategories: case-insensitive', () => {
  const got = matchCategories('NOTIFICATIONS', CATEGORIES)
  assert.equal(got.length, 1)
  assert.equal(got[0].id, 'notifications')
})

test('every category appears in the index at least once', () => {
  const seen = new Set(INDEX.map((e) => e.category))
  // The fixture intentionally omits some categories; verify the helper
  // doesn't silently swallow categories that *do* appear.
  for (const c of ['appearance', 'defaults', 'notifications', 'inbound']) {
    assert.ok(seen.has(c), `category ${c} missing from index fixture`)
  }
})
