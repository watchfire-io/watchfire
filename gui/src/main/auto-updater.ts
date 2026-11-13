import { autoUpdater } from 'electron-updater'
import { app, ipcMain } from 'electron'
import { existsSync, readFileSync, writeFileSync } from 'fs'
import { homedir } from 'os'
import { join } from 'path'
import { parse } from 'yaml'
import { broadcast } from './windows'
import {
  DEFAULT_UPDATE_POLICY,
  parseUpdatePolicy,
  shouldCheckOnStartup,
  type UpdatePolicy
} from './update-policy'

// Read the `updates:` section of ~/.watchfire/settings.yaml. Re-read on every
// check (not cached) so flipping a toggle in Settings applies to the very
// next check without an app relaunch. Any read/parse failure falls back to
// the daemon's defaults.
function readUpdatePolicy(): UpdatePolicy {
  try {
    const path = join(homedir(), '.watchfire', 'settings.yaml')
    if (!existsSync(path)) return { ...DEFAULT_UPDATE_POLICY }
    return parseUpdatePolicy(parse(readFileSync(path, 'utf-8')))
  } catch {
    return { ...DEFAULT_UPDATE_POLICY }
  }
}

// The GUI keeps its own last-check stamp for the daily/weekly frequency. The
// daemon's `updates.last_checked` is no use here: the daemon refreshes it on
// its own startup check moments before the GUI would read it.
function lastCheckFile(): string {
  return join(app.getPath('userData'), 'update-check.json')
}

function readLastChecked(): number | null {
  try {
    const data = JSON.parse(readFileSync(lastCheckFile(), 'utf-8')) as { lastChecked?: unknown }
    return typeof data.lastChecked === 'number' ? data.lastChecked : null
  } catch {
    return null
  }
}

function writeLastChecked(ms: number): void {
  try {
    writeFileSync(lastCheckFile(), JSON.stringify({ lastChecked: ms }))
  } catch {
    // best effort — worst case the next launch checks again
  }
}

// Every check goes through here so the auto-download setting is applied
// fresh each time. With it off, electron-updater only emits
// `update-available`; the banner's Download button is then the one thing
// that fetches the update (issue #61).
async function check(): Promise<void> {
  autoUpdater.autoDownload = readUpdatePolicy().autoDownload
  await autoUpdater.checkForUpdates()
  writeLastChecked(Date.now())
}

export function initAutoUpdater(): void {
  // Never download until a check has applied the user's setting.
  autoUpdater.autoDownload = false
  // Only affects an update that was already downloaded — automatically
  // (setting on) or because the user clicked Download.
  autoUpdater.autoInstallOnAppQuit = true

  // Events → every open window. The update banner lives in each renderer, so
  // a project window should learn about an available/downloaded update too.
  autoUpdater.on('update-available', (info) => {
    broadcast('update-available', {
      version: info.version,
      releaseNotes: info.releaseNotes
    })
  })

  autoUpdater.on('download-progress', (progress) => {
    broadcast('update-progress', progress.percent)
  })

  autoUpdater.on('update-downloaded', () => {
    broadcast('update-downloaded')
  })

  autoUpdater.on('error', (err) => {
    broadcast('update-error', err.message)
  })

  // IPC handlers
  ipcMain.handle('check-for-updates', async () => {
    // "Check Now" is an explicit request: it always checks, but still only
    // downloads on its own when auto-download is on.
    await check()
  })

  ipcMain.handle('download-update', async () => {
    await autoUpdater.downloadUpdate()
  })

  ipcMain.handle('install-update', async () => {
    // After restart, checkAndInstallCLI() will update the CLI/daemon binaries
    // since they're bundled in the new app version
    autoUpdater.quitAndInstall()
  })

  // Launch-time check — only if "Check on startup" is on and the configured
  // frequency says one is due.
  if (shouldCheckOnStartup(readUpdatePolicy(), readLastChecked(), Date.now())) {
    check().catch((err) => {
      console.log('Auto-update check failed:', err.message)
    })
  }
}
