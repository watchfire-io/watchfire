import { existsSync, readFileSync } from 'fs'
import { join, resolve } from 'path'
import { homedir } from 'os'
import { execFile, execSync } from 'child_process'
import { parse } from 'yaml'
import { app } from 'electron'

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

  // Start daemon in background
  const child = execFile(daemonPath, [], {
    detached: true,
    stdio: 'ignore'
  })
  child.unref()

  // Wait for daemon to be ready
  for (let i = 0; i < 50; i++) {
    await sleep(100)
    const info = getDaemonInfo()
    if (info) return info
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

  // 5. /usr/local/bin fallback
  if (existsSync('/usr/local/bin/watchfired')) return '/usr/local/bin/watchfired'

  return null
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms))
}
