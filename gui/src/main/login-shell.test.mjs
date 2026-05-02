// Regression tests for the GUI login-shell PATH resolver (issue #32).
//
// Run with: node --test gui/src/main/login-shell.test.mjs
//
// The helpers live in login-shell.ts but are re-implemented here as JS so
// the test can run without a TypeScript / Electron toolchain. Keep this file
// in sync with login-shell.ts — any change to the parsing, fallback list, or
// timeout semantics there must be mirrored below (and the matching test case
// added).

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { homedir } from 'node:os'
import { join } from 'node:path'

const RELEVANT_ENV_VARS = [
  'PATH',
  'NVM_DIR',
  'VOLTA_HOME',
  'PNPM_HOME',
  'BUN_INSTALL',
  'DENO_INSTALL',
  'CARGO_HOME',
  'GOPATH',
  'JAVA_HOME',
  'LANG',
  'LC_ALL'
]

function fallbackPaths() {
  const home = homedir()
  return [
    join(home, '.local', 'bin'),
    join(home, '.npm-global', 'bin'),
    '/opt/homebrew/bin',
    '/usr/local/bin',
    join(home, '.cargo', 'bin'),
    join(home, '.bun', 'bin')
  ]
}

function mergeFallbackPaths(currentPath, fallbacks = fallbackPaths()) {
  const sep = ':'
  const seen = new Set(currentPath ? currentPath.split(sep) : [])
  const additions = []
  for (const p of fallbacks) {
    if (!seen.has(p)) {
      additions.push(p)
      seen.add(p)
    }
  }
  if (additions.length === 0) return currentPath
  return currentPath ? `${additions.join(sep)}${sep}${currentPath}` : additions.join(sep)
}

function parseEnvOutput(out) {
  const env = {}
  for (const line of out.split('\n')) {
    const eq = line.indexOf('=')
    if (eq <= 0) continue
    env[line.slice(0, eq)] = line.slice(eq + 1)
  }
  return env
}

function selectRelevantEnv(parsed, baseEnv = {}) {
  const out = {}
  for (const k of RELEVANT_ENV_VARS) {
    if (parsed[k] !== undefined) out[k] = parsed[k]
  }
  out.PATH = mergeFallbackPaths(out.PATH || baseEnv.PATH || '')
  return out
}

// resolveLoginShellEnv with an injected runner — mirrors the production
// signature so the test exercises the real timeout / non-zero / parse paths.
async function resolveLoginShellEnv(options = {}) {
  const { runner, timeoutMs = 3000, baseEnv = {} } = options
  const shell = baseEnv.SHELL || '/bin/zsh'
  try {
    const stdout = await runner(shell, ['-l', '-c', 'env'], { timeout: timeoutMs })
    const parsed = parseEnvOutput(stdout)
    if (!parsed.PATH) throw new Error('login shell produced no PATH')
    return { ...baseEnv, ...selectRelevantEnv(parsed, baseEnv) }
  } catch {
    const merged = { ...baseEnv }
    merged.PATH = mergeFallbackPaths(merged.PATH || '')
    return merged
  }
}

test('parseEnvOutput: representative env output is parsed correctly', () => {
  const sample = [
    'SHELL=/bin/zsh',
    'PATH=/Users/me/.local/bin:/Users/me/.npm-global/bin:/usr/bin:/bin',
    'NVM_DIR=/Users/me/.nvm',
    'VOLTA_HOME=/Users/me/.volta',
    'PNPM_HOME=/Users/me/Library/pnpm',
    'BUN_INSTALL=/Users/me/.bun',
    'LANG=en_US.UTF-8',
    'JUNK_LINE_NO_EQUALS',
    '=should_be_dropped',
    'WITH_EQUALS_IN_VALUE=foo=bar=baz'
  ].join('\n')

  const parsed = parseEnvOutput(sample)
  assert.equal(parsed.SHELL, '/bin/zsh')
  assert.equal(parsed.PATH, '/Users/me/.local/bin:/Users/me/.npm-global/bin:/usr/bin:/bin')
  assert.equal(parsed.NVM_DIR, '/Users/me/.nvm')
  assert.equal(parsed.VOLTA_HOME, '/Users/me/.volta')
  assert.equal(parsed.PNPM_HOME, '/Users/me/Library/pnpm')
  assert.equal(parsed.BUN_INSTALL, '/Users/me/.bun')
  assert.equal(parsed.LANG, 'en_US.UTF-8')
  // `=value` lines (no key) and bare `KEY` lines (no =) are dropped.
  assert.equal(parsed.JUNK_LINE_NO_EQUALS, undefined)
  // Values containing `=` are preserved verbatim past the first `=`.
  assert.equal(parsed.WITH_EQUALS_IN_VALUE, 'foo=bar=baz')
})

