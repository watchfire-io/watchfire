import { useEffect, useState } from 'react'

/**
 * Tracks which projects currently have their own OS window open (v8 Inferno —
 * mission control). The home/dashboard window uses this so a card's primary
 * affordance flips from "open in new window" to "focus existing window" and the
 * main process never spawns a duplicate.
 *
 * Live: reads the snapshot once at mount, then keeps the set fresh from the
 * main process's `project-windows-changed` broadcast (fired on every project
 * window open/close). Returns a Set keyed by projectId.
 */
export function useOpenProjectWindows(): Set<string> {
  const [open, setOpen] = useState<Set<string>>(() => new Set())

  useEffect(() => {
    let active = true
    void window.watchfire.listProjectWindows().then((ids) => {
      if (active) setOpen(new Set(ids))
    })
    const unsubscribe = window.watchfire.onProjectWindowsChanged((ids) => {
      setOpen(new Set(ids))
    })
    return () => {
      active = false
      unsubscribe()
    }
  }, [])

  return open
}
