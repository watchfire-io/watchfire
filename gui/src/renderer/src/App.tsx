import { useEffect, useCallback, useState } from 'react'
import { useAutoReconnect } from './hooks/useAutoReconnect'
import { useAppStore } from './stores/app-store'
import { useFocusStore } from './stores/focus-store'
// Touching the notifications store at app load preloads the two notification
// sounds so the first TASK_FAILED / RUN_COMPLETE play() doesn't pay the
// disk-fetch latency. The store exposes `notify(kind, ...)`; the gRPC stream
// subscriber from task 0049 is the call site.
import { useNotificationsStore } from './stores/notifications-store'
import { Sidebar } from './components/Sidebar'
import { Dashboard } from './views/Dashboard/Dashboard'
import { AddProjectWizard } from './views/AddProject/AddProjectWizard'
import { ProjectView } from './views/ProjectView/ProjectView'
import { GlobalSettings } from './views/Settings/GlobalSettings'
import { UpdateBanner } from './components/UpdateBanner'

// Eagerly read the store so its factory runs at app load — that's where the
// two notification-sound Audio elements get preloaded so the first play()
// from task 0049's gRPC stream subscriber lands without latency.
useNotificationsStore.getState()

export default function App() {
  const { wasConnected, stopReconnect } = useAutoReconnect()
  const view = useAppStore((s) => s.view)
  const connected = useAppStore((s) => s.connected)
  const setView = useAppStore((s) => s.setView)
  const startFocus = useFocusStore((s) => s.start)
  const stopFocus = useFocusStore((s) => s.stop)
  const startNotifications = useNotificationsStore((s) => s.start)
  const stopNotifications = useNotificationsStore((s) => s.stop)
  const requestFocus = useAppStore((s) => s.requestFocus)
  const [daemonShutdown, setDaemonShutdown] = useState(false)

  // Listen for daemon shutdown from main process
  useEffect(() => {
    window.watchfire.onDaemonShutdown(() => {
      setDaemonShutdown(true)
      stopReconnect()
      stopFocus()
      stopNotifications()
    })
  }, [stopReconnect, stopFocus, stopNotifications])

  // When the user clicks an OS notification, route to the failing project's
  // TasksTab. Main process focuses the window first, so this just handles
  // the in-renderer routing.
  useEffect(() => {
    window.watchfire.onNotificationClick(({ projectId, taskNumber }) => {
      if (!projectId) return
      requestFocus({
        projectId,
        target: 'tasks',
        taskNumber: taskNumber || undefined
      })
    })
  }, [requestFocus])

  // Open the tray-driven focus stream when the daemon connects so a click
  // in the menu bar can route the GUI to a specific project / tab. The
  // stream auto-reconnects on transient errors via the focus store.
  useEffect(() => {
    if (!connected) return
    startFocus()
    startNotifications()
    return () => {
      stopFocus()
      stopNotifications()
    }
  }, [connected, startFocus, stopFocus, startNotifications, stopNotifications])

  // Global keyboard shortcuts
  const handleKeyDown = useCallback(
    (e: KeyboardEvent) => {
      const meta = e.metaKey || e.ctrlKey

      // Cmd+, → Settings
      if (meta && e.key === ',') {
        e.preventDefault()
        setView('settings')
      }
    },
    [setView]
  )

  useEffect(() => {
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [handleKeyDown])

  return (
    <div className="flex h-screen bg-[var(--wf-bg-primary)] text-[var(--wf-text-primary)]">
      <Sidebar />
      <main className="flex-1 flex flex-col overflow-hidden relative">
        {/* macOS title bar drag area */}
        <div className="titlebar-drag h-8 shrink-0" />
        <UpdateBanner />

        {/* Always render views so they stay mounted during disconnects */}
        {daemonShutdown ? (
          <div className="flex-1 flex items-center justify-center">
            <div className="text-center">
              <div className="w-12 h-12 rounded-full bg-[var(--wf-error)]/10 flex items-center justify-center mx-auto mb-4">
                <span className="text-2xl">&#x26A0;</span>
              </div>
              <h2 className="text-xl font-semibold mb-2">Daemon Has Shut Down</h2>
              <p className="text-[var(--wf-text-secondary)] mb-4">
                The Watchfire daemon process has exited. This window will close automatically.
              </p>
            </div>
          </div>
        ) : !connected && !wasConnected ? (
          <div className="flex-1 flex items-center justify-center">
            <div className="text-center">
              <div className="w-8 h-8 border-2 border-[var(--wf-fire)] border-t-transparent rounded-full animate-spin mx-auto mb-4" />
              <p className="text-[var(--wf-text-secondary)]">Connecting to daemon...</p>
            </div>
          </div>
        ) : (
          <>
            {view === 'dashboard' && <Dashboard />}
            {view === 'add-project' && <AddProjectWizard />}
            {view === 'project' && <ProjectView />}
            {view === 'settings' && <GlobalSettings />}

            {/* Overlay for reconnecting state — views stay visible underneath */}
            {!connected && wasConnected && (
              <div className="absolute inset-0 bg-[var(--wf-bg-primary)]/80 backdrop-blur-sm flex items-center justify-center z-50">
                <div className="text-center">
                  <div className="text-4xl mb-4">&#x26A1;</div>
                  <h2 className="text-xl font-semibold mb-2">Daemon Disconnected</h2>
                  <p className="text-[var(--wf-text-secondary)] mb-4">
                    Lost connection to the Watchfire daemon. Attempting to reconnect...
                  </p>
                  <div className="w-6 h-6 border-2 border-[var(--wf-fire)] border-t-transparent rounded-full animate-spin mx-auto" />
                </div>
              </div>
            )}
          </>
        )}
      </main>
    </div>
  )
}
