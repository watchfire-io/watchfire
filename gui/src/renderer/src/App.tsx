import { useEffect, useCallback, useState } from 'react'
import { useAutoReconnect } from './hooks/useAutoReconnect'
import { useAppStore } from './stores/app-store'
import { Sidebar } from './components/Sidebar'
import { Dashboard } from './views/Dashboard/Dashboard'
import { AddProjectWizard } from './views/AddProject/AddProjectWizard'
import { ProjectView } from './views/ProjectView/ProjectView'
import { GlobalSettings } from './views/Settings/GlobalSettings'

export default function App() {
  const { wasConnected, stopReconnect } = useAutoReconnect()
  const view = useAppStore((s) => s.view)
  const connected = useAppStore((s) => s.connected)
  const setView = useAppStore((s) => s.setView)
  const [daemonShutdown, setDaemonShutdown] = useState(false)

  // Listen for daemon shutdown from main process
  useEffect(() => {
    window.watchfire.onDaemonShutdown(() => {
      setDaemonShutdown(true)
      stopReconnect()
    })
  }, [stopReconnect])

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
        ) : !connected && wasConnected ? (
          <div className="flex-1 flex items-center justify-center">
            <div className="text-center">
              <div className="text-4xl mb-4">&#x26A1;</div>
              <h2 className="text-xl font-semibold mb-2">Daemon Disconnected</h2>
              <p className="text-[var(--wf-text-secondary)] mb-4">
                Lost connection to the Watchfire daemon. Attempting to reconnect...
              </p>
              <div className="w-6 h-6 border-2 border-[var(--wf-fire)] border-t-transparent rounded-full animate-spin mx-auto" />
            </div>
          </div>
        ) : (
          <>
            {view === 'dashboard' && <Dashboard />}
            {view === 'add-project' && <AddProjectWizard />}
            {view === 'project' && <ProjectView />}
            {view === 'settings' && <GlobalSettings />}
          </>
        )}
      </main>
    </div>
  )
}
