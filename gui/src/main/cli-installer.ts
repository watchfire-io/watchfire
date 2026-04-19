import { existsSync, copyFileSync, lstatSync, readlinkSync, mkdirSync } from 'fs'
import { join } from 'path'
import { execSync } from 'child_process'
import { app, dialog } from 'electron'
import { stopDaemon } from './daemon'
import { parseCLIVersion, compareSemver } from './version'

const IS_MAC = process.platform === 'darwin'
const IS_WIN = process.platform === 'win32'
const IS_LINUX = process.platform === 'linux'

// Platform-specific install directories.
// User-owned paths come first so that when the GUI installs to ~/.local/bin,
// the next launch's version check reads that same binary (not a stale
// /usr/local/bin copy left over from a prior manual install — the root cause
// of #30 on Linux: install target and lookup order disagreed, so every launch
// re-prompted against an older system binary).
const MAC_INSTALL_DIRS = ['/usr/local/bin', '/opt/homebrew/bin']
const LINUX_INSTALL_DIRS = [join(process.env.HOME || '', '.local', 'bin'), '/usr/local/bin']
const WIN_INSTALL_DIR = join(
  process.env.LOCALAPPDATA || join(process.env.USERPROFILE || '', 'AppData', 'Local'),
  'Watchfire'
)

const CLI_NAME = IS_WIN ? 'watchfire.exe' : 'watchfire'
const DAEMON_NAME = IS_WIN ? 'watchfired.exe' : 'watchfired'

function getInstallDir(): string {
  if (IS_MAC) return '/usr/local/bin'
  if (IS_LINUX) return join(process.env.HOME || '', '.local', 'bin')
  return WIN_INSTALL_DIR
}

function getSearchDirs(): string[] {
  if (IS_MAC) return MAC_INSTALL_DIRS
  if (IS_LINUX) return LINUX_INSTALL_DIRS
  return [WIN_INSTALL_DIR]
}

/** Find an installed binary in known paths, falling back to PATH lookup. */
function findInstalledPath(binary: string): string | null {
  for (const dir of getSearchDirs()) {
    const p = join(dir, binary)
    if (existsSync(p)) return p
  }
  // Fall back to PATH lookup so system-package installs (rpm/deb to /usr/bin)
  // or Linuxbrew installs are also recognized.
  try {
    const cmd = IS_WIN ? `where ${binary}` : `command -v ${binary}`
    const result = execSync(cmd, { encoding: 'utf-8', timeout: 5000 })
    const firstLine = result.trim().split('\n')[0]
    if (firstLine && existsSync(firstLine)) return firstLine
  } catch {
    // not found in PATH
  }
  return null
}

interface InstallStatus {
  needed: boolean
  reason: 'missing' | 'outdated' | 'none'
}

/** Check if the installed CLI binaries need installation or update. */
export function needsInstall(): InstallStatus {
  const cliPath = findInstalledPath(CLI_NAME)
  const daemonPath = findInstalledPath(DAEMON_NAME)

  if (!cliPath || !daemonPath) {
    return { needed: true, reason: 'missing' }
  }

  const appVersion = app.getVersion()

  try {
    const rawOutput = execSync(`"${cliPath}" version`, { encoding: 'utf-8', timeout: 5000 })
    const installedVersion = parseCLIVersion(rawOutput)
    if (!installedVersion) {
      // Couldn't extract a version (e.g. dev build printing "Watchfire dev").
      // Fall through without prompting — the user is running something
      // unofficial and shouldn't be nagged about it.
      console.warn(
        `[cli-installer] Could not parse version from ${cliPath} output; skipping update check`
      )
      return { needed: false, reason: 'none' }
    }

    const cmp = compareSemver(installedVersion, appVersion)
    if (cmp === null) {
      console.warn(
        `[cli-installer] Non-semver versions — installed=${installedVersion} app=${appVersion}; skipping update check`
      )
      return { needed: false, reason: 'none' }
    }
    if (cmp < 0) {
      console.log(
        `[cli-installer] Installed CLI is older — installed=${installedVersion} app=${appVersion}`
      )
      return { needed: true, reason: 'outdated' }
    }
    // Equal or newer — nothing to do.
  } catch (err) {
    // If we can't run the binary, treat as needing install.
    // Log the actual error for debugging (e.g., wrong platform binary, missing libs).
    console.error(
      '[cli-installer] Failed to check CLI version:',
      err instanceof Error ? err.message : err
    )
    return { needed: true, reason: 'outdated' }
  }

  return { needed: false, reason: 'none' }
}

