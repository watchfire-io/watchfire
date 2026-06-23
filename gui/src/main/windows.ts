import { BrowserWindow, shell } from 'electron'
import { join } from 'path'
import { readFileSync } from 'fs'
import { homedir } from 'os'
import { is } from '@electron-toolkit/utils'
import { parse } from 'yaml'
import {
  loadWindowState,
  trackWindowState,
  addOpenProject,
  removeOpenProject,
  getOpenProjects
} from './window-state'
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

// The id of the window that most recently gained focus. Drives `focus-window`
// and notification routing fallbacks so a tray click or a digest notification
// lands on the window the user was last looking at rather than an arbitrary one.
let lastFocusedId: number | null = null

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

// All project ids currently registered in ~/.watchfire/projects.yaml. Used to
// drop stale ids when restoring open windows on relaunch.
function listProjectIds(): string[] {
  try {
    const path = join(homedir(), '.watchfire', 'projects.yaml')
    const raw = readFileSync(path, 'utf-8')
    const parsed = parse(raw) as { projects?: Array<{ project_id: string }> } | null
    return parsed?.projects?.map((p) => p.project_id) ?? []
  } catch {
    return []
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

  // Track the most-recently-focused window for focus/notification routing.
  win.on('focus', () => {
    lastFocusedId = win.id
  })

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
    if (lastFocusedId === win.id) lastFocusedId = null
    // A project window closing changes the open-windows set the home/dashboard
    // renders; broadcast so its "focus existing window" affordances update live.
    if (kind === 'project') broadcastProjectWindows()
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

  const savedState = loadWindowState('home')
  const usePosition = savedState.x !== -1 && savedState.y !== -1

  const win = new BrowserWindow({
    width: savedState.width,
    height: savedState.height,
    ...(usePosition ? { x: savedState.x, y: savedState.y } : {}),
    ...baseWindowOptions('Watchfire')
  })

  trackWindowState(win, 'home')
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
  const savedState = loadWindowState(projectId)
  const usePosition = savedState.x !== -1 && savedState.y !== -1

  const win = new BrowserWindow({
    width: savedState.width,
    height: savedState.height,
    ...(usePosition ? { x: savedState.x, y: savedState.y } : {}),
    ...baseWindowOptions(name ?? 'Watchfire')
  })

  trackWindowState(win, projectId)
  addOpenProject(projectId)
  win.on('closed', () => removeOpenProject(projectId))
  registerWindow(win, 'project', projectId)
  loadRenderer(win, `?project=${encodeURIComponent(projectId)}`)
  // Tell the home/dashboard window a new project window now exists so its
  // per-card affordance flips to "focus existing window".
  broadcastProjectWindows()
  return win
}

// Re-open the project windows that were open at last quit (v8 Inferno D3).
// Stale ids — projects deleted from projects.yaml while the app was closed —
// are skipped so a removed project never resurrects an empty window.
export function restoreOpenProjectWindows(): void {
  const valid = new Set(listProjectIds())
  for (const projectId of getOpenProjects()) {
    if (valid.has(projectId)) {
      createProjectWindow(projectId)
    } else {
      removeOpenProject(projectId)
    }
  }
}

// The project ids that currently have a live window, derived from the registry
// (not the persisted last-quit set in window-state). The home/dashboard window
// uses this to show which projects are already open so it focuses rather than
// duplicates. Broadcast on every open/close via `project-windows-changed`.
export function getOpenProjectWindowIds(): string[] {
  const ids: string[] = []
  for (const w of windows.values()) {
    if (w.kind === 'project' && w.projectId) ids.push(w.projectId)
  }
  return ids
}

// Push the current open-project-window set to every renderer. Cheap and
// idempotent — the home window dedups in its store.
function broadcastProjectWindows(): void {
  broadcast('project-windows-changed', getOpenProjectWindowIds())
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

// Send an IPC message to EVERY open window. Used for app-wide lifecycle/update
// events (daemon-ready/shutdown, update-*) that are not scoped to one project.
// Destroyed windows are skipped — a window can be torn down between iterations.
export function broadcast(channel: string, ...args: unknown[]): void {
  for (const { win } of windows.values()) {
    if (!win.isDestroyed()) win.webContents.send(channel, ...args)
  }
}

// The window the user most recently focused, or the home window as a fallback
// (or null if nothing is open). Used to route `focus-window` and digest
// notification clicks to the most contextually-relevant window.
export function getMostRecentlyFocusedWindow(): BrowserWindow | null {
  if (lastFocusedId !== null) {
    const w = windows.get(lastFocusedId)
    if (w && !w.win.isDestroyed()) return w.win
  }
  return getHomeWindow()
}

// Cycle focus across the open windows (v8 Inferno — Cmd+Shift+] / Cmd+Shift+[).
// `direction` is +1 for next, -1 for previous. Windows are ordered by `win.id`
// (creation order) for a stable, predictable cycle, and the traversal wraps
// around. No-op with fewer than two live windows.
export function focusAdjacentWindow(direction: 1 | -1): void {
  const wins = [...windows.values()]
    .map((w) => w.win)
    .filter((w) => !w.isDestroyed())
    .sort((a, b) => a.id - b.id)
  if (wins.length < 2) return

  const focused = BrowserWindow.getFocusedWindow()
  const currentIdx = focused ? wins.findIndex((w) => w.id === focused.id) : -1
  const nextIdx =
    currentIdx === -1
      ? direction === 1
        ? 0
        : wins.length - 1
      : (currentIdx + direction + wins.length) % wins.length

  const target = wins[nextIdx]
  if (target.isMinimized()) target.restore()
  target.focus()
}
