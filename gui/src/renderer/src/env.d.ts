/// <reference types="vite/client" />

interface DaemonInfo {
  host: string
  port: number
  pid: number
  started_at: string
}

interface CLIInstallStatus {
  needed: boolean
  reason: 'missing' | 'outdated' | 'none'
}

interface UpdateInfo {
  version: string
  releaseNotes?: string
}

interface WatchfireAPI {
  getDaemonInfo(): Promise<DaemonInfo | null>
  ensureDaemon(): Promise<DaemonInfo>
  openFolderDialog(): Promise<string | null>
  checkProjectExists(folderPath: string): Promise<boolean>
  getVersion(): Promise<string>
  onDaemonShutdown(callback: () => void): void

  // CLI installation
  installCLI(): Promise<boolean>
  checkCLIStatus(): Promise<CLIInstallStatus>

  // Auto-updates
  checkForUpdates(): Promise<void>
  downloadUpdate(): Promise<void>
  installUpdate(): Promise<void>
  onUpdateAvailable(callback: (info: UpdateInfo) => void): void
  onUpdateReady(callback: () => void): void
  onUpdateProgress(callback: (percent: number) => void): void
  onUpdateError(callback: (error: string) => void): void

  // Terminal PTY
  ptyCreate(cwd: string): Promise<string>
  ptyWrite(id: string, data: string): Promise<void>
  ptyResize(id: string, cols: number, rows: number): Promise<void>
  ptyDestroy(id: string): Promise<void>
  ptyDestroyAll(): Promise<void>
  onPtyOutput(callback: (data: { id: string; data: string }) => void): void
  offPtyOutput(): void
  onPtyExit(callback: (data: { id: string; exitCode: number }) => void): void
  offPtyExit(): void

  // Open a project path in an external IDE / file manager
  openInIDE(ide: string, projectPath: string): Promise<{ ok: boolean; error?: string }>

  // Browse for a custom shell binary (issue #32 / `defaults.terminal_shell`).
  // Returns the absolute path on pick, null on cancel.
  browseShellBinary(): Promise<string | null>

  // Bring the main window to the foreground
  focusWindow(): Promise<void>

  // v8 Inferno — open/focus the home window or a project's window
  openHomeWindow(): Promise<void>
  openProjectWindow(projectId: string): Promise<void>

  // v8 Inferno — mission control: open/focus a project window and route it to a
  // surface (needs-attention click-through), plus the renderer-side receiver.
  focusProjectWindow(projectId: string, target?: string, taskNumber?: number): Promise<void>
  onProjectFocus(
    callback: (payload: { projectId: string; target?: string; taskNumber?: number }) => void
  ): void

  // v8 Inferno — mission control: which projects already have their own window,
  // plus a live subscription so the dashboard can focus instead of duplicate.
  listProjectWindows(): Promise<string[]>
  onProjectWindowsChanged(callback: (projectIds: string[]) => void): () => void

  // Native OS notifications (v5.0 Pulse)
  emitNotification(payload: {
    id: string
    kind: string
    projectId: string
    taskNumber: number
    title: string
    body: string
  }): Promise<void>
  onNotificationClick(
    callback: (payload: { kind?: string; projectId: string; taskNumber: number }) => void
  ): void

  // v6.0 Ember — weekly digest reads
  readDigest(dateKey: string): Promise<string | null>
  listDigests(): Promise<string[]>
}

declare global {
  interface Window {
    watchfire: WatchfireAPI
  }
}

export {}
