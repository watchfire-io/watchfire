import { existsSync, copyFileSync, lstatSync, readlinkSync } from 'fs'
import { join } from 'path'
import { execSync } from 'child_process'
import { app, dialog } from 'electron'
import { stopDaemon } from './daemon'

const CLI_PATH = '/usr/local/bin/watchfire'
const DAEMON_PATH = '/usr/local/bin/watchfired'

interface InstallStatus {
  needed: boolean
  reason: 'missing' | 'outdated' | 'none'
}

/** Check if the installed CLI binaries need installation or update. */
export function needsInstall(): InstallStatus {
  if (!existsSync(CLI_PATH) || !existsSync(DAEMON_PATH)) {
    return { needed: true, reason: 'missing' }
  }

  // Check version of installed binary
  try {
    const output = execSync(`"${CLI_PATH}" version`, { encoding: 'utf-8', timeout: 5000 })
    const match = output.match(/Watchfire\s+([\d.]+)/)
    if (match) {
      const installedVersion = match[1]
      const appVersion = app.getVersion()
      if (installedVersion !== appVersion) {
        return { needed: true, reason: 'outdated' }
      }
    }
  } catch {
    // If we can't run the binary, treat as needing install
    return { needed: true, reason: 'outdated' }
  }

  return { needed: false, reason: 'none' }
}

/** Check if a binary is managed by Homebrew. */
function isHomebrewInstalled(binaryPath: string): boolean {
  try {
    const stat = lstatSync(binaryPath)
    if (!stat.isSymbolicLink()) return false
    const target = readlinkSync(binaryPath)
    return target.includes('/opt/homebrew/') || target.includes('/usr/local/Cellar/')
  } catch {
    return false
  }
}

/** Copy bundled binaries to /usr/local/bin. */
export async function installCLI(): Promise<boolean> {
  const bundledCLI = join(process.resourcesPath, 'watchfire')
  const bundledDaemon = join(process.resourcesPath, 'watchfired')

  if (!existsSync(bundledCLI) || !existsSync(bundledDaemon)) {
    throw new Error('Bundled binaries not found in app resources')
  }

  // Check if existing binaries are Homebrew-managed
  if (existsSync(CLI_PATH) && isHomebrewInstalled(CLI_PATH)) {
    dialog.showMessageBoxSync({
      type: 'info',
      title: 'Homebrew Installation Detected',
      message:
        'watchfire is installed via Homebrew. Use `brew upgrade watchfire` to update instead.',
      buttons: ['OK']
    })
    return false
  }

  // Try direct copy first
  try {
    copyFileSync(bundledCLI, CLI_PATH)
    copyFileSync(bundledDaemon, DAEMON_PATH)
    execSync(`chmod +x "${CLI_PATH}" "${DAEMON_PATH}"`)
    return true
  } catch {
    // Direct copy failed (likely permissions), fall back to osascript
  }

  // Fall back to osascript with admin privileges
  try {
    const script = `
      do shell script "cp '${bundledCLI}' '${CLI_PATH}' && cp '${bundledDaemon}' '${DAEMON_PATH}' && chmod +x '${CLI_PATH}' '${DAEMON_PATH}'" with administrator privileges
    `
    execSync(`osascript -e '${script.replace(/'/g, "'\\''")}'`, { timeout: 30000 })
    return true
  } catch {
    return false
  }
}

/** Check and prompt for CLI installation on startup. */
export async function checkAndInstallCLI(): Promise<void> {
  if (!app.isPackaged) return

  const status = needsInstall()
  if (!status.needed) return

  const message =
    status.reason === 'missing'
      ? 'Watchfire CLI tools are not installed. Install them to /usr/local/bin?'
      : `Watchfire CLI tools are outdated (app: ${app.getVersion()}). Update them?`

  const result = dialog.showMessageBoxSync({
    type: 'question',
    title: 'Install CLI Tools',
    message,
    buttons: ['Install', 'Skip'],
    defaultId: 0,
    cancelId: 1
  })

  if (result === 0) {
    try {
      const success = await installCLI()
      if (success) {
        // Stop the old daemon so ensureDaemon() will start the new version
        await stopDaemon()
        dialog.showMessageBoxSync({
          type: 'info',
          title: 'CLI Installed',
          message: 'Watchfire CLI tools have been installed successfully.',
          buttons: ['OK']
        })
      }
    } catch (err) {
      dialog.showMessageBoxSync({
        type: 'error',
        title: 'Installation Failed',
        message: `Failed to install CLI tools: ${err}`,
        buttons: ['OK']
      })
    }
  }
}
