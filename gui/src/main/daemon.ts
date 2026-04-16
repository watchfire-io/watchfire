import { existsSync, readFileSync } from 'fs'
import { join, resolve } from 'path'
import { homedir, platform } from 'os'
import { spawn, execSync } from 'child_process'
import { createConnection } from 'net'
import { parse } from 'yaml'
import { app } from 'electron'

/**
 * Resolve the user's login shell PATH.
 * macOS GUI apps inherit a minimal environment (PATH=/usr/bin:/bin:/usr/sbin:/sbin)
 * which is missing user-installed tool paths like ~/.local/bin. This causes
 * spawned agents (e.g. Claude Code) to run in a degraded environment that can
 * break authentication and billing routing.
 */
function resolveShellEnv(): Record<string, string> {
  const env: Record<string, string> = { ...process.env }
  if (platform() !== 'darwin' && platform() !== 'linux') return env

  try {
    const shell = process.env.SHELL || '/bin/zsh'
    // Run a login shell to get the full PATH
    const fullPath = execSync(`${shell} -l -c 'echo $PATH'`, {
      encoding: 'utf-8',
      timeout: 5000
    }).trim()
    if (fullPath) {
      env.PATH = fullPath
    }
  } catch {
    // Fallback: manually prepend common user paths
    const home = homedir()
    const userPaths = [
      join(home, '.local', 'bin'),
      '/opt/homebrew/bin',
      '/usr/local/bin'
    ]
    const currentPath = process.env.PATH || ''
    const missing = userPaths.filter((p) => !currentPath.includes(p))
    if (missing.length > 0) {
      env.PATH = [...missing, currentPath].join(':')
    }
  }
  return env
}

export interface DaemonInfo {
  host: string
  port: number
  pid: number
  started_at: string
}

const DAEMON_YAML = join(homedir(), '.watchfire', 'daemon.yaml')

/** Read daemon.yaml to get connection info */
export function getDaemonInfo(): DaemonInfo | null {
  try {
    if (!existsSync(DAEMON_YAML)) return null
    const raw = readFileSync(DAEMON_YAML, 'utf-8')
    const info = parse(raw) as DaemonInfo
    if (!info.port) return null

    // Verify the process is actually running
    try {
      process.kill(info.pid, 0)
      return info
    } catch {
      return null
    }
  } catch {
    return null
  }
}

/** Ensure daemon is running, start it if not */
export async function ensureDaemon(): Promise<DaemonInfo> {
  const existing = getDaemonInfo()
  if (existing) return existing

  // Try to find and start the daemon binary
  const daemonPath = findDaemonBinary()
  if (!daemonPath) {
    throw new Error('watchfired binary not found')
  }

  // Start daemon in background with the user's full shell environment.
  // Without this, macOS GUI apps pass a minimal env that breaks agent auth.
  const child = spawn(daemonPath, [], {
    detached: true,
    stdio: 'ignore',
    env: resolveShellEnv()
  })
  child.unref()

  // Wait for daemon to be ready (daemon.yaml + port accepting connections)
  for (let i = 0; i < 50; i++) {
    await sleep(100)
    const info = getDaemonInfo()
    if (info) {
      // Verify the port is actually accepting connections
      const ready = await waitForPort(info.port, 2000)
      if (ready) return info
    }
  }

  throw new Error('Daemon failed to start within timeout')
}

function findDaemonBinary(): string | null {
  // 1. Try PATH
  try {
    const p = execSync('which watchfired', { encoding: 'utf-8' }).trim()
    if (p && existsSync(p)) return p
  } catch { /* not in PATH */ }

  // 2. Try relative to this executable (bundled in app or dev mode)
  const exePath = app.isPackaged
    ? join(process.resourcesPath, 'watchfired')
    : resolve(app.getAppPath(), '..', '..', 'build', 'watchfired')
  if (existsSync(exePath)) return exePath

  // 3. Try ./build/watchfired from cwd
  const buildPath = join(process.cwd(), 'build', 'watchfired')
  if (existsSync(buildPath)) return buildPath

  // 4. Try ../build/watchfired (if cwd is gui/)
  const parentBuild = resolve(process.cwd(), '..', 'build', 'watchfired')
  if (existsSync(parentBuild)) return parentBuild

  // 5. /opt/homebrew/bin fallback (Apple Silicon Homebrew)
  if (existsSync('/opt/homebrew/bin/watchfired')) return '/opt/homebrew/bin/watchfired'

  // 6. /usr/local/bin fallback
  if (existsSync('/usr/local/bin/watchfired')) return '/usr/local/bin/watchfired'

  return null
}

/** Stop the running daemon by sending SIGTERM and waiting for it to exit */
export async function stopDaemon(): Promise<void> {
  const info = getDaemonInfo()
  if (!info) return

  try {
    process.kill(info.pid, 'SIGTERM')
  } catch {
    return // process already gone
  }

  // Poll until daemon is gone (max 5s)
  for (let i = 0; i < 50; i++) {
    await sleep(100)
    if (!getDaemonInfo()) return
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms))
}

/** Check if a port is accepting TCP connections, with a timeout. */
function waitForPort(port: number, timeoutMs: number): Promise<boolean> {
  return new Promise((resolve) => {
    const deadline = Date.now() + timeoutMs

    function attempt(): void {
      if (Date.now() >= deadline) {
        resolve(false)
        return
      }
      const socket = createConnection({ host: 'localhost', port }, () => {
        socket.destroy()
        resolve(true)
      })
      socket.on('error', () => {
        socket.destroy()
        setTimeout(attempt, 50)
      })
      socket.setTimeout(100, () => {
        socket.destroy()
        setTimeout(attempt, 50)
      })
    }

    attempt()
  })
}
