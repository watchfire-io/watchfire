import { BrowserWindow } from 'electron'
import { randomUUID } from 'crypto'
import { createRequire } from 'module'

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

export function createPty(cwd: string): string {
  if (!win) throw new Error('No window set')

  const id = randomUUID()
  const shell = process.env.SHELL || '/bin/zsh'

  const p = pty.spawn(shell, [], {
    name: 'xterm-256color',
    cols: 80,
    rows: 24,
    cwd,
    env: { ...process.env } as Record<string, string>
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
