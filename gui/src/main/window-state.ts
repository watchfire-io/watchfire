import { BrowserWindow, screen } from 'electron'
import { readFileSync, writeFileSync, mkdirSync } from 'fs'
import { join } from 'path'
import { homedir } from 'os'

interface WindowBounds {
  x: number
  y: number
  width: number
  height: number
}

// Keyed window state. `home` holds the dashboard window bounds; `monitor`
// holds the always-on-top mini-monitor bounds (v8 Inferno — stretch);
// `projects` holds per-project window bounds keyed by projectId; `openProjects`
// lists the project ids whose windows were open at last quit, so they can be
// restored on relaunch (v8 Inferno D3).
interface WindowStateFile {
  home?: WindowBounds
  monitor?: WindowBounds
  projects?: Record<string, WindowBounds>
  openProjects?: string[]
}

// A window-state key is the singleton home window, the singleton mini-monitor,
// or a projectId.
export type WindowStateKey = 'home' | 'monitor' | string

const STATE_FILE = join(homedir(), '.watchfire', 'window-state.json')

const DEFAULTS: WindowBounds = {
  x: -1,
  y: -1,
  width: 1280,
  height: 800
}

// Read and normalise the on-disk state. Backward-compat: the v7 schema was a
// bare bounds rectangle `{x,y,width,height}` for the single (home) window; if
// we find that shape, treat it as the home bounds.
function readState(): WindowStateFile {
  try {
    const data = JSON.parse(readFileSync(STATE_FILE, 'utf-8')) as
      | WindowStateFile
      | WindowBounds
    if (data && typeof (data as WindowBounds).width === 'number') {
      // Old flat shape — migrate to keyed `home`.
      return { home: data as WindowBounds }
    }
    return (data as WindowStateFile) ?? {}
  } catch {
    // File doesn't exist or is invalid
    return {}
  }
}

function writeState(state: WindowStateFile): void {
  try {
    mkdirSync(join(homedir(), '.watchfire'), { recursive: true })
    writeFileSync(STATE_FILE, JSON.stringify(state))
  } catch {
    // Ignore write errors
  }
}

// Is the saved top-left corner on a currently-visible display? Guards against
// restoring a window off-screen after a monitor is unplugged.
function isOnVisibleDisplay(bounds: WindowBounds): boolean {
  const displays = screen.getAllDisplays()
  return displays.some((d) => {
    const b = d.bounds
    return (
      bounds.x >= b.x - 50 &&
      bounds.y >= b.y - 50 &&
      bounds.x < b.x + b.width &&
      bounds.y < b.y + b.height
    )
  })
}

function boundsForKey(state: WindowStateFile, key: WindowStateKey): WindowBounds | undefined {
  if (key === 'home') return state.home
  if (key === 'monitor') return state.monitor
  return state.projects?.[key]
}

// Load the saved bounds for a given window key ('home', 'monitor', or a
// projectId), validating that the position is on a visible display. Falls back
// to `fallback` (default geometry), which the mini-monitor overrides with a
// smaller default size than the standard windows.
export function loadWindowState(key: WindowStateKey, fallback: WindowBounds = DEFAULTS): WindowBounds {
  const bounds = boundsForKey(readState(), key)
  if (bounds && bounds.width > 0 && bounds.height > 0 && isOnVisibleDisplay(bounds)) {
    return bounds
  }
  return { ...fallback }
}

// Persist the bounds for a given window key, preserving the rest of the state.
export function saveWindowState(key: WindowStateKey, bounds: WindowBounds): void {
  const state = readState()
  if (key === 'home') {
    state.home = bounds
  } else if (key === 'monitor') {
    state.monitor = bounds
  } else {
    state.projects = { ...state.projects, [key]: bounds }
  }
  writeState(state)
}

// Track resize/move/close on a window, debouncing saves under its key.
export function trackWindowState(win: BrowserWindow, key: WindowStateKey): void {
  let saveTimeout: ReturnType<typeof setTimeout> | null = null

  const debouncedSave = (): void => {
    if (saveTimeout) clearTimeout(saveTimeout)
    saveTimeout = setTimeout(() => {
      saveWindowState(key, win.getBounds())
    }, 500)
  }

  win.on('resize', debouncedSave)
  win.on('move', debouncedSave)
  win.on('close', () => {
    if (saveTimeout) clearTimeout(saveTimeout)
    saveWindowState(key, win.getBounds())
  })
}

// Record/forget a project window in the `openProjects` set so it can be
// restored on relaunch. Called on project-window create (add) and close
// (remove). The home window is never tracked here.
export function addOpenProject(projectId: string): void {
  const state = readState()
  const open = new Set(state.openProjects ?? [])
  open.add(projectId)
  state.openProjects = [...open]
  writeState(state)
}

export function removeOpenProject(projectId: string): void {
  const state = readState()
  if (!state.openProjects?.length) return
  state.openProjects = state.openProjects.filter((id) => id !== projectId)
  writeState(state)
}

// The project ids whose windows were open at last quit. Restored on relaunch.
export function getOpenProjects(): string[] {
  return readState().openProjects ?? []
}
