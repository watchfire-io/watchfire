import { app, protocol, net } from 'electron'
import { join } from 'path'
import { existsSync } from 'fs'
import { pathToFileURL } from 'url'
import { homedir } from 'os'
import { electronApp, optimizer } from '@electron-toolkit/utils'
import { setupIpc } from './ipc'
import { ensureDaemon, getDaemonInfo } from './daemon'
import { checkAndInstallCLI } from './cli-installer'
import { initAutoUpdater } from './auto-updater'
import { destroyAll as destroyAllPtys } from './pty-manager'
import { createHomeWindow, broadcast } from './windows'

const DAEMON_YAML = join(homedir(), '.watchfire', 'daemon.yaml')

let daemonWatcherInterval: ReturnType<typeof setInterval> | null = null
let watchedDaemonPid: number | null = null

function startDaemonWatcher(): void {
  // Clean up any existing watcher
  if (daemonWatcherInterval) clearInterval(daemonWatcherInterval)

  ensureDaemon()
    .then((info) => {
      watchedDaemonPid = info.pid
      // Fan out to every open window — each renderer drives its own daemon
      // connection and needs to learn the daemon is up.
      broadcast('daemon-ready')

      daemonWatcherInterval = setInterval(async () => {
        try {
          process.kill(watchedDaemonPid!, 0)
        } catch {
          clearInterval(daemonWatcherInterval!)
          daemonWatcherInterval = null

          if (!existsSync(DAEMON_YAML)) {
            // daemon.yaml removed → graceful shutdown, quit the app
            console.log('Daemon shut down gracefully, quitting GUI...')
            broadcast('daemon-shutdown')
            setTimeout(() => app.quit(), 3000)
          } else {
            // daemon.yaml still exists with stale PID → crash, restart
            console.log('Daemon process gone, restarting...')
            startDaemonWatcher()
          }
        }
      }, 2000)
    })
    .catch((err) => {
      console.error('Failed to start daemon:', err)
      // Retry after a delay
      setTimeout(() => startDaemonWatcher(), 5000)
    })
}

const RENDERER_DIR = join(__dirname, '../renderer')

function registerAppProtocol(): void {
  protocol.handle('app', (request) => {
    const url = new URL(request.url)
    // Strip leading slash and normalize. `app://renderer/index.html` ->
    // `index.html` inside RENDERER_DIR. Any sub-path (assets/foo.js)
    // resolves under the same root.
    const relative = decodeURIComponent(url.pathname).replace(/^\/+/, '')
    const resolved = join(RENDERER_DIR, relative)
    // Guard against path traversal escaping the renderer dir.
    if (!resolved.startsWith(RENDERER_DIR)) {
      return new Response('Forbidden', { status: 403 })
    }
    return net.fetch(pathToFileURL(resolved).toString())
  })
}

// Privileged schemes must be declared before `app.whenReady()` resolves.
protocol.registerSchemesAsPrivileged([
  {
    scheme: 'app',
    privileges: {
      standard: true,
      secure: true,
      supportFetchAPI: true,
      corsEnabled: true
    }
  }
])

// Single-instance lock: a second `watchfire` launch must focus the existing
// instance rather than spawn a second Electron process — two processes would
// each run a daemon watcher and double-merge worktrees. If we don't hold the
// lock, quit immediately and let the running instance handle `second-instance`.
const gotSingleInstanceLock = app.requestSingleInstanceLock()
if (!gotSingleInstanceLock) {
  app.quit()
} else {
  app.on('second-instance', () => {
    // Another launch was redirected here — surface the home window.
    createHomeWindow()
  })

  app.whenReady().then(async () => {
    electronApp.setAppUserModelId('io.watchfire.app')

    registerAppProtocol()

    // Optimize for development
    app.on('browser-window-created', (_, window) => {
      optimizer.watchWindowShortcuts(window)
    })

    // First-launch CLI installation (only when packaged)
    await checkAndInstallCLI()

    // Ensure daemon is running and auto-restart if it dies
    startDaemonWatcher()

    // Set up IPC handlers
    setupIpc()

    // Open the dashboard-first home window. The PTY manager is window-aware
    // (each session routes to its originating window) and the auto-updater now
    // broadcasts update events to every open window.
    createHomeWindow()
    initAutoUpdater()

    app.on('activate', () => {
      // Dock click (macOS) → focus the home window, creating it if none.
      createHomeWindow()
    })
  })
}

app.on('window-all-closed', () => {
  destroyAllPtys()
  // Don't stop the daemon when GUI closes
  if (process.platform !== 'darwin') {
    app.quit()
  }
})

app.on('before-quit', () => {
  destroyAllPtys()
})
