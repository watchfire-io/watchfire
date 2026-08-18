// Tests for the integrated-terminal shell command builder (issue #32).
//
// Run with: node --test gui/src/main/shell-command.test.mjs
//
// resolveShellCommand lives in shell-command.ts but is re-implemented here
// as JS so the test can run without a TypeScript / Electron toolchain (same
// convention as login-shell.test.mjs / version.test.mjs). Keep this file in
// sync with shell-command.ts — any change to the resolution precedence,
// per-platform fallbacks, or login-flag logic there must be mirrored below.
// The configured-shell validation block mirrors readConfiguredShell in
// pty-manager.ts the same way.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { chmodSync, mkdtempSync, mkdirSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { tmpdir, platform } from 'node:os'
import { join } from 'node:path'

// --- mirror of resolveShellCommand (shell-command.ts) ----------------------

function resolveShellCommand(configuredShell, envShell, plat) {
  if (plat === 'win32') {
    return { file: configuredShell || envShell || '/bin/zsh', args: [] }
  }
  const fallback = plat === 'darwin' ? '/bin/zsh' : '/bin/bash'
  return { file: configuredShell || envShell || fallback, args: ['-l'] }
}

// --- unit tests: command/args construction per platform --------------------

test('darwin: $SHELL spawned as a login shell', () => {
  assert.deepEqual(resolveShellCommand(null, '/bin/bash', 'darwin'), {
    file: '/bin/bash',
    args: ['-l']
  })
})

test('darwin: no $SHELL falls back to /bin/zsh, still login', () => {
  assert.deepEqual(resolveShellCommand(null, undefined, 'darwin'), {
    file: '/bin/zsh',
    args: ['-l']
  })
})

test('linux: no $SHELL falls back to /bin/bash, still login', () => {
  assert.deepEqual(resolveShellCommand(null, undefined, 'linux'), {
    file: '/bin/bash',
    args: ['-l']
  })
})

test('configured terminal_shell wins over $SHELL and keeps the login flag', () => {
  assert.deepEqual(resolveShellCommand('/opt/homebrew/bin/fish', '/bin/zsh', 'darwin'), {
    file: '/opt/homebrew/bin/fish',
    args: ['-l']
  })
})

test('empty-string $SHELL is treated as unset', () => {
  assert.deepEqual(resolveShellCommand(null, '', 'linux'), {
    file: '/bin/bash',
    args: ['-l']
  })
})

test('win32: behavior unchanged — no login flag, pre-#32 fallback chain', () => {
  assert.deepEqual(resolveShellCommand(null, undefined, 'win32'), {
    file: '/bin/zsh',
    args: []
  })
  assert.deepEqual(resolveShellCommand('C:\\shells\\pwsh.exe', undefined, 'win32'), {
    file: 'C:\\shells\\pwsh.exe',
    args: []
  })
})

// --- live evidence: login shell recovers native-terminal PATH --------------
//
// Spawn the resolved command with the minimal PATH a macOS GUI app inherits
// from launchd and compare login vs non-login PATH. The login shell must at
// least re-run the system profile chain: on macOS, path_helper (/etc/zprofile
// → /etc/paths) always adds /usr/local/bin, which the launchd seed lacks —
// exactly the class of entry (like pnpm's) issue #32 reported missing.

const LAUNCHD_PATH = '/usr/bin:/bin:/usr/sbin:/sbin'

function shellPath(file, args) {
  return execFileSync(file, [...args, '-c', 'printf %s "$PATH"'], {
    encoding: 'utf-8',
    timeout: 10000,
    env: {
      HOME: process.env.HOME ?? '',
      USER: process.env.USER ?? '',
      LOGNAME: process.env.LOGNAME ?? '',
      TERM: 'xterm-256color',
      PATH: LAUNCHD_PATH
    }
  })
}

test('login shell yields a native-terminal PATH from the launchd seed', { skip: platform() === 'win32' }, () => {
  const { file, args } = resolveShellCommand(null, process.env.SHELL, platform())
  assert.deepEqual(args, ['-l'])
  const loginPath = shellPath(file, args).split(':')
  assert.ok(loginPath.includes('/usr/bin'), `login PATH lost /usr/bin: ${loginPath}`)
  if (platform() === 'darwin') {
    // path_helper only runs for login shells; /usr/local/bin comes from
    // /etc/paths on every macOS install.
    assert.ok(
      loginPath.includes('/usr/local/bin'),
      `login shell did not run path_helper (/etc/paths): ${loginPath}`
    )
  }
})

// --- mirror of readConfiguredShell validation (pty-manager.ts) -------------

function validateShellPath(shell) {
  try {
    if (!shell) return null
    const info = statSync(shell)
    if (!info.isFile()) return null
    if ((info.mode & 0o111) === 0) return null
    return shell
  } catch {
    return null
  }
}

test('configured-shell validation: executable file accepted, everything else rejected', () => {
  const dir = mkdtempSync(join(tmpdir(), 'wf-shell-'))
  try {
    const exe = join(dir, 'goodsh')
    writeFileSync(exe, '#!/bin/sh\n')
    chmodSync(exe, 0o755)
    assert.equal(validateShellPath(exe), exe)

    const plain = join(dir, 'notexec')
    writeFileSync(plain, 'data')
    chmodSync(plain, 0o644)
    assert.equal(validateShellPath(plain), null)

    const sub = join(dir, 'subdir')
    mkdirSync(sub)
    assert.equal(validateShellPath(sub), null)

    assert.equal(validateShellPath(join(dir, 'missing')), null)
    assert.equal(validateShellPath(''), null)
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
})
