import { BrowserWindow, screen } from 'electron'
import { readFileSync, writeFileSync, mkdirSync } from 'fs'
import { join } from 'path'
import { homedir } from 'os'

interface WindowState {
  x: number
  y: number
  width: number
  height: number
}

const STATE_FILE = join(homedir(), '.watchfire', 'window-state.json')

const DEFAULTS: WindowState = {
  x: -1,
  y: -1,
  width: 1280,
  height: 800
}

export function loadWindowState(): WindowState {
  try {
    const data = JSON.parse(readFileSync(STATE_FILE, 'utf-8')) as WindowState
    // Validate that the saved position is on a visible display
    const displays = screen.getAllDisplays()
    const visible = displays.some((d) => {
      const b = d.bounds
      return (
        data.x >= b.x - 50 &&
        data.y >= b.y - 50 &&
        data.x < b.x + b.width &&
        data.y < b.y + b.height
      )
    })
    if (visible && data.width > 0 && data.height > 0) {
      return data
    }
  } catch {
    // File doesn't exist or is invalid
  }
  return { ...DEFAULTS }
}

function saveWindowState(state: WindowState): void {
  try {
    mkdirSync(join(homedir(), '.watchfire'), { recursive: true })
    writeFileSync(STATE_FILE, JSON.stringify(state))
  } catch {
    // Ignore write errors
  }
}

export function trackWindowState(win: BrowserWindow): void {
  let saveTimeout: ReturnType<typeof setTimeout> | null = null

  const debouncedSave = (): void => {
    if (saveTimeout) clearTimeout(saveTimeout)
    saveTimeout = setTimeout(() => {
      const bounds = win.getBounds()
      saveWindowState(bounds)
    }, 500)
  }

  win.on('resize', debouncedSave)
  win.on('move', debouncedSave)
  win.on('close', () => {
    if (saveTimeout) clearTimeout(saveTimeout)
    const bounds = win.getBounds()
    saveWindowState(bounds)
  })
}
