import { BrowserWindow, shell } from 'electron'
import { join } from 'path'
import { readFileSync } from 'fs'
import { homedir } from 'os'
import { is } from '@electron-toolkit/utils'
import { parse } from 'yaml'
import { loadWindowState, trackWindowState } from './window-state'
import { destroyForWindow as destroyPtysForWindow } from './pty-manager'

// A registry of every BrowserWindow the app owns. Replaces the single
// `mainWindow` singleton so the GUI can run one independent window per project
// plus a "home" (dashboard / mission-control) window. Keyed by `win.id`.
export interface WfWindow {
  win: BrowserWindow
  kind: 'home' | 'project'
  projectId?: string
}

const windows = new Map<number, WfWindow>()

// Resolve a project's display name from ~/.watchfire/projects.yaml so the
// window title reads "watchfire" instead of a bare UUID. Best-effort: any
// read/parse failure falls back to null (caller uses "Watchfire").
function resolveProjectName(projectId: string): string | null {
  try {
    const path = join(homedir(), '.watchfire', 'projects.yaml')
    const raw = readFileSync(path, 'utf-8')
    const parsed = parse(raw) as { projects?: Array<{ project_id: string; name: string }> } | null
    const match = parsed?.projects?.find((p) => p.project_id === projectId)
    return match?.name?.trim() || null
  } catch {
    return null
  }
}

// Window construction options shared by every window — webPreferences,
// titleBar, backgroundColor. Bounds (width/height/x/y) are layered on by the
// caller so the home window can restore its saved geometry.
function baseWindowOptions(title: string): Electron.BrowserWindowConstructorOptions {
  return {
    minWidth: 900,
    minHeight: 600,
    show: false,
    title,
    ...(process.platform === 'darwin'
      ? { titleBarStyle: 'hiddenInset', trafficLightPosition: { x: 16, y: 16 } }
      : { frame: true }),
    backgroundColor: '#16181d',
    webPreferences: {
      preload: join(__dirname, '../preload/index.js'),
      sandbox: false,
      contextIsolation: true,
      nodeIntegration: false
    }
  }
}

// Register a freshly created window in the registry and wire the lifecycle
// behaviour every window shares (show-on-ready, external-link handling,
// dev tools, and self-removal from the registry on close).
function registerWindow(win: BrowserWindow, kind: 'home' | 'project', projectId?: string): void {
  windows.set(win.id, { win, kind, projectId })

  win.on('ready-to-show', () => {
    win.show()
    // Auto-open DevTools in dev so any residual renderer error is visible to
    // anyone running `npm run dev` without requiring Cmd+Opt+I.
    if (is.dev) {
      win.webContents.openDevTools({ mode: 'detach' })
    }
  })

  win.webContents.setWindowOpenHandler((details) => {
    shell.openExternal(details.url)
    return { action: 'deny' }
  })

  win.on('closed', () => {
    // Tear down only this window's integrated terminals; other windows' PTYs
    // stay alive. Capture the id before the BrowserWindow is gone.
    destroyPtysForWindow(win.id)
    windows.delete(win.id)
  })
}

// Load the renderer into a window, optionally scoped to a project via a query
// string. The `app://` protocol handler ignores the query (it resolves on
// `new URL().pathname`), so prod windows load the same bundle; the renderer
// reads `?project=<id>` at boot to scope itself.
function loadRenderer(win: BrowserWindow, query: string): void {
  if (is.dev && process.env['ELECTRON_RENDERER_URL']) {
    win.loadURL(`${process.env['ELECTRON_RENDERER_URL']}${query}`)
  } else {
    win.loadURL(`app://renderer/index.html${query}`)
  }
}

function focusWindow(win: BrowserWindow): BrowserWindow {
  if (win.isMinimized()) win.restore()
  win.focus()
  return win
}

// The dashboard / mission-control window. Singleton: if one is already open,
// focus it instead of spawning another. Restores saved geometry.
export function createHomeWindow(): BrowserWindow {
  const existing = getHomeWindow()
  if (existing) return focusWindow(existing)

  const savedState = loadWindowState()
  const usePosition = savedState.x !== -1 && savedState.y !== -1

  const win = new BrowserWindow({
    width: savedState.width,
    height: savedState.height,
    ...(usePosition ? { x: savedState.x, y: savedState.y } : {}),
    ...baseWindowOptions('Watchfire')
  })

  trackWindowState(win)
  registerWindow(win, 'home')
  loadRenderer(win, '')
  return win
}

// Open (or focus) the window dedicated to a single project. The renderer boots
// scoped via `?project=<id>`. One window per project: a second call for an
// already-open project just focuses it.
export function createProjectWindow(projectId: string): BrowserWindow {
  const existing = getProjectWindow(projectId)
  if (existing) return focusWindow(existing)

  const name = resolveProjectName(projectId)
  const win = new BrowserWindow({
    width: 1280,
    height: 800,
    ...baseWindowOptions(name ?? 'Watchfire')
  })

  registerWindow(win, 'project', projectId)
  loadRenderer(win, `?project=${encodeURIComponent(projectId)}`)
  return win
}

export function getProjectWindow(projectId: string): BrowserWindow | null {
  for (const w of windows.values()) {
    if (w.kind === 'project' && w.projectId === projectId) return w.win
  }
  return null
}

export function getHomeWindow(): BrowserWindow | null {
  for (const w of windows.values()) {
    if (w.kind === 'home') return w.win
  }
  return null
}

export function allWindows(): BrowserWindow[] {
  return [...windows.values()].map((w) => w.win)
}

export function windowForId(id: number): WfWindow | undefined {
  return windows.get(id)
}
