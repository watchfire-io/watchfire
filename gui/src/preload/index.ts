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

  // Bring the main window to the foreground. Used by the focus subscriber
  // when the daemon's tray emits a click so a hidden window comes back
  // into view.
  focusWindow: (): Promise<void> => ipcRenderer.invoke('focus-window'),

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
  // TasksTab.
  onNotificationClick: (
    callback: (payload: { projectId: string; taskNumber: number }) => void
  ): void => {
    ipcRenderer.on('notifications:click', (_ev, payload) => callback(payload))
  }
}

contextBridge.exposeInMainWorld('watchfire', api)

export type WatchfireAPI = typeof api
