import { BrowserWindow } from 'electron'
import { randomUUID } from 'crypto'
import { createRequire } from 'module'
import { existsSync, readFileSync, statSync } from 'fs'
import { homedir } from 'os'
import { join } from 'path'
import { parse } from 'yaml'
import { loginShellEnv } from './login-shell'

// Use createRequire to load native module at runtime — bypasses Rollup bundling
const _require = createRequire(import.meta.url || __filename)
const pty = _require('node-pty') as typeof import('node-pty')

interface PtySession {
  pty: import('node-pty').IPty
  id: string
}

let sessions: Map<string, PtySession> = new Map()
let win: BrowserWindow | null = null

export function setWindow(w: BrowserWindow): void {
  win = w
}

function safeSend(channel: string, data: unknown): void {
  if (win && !win.isDestroyed() && !win.webContents.isDestroyed()) {
    win.webContents.send(channel, data)
  }
}

// Read defaults.terminal_shell from ~/.watchfire/settings.yaml. Returns the
// configured path iff it points at an executable; otherwise null. We re-read
// the file on each PTY spawn (not cached) so a settings change takes effect
// on the next new tab without an app relaunch — the file is tiny and the
// stat/read cost is dwarfed by the PTY spawn itself.
function readConfiguredShell(): string | null {
  try {
    const path = join(homedir(), '.watchfire', 'settings.yaml')
    if (!existsSync(path)) return null
    const raw = readFileSync(path, 'utf-8')
    const parsed = parse(raw) as { defaults?: { terminal_shell?: string } } | null
    const shell = parsed?.defaults?.terminal_shell?.trim()
    if (!shell) return null
    const info = statSync(shell)
    if (!info.isFile()) return null
    if ((info.mode & 0o111) === 0) return null
    return shell
  } catch {
    return null
  }
}

export async function createPty(cwd: string): Promise<string> {
  if (!win) throw new Error('No window set')

  const id = randomUUID()
  const env = await loginShellEnv()
  const shell = readConfiguredShell() || process.env.SHELL || '/bin/zsh'

  const p = pty.spawn(shell, [], {
    name: 'xterm-256color',
    cols: 80,
    rows: 24,
    cwd,
    env: env as Record<string, string>
  })

  p.onData((data) => {
    safeSend('pty-output', { id, data })
  })

  p.onExit(({ exitCode }) => {
    safeSend('pty-exit', { id, exitCode })
    sessions.delete(id)
  })

  sessions.set(id, { pty: p, id })
  return id
}

export function writePty(id: string, data: string): void {
  sessions.get(id)?.pty.write(data)
}

export function resizePty(id: string, cols: number, rows: number): void {
  try {
    sessions.get(id)?.pty.resize(cols, rows)
  } catch {
    // ignore resize errors on dead PTY
  }
}

export function destroyPty(id: string): void {
  const session = sessions.get(id)
  if (session) {
    session.pty.kill()
    sessions.delete(id)
  }
}

export function destroyAll(): void {
  for (const [, session] of sessions) {
    session.pty.kill()
  }
  sessions.clear()
}
