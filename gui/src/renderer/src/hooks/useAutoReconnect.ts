import { useEffect, useRef, useState, useCallback } from 'react'
import { useAppStore } from '../stores/app-store'
import { useProjectsStore } from '../stores/projects-store'
import { connectToDaemon } from '../lib/daemon'

/** Automatically reconnect to daemon every 3s when disconnected */
export function useAutoReconnect(): { wasConnected: boolean; stopReconnect: () => void } {
  const connected = useAppStore((s) => s.connected)
  const setConnected = useAppStore((s) => s.setConnected)
  const fetchProjects = useProjectsStore((s) => s.fetchProjects)
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const stoppedRef = useRef(false)
  const [wasConnected, setWasConnected] = useState(false)

  const stopReconnect = useCallback(() => {
    stoppedRef.current = true
    if (intervalRef.current) {
      clearInterval(intervalRef.current)
      intervalRef.current = null
    }
  }, [])

  // Track if we were ever connected (for disconnect overlay)
  useEffect(() => {
    if (connected) setWasConnected(true)
  }, [connected])

  useEffect(() => {
    // Initial connection
    connectToDaemon()
      .then(({ port }) => {
        setConnected(true, port)
        fetchProjects()
      })
      .catch(() => setConnected(false))

    return () => {
      if (intervalRef.current) clearInterval(intervalRef.current)
    }
  }, [])

  useEffect(() => {
    if (stoppedRef.current) return

    if (connected) {
      if (intervalRef.current) {
        clearInterval(intervalRef.current)
        intervalRef.current = null
      }
      return
    }

    // Retry every 3 seconds
    intervalRef.current = setInterval(async () => {
      if (stoppedRef.current) return
      try {
        const { port } = await connectToDaemon()
        setConnected(true, port)
        fetchProjects()
      } catch {
        // Still disconnected
      }
    }, 3000)

    return () => {
      if (intervalRef.current) clearInterval(intervalRef.current)
    }
  }, [connected])

  return { wasConnected, stopReconnect }
}
