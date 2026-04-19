// Regression tests for the CLI version parser / comparator (issue #30).
//
// Run with: node --test gui/src/main/version.test.mjs
//
// The helpers live in version.ts but are re-implemented here as JS so the test
// can run without a TypeScript/Electron toolchain. Keep this file in sync with
// version.ts — any change to the regexes or comparator there must be mirrored
// below (and the matching test case added).

import { test } from 'node:test'
import assert from 'node:assert/strict'

function stripAnsi(s) {
  return s
    .replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, '')
    .replace(/\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)/g, '')
    .replace(/\x1b[@-Z\\-_]/g, '')
}

function parseCLIVersion(rawOutput) {
  const clean = stripAnsi(rawOutput)
  const match = clean.match(/Watchfire\s+v?(\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?)/)
  return match ? match[1] : null
}

function parseSemver(v) {
  const m = v.trim().match(/^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$/)
  if (!m) return null
  return [Number(m[1]), Number(m[2]), Number(m[3]), m[4] ?? '']
}

function compareSemver(a, b) {
  const pa = parseSemver(a)
  const pb = parseSemver(b)
  if (!pa || !pb) return null
  for (let i = 0; i < 3; i++) {
    if (pa[i] !== pb[i]) return pa[i] < pb[i] ? -1 : 1
  }
  const prA = pa[3]
  const prB = pb[3]
  if (prA === prB) return 0
  if (prA === '') return 1
  if (prB === '') return -1
  return prA < prB ? -1 : 1
}

test('parseCLIVersion: plain non-TTY output (macOS/Linux, no colors)', () => {
  const out = '  Watchfire 2.0.1 (Spark)\n    Commit   abc1234\n'
  assert.equal(parseCLIVersion(out), '2.0.1')
})

test('parseCLIVersion: lipgloss ANSI CSI color codes', () => {
  const out = '\x1b[1;96mWatchfire\x1b[0m \x1b[92m2.0.1\x1b[0m \x1b[90m(Spark)\x1b[0m\n'
  assert.equal(parseCLIVersion(out), '2.0.1')
})

test('parseCLIVersion: OSC 8 hyperlinks around the brand token', () => {
  const out = '\x1b]8;;https://watchfire.io\x07Watchfire\x1b]8;;\x07 2.0.1 (Spark)\n'
  assert.equal(parseCLIVersion(out), '2.0.1')
})

test('parseCLIVersion: dev build returns null so no prompt is shown', () => {
  assert.equal(parseCLIVersion('  Watchfire dev (unknown)\n'), null)
})

test('parseCLIVersion: pre-release tag is captured', () => {
  assert.equal(parseCLIVersion('  Watchfire v2.0.1-rc.1 (Spark)\n'), '2.0.1-rc.1')
})

test('parseCLIVersion: CRLF line endings are tolerated', () => {
  assert.equal(parseCLIVersion('  Watchfire 2.0.1 (Spark)\r\n'), '2.0.1')
})

test('parseCLIVersion: trailing update-banner does not confuse match', () => {
  const out = '  Watchfire 2.0.0 (Spark)\n⚡ Update available: v2.0.1 — run ...\n'
  assert.equal(parseCLIVersion(out), '2.0.0')
})

test('compareSemver: equal versions', () => {
  assert.equal(compareSemver('2.0.1', '2.0.1'), 0)
})

test('compareSemver: older installed < newer app', () => {
  assert.equal(compareSemver('2.0.0', '2.0.1'), -1)
})

test('compareSemver: newer installed > older app', () => {
  assert.equal(compareSemver('2.0.1', '2.0.0'), 1)
})

test('compareSemver: whitespace is trimmed', () => {
  assert.equal(compareSemver(' 2.0.1 ', '2.0.1'), 0)
})

test('compareSemver: leading v is ignored', () => {
  assert.equal(compareSemver('v2.0.1', '2.0.1'), 0)
})

test('compareSemver: build metadata (+suffix) is ignored', () => {
  assert.equal(compareSemver('2.0.1+build.7', '2.0.1'), 0)
})

test('compareSemver: pre-release sorts below release (SemVer 2.0.0 §11)', () => {
  assert.equal(compareSemver('2.0.1-rc.1', '2.0.1'), -1)
  assert.equal(compareSemver('2.0.1', '2.0.1-rc.1'), 1)
})

test('compareSemver: non-semver on either side returns null', () => {
  assert.equal(compareSemver('dev', '2.0.1'), null)
  assert.equal(compareSemver('2.0.1', 'dev'), null)
  assert.equal(compareSemver('2.0', '2.0.1'), null)
})
