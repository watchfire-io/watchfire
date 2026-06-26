import { contextBridge, ipcRenderer } from 'electron'

export interface DaemonInfo {
  host: string
  port: number
  pid: number
  started_at: string
}

export interface CLIInstallStatus {
  needed: boolean
  reason: 'missing' | 'outdated' | 'none'
}

export interface UpdateInfo {
  version: string
  releaseNotes?: string
}

const api = {
  getDaemonInfo: (): Promise<DaemonInfo | null> =>
    ipcRenderer.invoke('get-daemon-info'),

  ensureDaemon: (): Promise<DaemonInfo> =>
    ipcRenderer.invoke('ensure-daemon'),

  openFolderDialog: (): Promise<string | null> =>
    ipcRenderer.invoke('open-folder-dialog'),

  checkProjectExists: (folderPath: string): Promise<boolean> =>
    ipcRenderer.invoke('check-project-exists', folderPath),

  getVersion: (): Promise<string> =>
    ipcRenderer.invoke('get-version'),

  onDaemonShutdown: (callback: () => void): void => {
    ipcRenderer.on('daemon-shutdown', () => callback())
  },

  // CLI installation
  installCLI: (): Promise<boolean> =>
    ipcRenderer.invoke('install-cli'),

  checkCLIStatus: (): Promise<CLIInstallStatus> =>
    ipcRenderer.invoke('check-cli-status'),

  // Auto-updates
  checkForUpdates: (): Promise<void> =>
    ipcRenderer.invoke('check-for-updates'),

  downloadUpdate: (): Promise<void> =>
    ipcRenderer.invoke('download-update'),

  installUpdate: (): Promise<void> =>
    ipcRenderer.invoke('install-update'),

  onUpdateAvailable: (callback: (info: UpdateInfo) => void): void => {
    ipcRenderer.on('update-available', (_event, info) => callback(info))
  },

  onUpdateReady: (callback: () => void): void => {
    ipcRenderer.on('update-downloaded', () => callback())
  },

  onUpdateProgress: (callback: (percent: number) => void): void => {
    ipcRenderer.on('update-progress', (_event, percent) => callback(percent))
  },

  onUpdateError: (callback: (error: string) => void): void => {
    ipcRenderer.on('update-error', (_event, error) => callback(error))
  },

  // Terminal PTY
  ptyCreate: (cwd: string): Promise<string> => ipcRenderer.invoke('pty-create', cwd),
  ptyWrite: (id: string, data: string): Promise<void> => ipcRenderer.invoke('pty-write', id, data),
  ptyResize: (id: string, cols: number, rows: number): Promise<void> => ipcRenderer.invoke('pty-resize', id, cols, rows),
  ptyDestroy: (id: string): Promise<void> => ipcRenderer.invoke('pty-destroy', id),
  ptyDestroyAll: (): Promise<void> => ipcRenderer.invoke('pty-destroy-all'),
  onPtyOutput: (callback: (data: { id: string; data: string }) => void): void => {
    ipcRenderer.on('pty-output', (_ev, payload) => callback(payload))
  },
  offPtyOutput: (): void => {
    ipcRenderer.removeAllListeners('pty-output')
  },
  onPtyExit: (callback: (data: { id: string; exitCode: number }) => void): void => {
    ipcRenderer.on('pty-exit', (_ev, payload) => callback(payload))
  },
  offPtyExit: (): void => {
    ipcRenderer.removeAllListeners('pty-exit')
  },

  // Open a project path in an external IDE / file manager
  openInIDE: (ide: string, projectPath: string): Promise<{ ok: boolean; error?: string }> =>
    ipcRenderer.invoke('open-in-ide', ide, projectPath),

  // Pick a shell binary for the in-app terminal (issue #32 / global setting
  // `defaults.terminal_shell`). Returns the absolute path on selection,
  // null on cancel. The renderer posts the result through the
  // SettingsService.UpdateSettings RPC so the daemon validates and persists.
  browseShellBinary: (): Promise<string | null> =>
    ipcRenderer.invoke('browse-shell-binary'),

  // Bring the main window to the foreground. Used by the focus subscriber
  // when the daemon's tray emits a click so a hidden window comes back
  // into view.
  focusWindow: (): Promise<void> => ipcRenderer.invoke('focus-window'),

  // v8 Inferno — open (or focus) the home/dashboard window. A project window's
  // "Open another project" affordance calls this to get back to the
  // multi-project surface where the user can pick a different project.
  openHomeWindow: (): Promise<void> => ipcRenderer.invoke('open-home-window'),

  // v8 Inferno — open (or focus) a project's own window. The main process
  // resolves it through the window registry (one window per project). Reused
  // by the dashboard/sidebar "Open in new window" affordances (#0107).
  openProjectWindow: (projectId: string): Promise<void> =>
    ipcRenderer.invoke('open-project-window', projectId),

  // v8 Inferno (stretch) — open (or focus) the always-on-top mini-monitor: a
  // small floating window with a glanceable per-project fleet status.
  openMonitorWindow: (): Promise<void> => ipcRenderer.invoke('open-monitor-window'),

  // v8 Inferno — mission control. Open (or focus) a project's window AND route
  // its renderer to a surface (Tasks tab / a task / just-focus). Used by the
  // home window's needs-attention panel click-through. The main process defers
  // the routing message until the (possibly freshly-created) window's renderer
  // has loaded.
  focusProjectWindow: (
    projectId: string,
    target?: string,
    taskNumber?: number
  ): Promise<void> => ipcRenderer.invoke('focus-project-window', projectId, target, taskNumber),

  // Receive a routing request for this window (sent by `focusProjectWindow`).
  // App.tsx translates it into an app-store focus request.
  onProjectFocus: (
    callback: (payload: { projectId: string; target?: string; taskNumber?: number }) => void
  ): void => {
    ipcRenderer.on('project-focus', (_ev, payload) => callback(payload))
  },

  // v8 Inferno — mission control. The list of projects that currently have
  // their own window, so the home/dashboard can flip a card's affordance to
  // "focus existing window". Snapshot read; pair with `onProjectWindowsChanged`
  // to keep it live.
  listProjectWindows: (): Promise<string[]> => ipcRenderer.invoke('list-project-windows'),

  // Subscribe to open-project-window changes (a project window opened or
  // closed). The main process broadcasts the full id set. Returns an
  // unsubscribe fn so the home renderer can clean up on unmount.
  onProjectWindowsChanged: (callback: (projectIds: string[]) => void): (() => void) => {
    const handler = (_ev: unknown, projectIds: string[]): void => callback(projectIds)
    ipcRenderer.on('project-windows-changed', handler)
    return () => ipcRenderer.removeListener('project-windows-changed', handler)
  },

  // Show a native OS notification. The renderer relays each message it
  // receives from the daemon's NotificationService.Subscribe stream so
  // Electron's Notification API can produce the platform-correct toast /
  // banner with proper app attribution.
  emitNotification: (payload: {
    id: string
    kind: string
    projectId: string
    taskNumber: number
    title: string
    body: string
  }): Promise<void> => ipcRenderer.invoke('notifications:emit', payload),

  // Subscribe to "user clicked the OS notification" events from the main
  // process. Used by App.tsx to route the GUI to the failing project's
  // TasksTab — or to open the DigestModal when kind=WEEKLY_DIGEST.
  onNotificationClick: (
    callback: (payload: { kind?: string; projectId: string; taskNumber: number }) => void
  ): void => {
    ipcRenderer.on('notifications:click', (_ev, payload) => callback(payload))
  },

  // v6.0 Ember — read a single weekly-digest's Markdown body from
  // `~/.watchfire/digests/<dateKey>.md`. Returns null when the file is
  // missing (rare — we only ever ask for dates the daemon has emitted).
  readDigest: (dateKey: string): Promise<string | null> =>
    ipcRenderer.invoke('digests:read', dateKey),

  // v6.0 Ember — list available digest dates, newest first. Used by the
  // in-app notification center's Digests tab.
  listDigests: (): Promise<string[]> => ipcRenderer.invoke('digests:list')
}

contextBridge.exposeInMainWorld('watchfire', api)

export type WatchfireAPI = typeof api