/** Check if a binary is managed by Homebrew (macOS/Linux only). */
function isHomebrewInstalled(binaryPath: string): boolean {
  if (IS_WIN) return false
  try {
    const stat = lstatSync(binaryPath)
    if (!stat.isSymbolicLink()) return false
    const target = readlinkSync(binaryPath)
    return target.includes('/opt/homebrew/') || target.includes('/usr/local/Cellar/')
  } catch {
    return false
  }
}

/** Copy bundled binaries to the platform-specific install directory. */
export async function installCLI(): Promise<boolean> {
  // On macOS, bundled binaries are without extension (universal).
  // On Linux/Windows, they match the platform binary name.
  const bundledCLI = join(process.resourcesPath, CLI_NAME)
  const bundledDaemon = join(process.resourcesPath, DAEMON_NAME)

  if (!existsSync(bundledCLI) || !existsSync(bundledDaemon)) {
    throw new Error('Bundled binaries not found in app resources')
  }

  // Check if existing binaries are Homebrew-managed (macOS/Linux)
  const existingCLI = findInstalledPath(CLI_NAME)
  if (existingCLI && isHomebrewInstalled(existingCLI)) {
    dialog.showMessageBoxSync({
      type: 'info',
      title: 'Homebrew Installation Detected',
      message:
        'watchfire is installed via Homebrew. Use `brew upgrade watchfire` to update instead.',
      buttons: ['OK']
    })
    return false
  }

  const installDir = getInstallDir()
  const cliDest = join(installDir, CLI_NAME)
  const daemonDest = join(installDir, DAEMON_NAME)

  // Ensure install directory exists
  if (!existsSync(installDir)) {
    mkdirSync(installDir, { recursive: true })
  }

  // Try direct copy first
  try {
    copyFileSync(bundledCLI, cliDest)
    copyFileSync(bundledDaemon, daemonDest)
    if (!IS_WIN) {
      execSync(`chmod +x "${cliDest}" "${daemonDest}"`)
    }
    return true
  } catch {
    // Direct copy failed (likely permissions)
  }

  if (IS_MAC) {
    // Fall back to osascript with admin privileges
    try {
      const script = `
        do shell script "cp '${bundledCLI}' '${cliDest}' && cp '${bundledDaemon}' '${daemonDest}' && chmod +x '${cliDest}' '${daemonDest}'" with administrator privileges
      `
      execSync(`osascript -e '${script.replace(/'/g, "'\\''")}'`, { timeout: 30000 })
      return true
    } catch {
      return false
    }
  }

  if (IS_LINUX) {
    // Fall back to pkexec for admin privileges
    try {
      execSync(
        `pkexec sh -c 'cp "${bundledCLI}" "${cliDest}" && cp "${bundledDaemon}" "${daemonDest}" && chmod +x "${cliDest}" "${daemonDest}"'`,
        { timeout: 30000 }
      )
      return true
    } catch {
      return false
    }
  }

  // Windows: try PowerShell elevation
  if (IS_WIN) {
    try {
      const psScript = `
        Start-Process powershell -Verb RunAs -ArgumentList '-Command', 'Copy-Item "${bundledCLI}" "${cliDest}" -Force; Copy-Item "${bundledDaemon}" "${daemonDest}" -Force'
      `.replace(/\\/g, '\\\\')
      execSync(`powershell -Command "${psScript}"`, { timeout: 30000 })
      return true
    } catch {
      return false
    }
  }

  return false
}

/** Check and prompt for CLI installation on startup. */
export async function checkAndInstallCLI(): Promise<void> {
  if (!app.isPackaged) return

  const status = needsInstall()
  if (!status.needed) return

  const message =
    status.reason === 'missing'
      ? 'Watchfire CLI tools are not installed. Install them to ' + getInstallDir() + '?'
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
