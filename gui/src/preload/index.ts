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
  }
}

contextBridge.exposeInMainWorld('watchfire', api)

export type WatchfireAPI = typeof api