test('selectRelevantEnv: only the allowlist is plucked + fallbacks merged into PATH', () => {
  const parsed = {
    PATH: '/Users/me/.local/bin:/usr/bin:/bin',
    NVM_DIR: '/Users/me/.nvm',
    VOLTA_HOME: '/Users/me/.volta',
    SHELL: '/bin/zsh', // not in the allowlist
    PWD: '/tmp', // not in the allowlist
    HOME: '/Users/me' // not in the allowlist
  }
  const out = selectRelevantEnv(parsed)

  // Allowlisted vars copied through.
  assert.equal(out.NVM_DIR, '/Users/me/.nvm')
  assert.equal(out.VOLTA_HOME, '/Users/me/.volta')
  // Non-allowlisted vars dropped.
  assert.equal(out.SHELL, undefined)
  assert.equal(out.PWD, undefined)
  assert.equal(out.HOME, undefined)
  // PATH starts with fallback dirs that weren't already on the user's PATH,
  // and the original entries are preserved at the tail.
  const pathEntries = out.PATH.split(':')
  assert.ok(pathEntries.includes('/Users/me/.local/bin'))
  assert.ok(pathEntries.includes('/opt/homebrew/bin'))
  assert.ok(pathEntries.includes('/usr/local/bin'))
  assert.ok(pathEntries.includes(join(homedir(), '.npm-global', 'bin')))
  assert.ok(pathEntries.includes('/usr/bin'))
})

test('mergeFallbackPaths: hardcoded fallbacks appear exactly once each (no dupes)', () => {
  // Start with a PATH that already contains some of the fallbacks — those
  // entries must not be duplicated, and the rest must be prepended.
  const home = homedir()
  const userPath = `${home}/.local/bin:/opt/homebrew/bin:/usr/bin`
  const merged = mergeFallbackPaths(userPath)
  const entries = merged.split(':')

  for (const fallback of fallbackPaths()) {
    const occurrences = entries.filter((e) => e === fallback).length
    assert.equal(occurrences, 1, `fallback ${fallback} should appear exactly once, saw ${occurrences}`)
  }
  // Original entries preserved.
  assert.ok(entries.includes('/usr/bin'))
})

test('mergeFallbackPaths: empty input yields exactly the fallback list', () => {
  const merged = mergeFallbackPaths('')
  const entries = merged.split(':')
  assert.deepEqual(entries, fallbackPaths())
})

test('resolveLoginShellEnv: happy path returns parsed login-shell env', async () => {
  const runner = async () =>
    [
      'PATH=/Users/me/.local/bin:/Users/me/Library/pnpm:/usr/bin',
      'NVM_DIR=/Users/me/.nvm',
      'PNPM_HOME=/Users/me/Library/pnpm',
      'LANG=en_US.UTF-8'
    ].join('\n')

  const env = await resolveLoginShellEnv({
    runner,
    baseEnv: { SHELL: '/bin/zsh', PATH: '/usr/bin:/bin' }
  })

  // Login-shell PATH replaces the minimal Electron PATH.
  const entries = env.PATH.split(':')
  assert.ok(entries.includes('/Users/me/.local/bin'))
  assert.ok(entries.includes('/Users/me/Library/pnpm'))
  // pnpm-related env preserved so `pnpm env use` flows work in the in-app
  // terminal exactly like in the user's native terminal.
  assert.equal(env.PNPM_HOME, '/Users/me/Library/pnpm')
  assert.equal(env.NVM_DIR, '/Users/me/.nvm')
  assert.equal(env.LANG, 'en_US.UTF-8')
})

test('resolveLoginShellEnv: timeout falls back to process.env + hardcoded fallback list', async () => {
  const runner = (_cmd, _args, opts) =>
    new Promise((_resolve, reject) => {
      // Simulate a hung login shell: never resolves, instead the test's own
      // timer rejects after the configured timeout. We match the production
      // contract (rejected promise on timeout).
      setTimeout(() => reject(new Error('timeout')), opts.timeout + 1)
    })

  const env = await resolveLoginShellEnv({
    runner,
    timeoutMs: 10,
    baseEnv: { SHELL: '/bin/zsh', PATH: '/usr/bin:/bin', HOME: homedir() }
  })

  // Process env preserved.
  assert.equal(env.SHELL, '/bin/zsh')
  // Fallback PATH dirs prepended.
  const entries = env.PATH.split(':')
  for (const fallback of fallbackPaths()) {
    assert.ok(entries.includes(fallback), `expected ${fallback} in PATH, got ${env.PATH}`)
  }
  // No fallback duplicated.
  for (const fallback of fallbackPaths()) {
    const count = entries.filter((e) => e === fallback).length
    assert.equal(count, 1, `${fallback} duplicated in fallback PATH`)
  }
  // Original PATH preserved at the tail.
  assert.ok(entries.includes('/usr/bin'))
  assert.ok(entries.includes('/bin'))
})

test('resolveLoginShellEnv: shell exits non-zero falls back', async () => {
  const runner = async () => {
    const err = new Error('shell exited with code 127')
    throw err
  }

  const env = await resolveLoginShellEnv({
    runner,
    baseEnv: { SHELL: '/bin/zsh', PATH: '/usr/bin' }
  })

  // Same fallback shape as the timeout case.
  const entries = env.PATH.split(':')
  for (const fallback of fallbackPaths()) {
    assert.ok(entries.includes(fallback))
  }
})

test('resolveLoginShellEnv: shell prints output but no PATH falls back', async () => {
  // Some weird rc files print only a banner / colour reset and no PATH —
  // we can't trust that as the user's real env, so we degrade to the
  // fallback list rather than letting the in-app terminal start with no
  // tooling visible.
  const runner = async () => 'NVM_DIR=/Users/me/.nvm\nLANG=C.UTF-8\n'

  const env = await resolveLoginShellEnv({
    runner,
    baseEnv: { SHELL: '/bin/zsh', PATH: '/usr/bin' }
  })

  const entries = env.PATH.split(':')
  for (const fallback of fallbackPaths()) {
    assert.ok(entries.includes(fallback))
  }
})
