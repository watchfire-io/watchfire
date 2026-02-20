import { useEffect, useState } from 'react'
import { Download, RefreshCw, X } from 'lucide-react'

type UpdateState =
  | { status: 'idle' }
  | { status: 'available'; version: string; releaseNotes?: string }
  | { status: 'downloading'; percent: number }
  | { status: 'ready' }
  | { status: 'error'; message: string }

export function UpdateBanner() {
  const [state, setState] = useState<UpdateState>({ status: 'idle' })
  const [dismissed, setDismissed] = useState(false)

  useEffect(() => {
    window.watchfire.onUpdateAvailable((info) => {
      setState({ status: 'available', version: info.version, releaseNotes: info.releaseNotes })
      setDismissed(false)
    })

    window.watchfire.onUpdateProgress((percent) => {
      setState({ status: 'downloading', percent })
    })

    window.watchfire.onUpdateReady(() => {
      setState({ status: 'ready' })
    })

    window.watchfire.onUpdateError((message) => {
      // Silently ignore "no release found" / 404 errors — this just means
      // no GUI update assets have been uploaded to the release yet
      if (message.includes('404') || message.includes('no published release')) return
      setState({ status: 'error', message })
    })
  }, [])

  if (state.status === 'idle' || dismissed) return null

  return (
    <div className="mx-4 mb-2 px-4 py-2.5 rounded-[var(--wf-radius-lg)] bg-fire-500/10 border border-fire-500/30 flex items-center gap-3 text-sm">
      {state.status === 'available' && (
        <>
          <div className="flex-1">
            <span className="font-medium text-fire-400">v{state.version}</span>
            <span className="text-[var(--wf-text-secondary)] ml-2">is available</span>
          </div>
          <button
            onClick={() => window.watchfire.downloadUpdate()}
            className="flex items-center gap-1.5 px-3 py-1 rounded-[var(--wf-radius-md)] bg-fire-500 text-white text-xs font-medium hover:bg-fire-600 transition-colors"
          >
            <Download size={12} />
            Download
          </button>
          <button onClick={() => setDismissed(true)} className="text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)]">
            <X size={14} />
          </button>
        </>
      )}

      {state.status === 'downloading' && (
        <div className="flex-1 flex items-center gap-3">
          <span className="text-[var(--wf-text-secondary)]">Downloading update...</span>
          <div className="flex-1 max-w-48 h-1.5 bg-[var(--wf-bg-tertiary)] rounded-full overflow-hidden">
            <div
              className="h-full bg-fire-500 rounded-full transition-all duration-300"
              style={{ width: `${state.percent}%` }}
            />
          </div>
          <span className="text-xs text-[var(--wf-text-muted)]">{Math.round(state.percent)}%</span>
        </div>
      )}

      {state.status === 'ready' && (
        <>
          <div className="flex-1 text-[var(--wf-text-secondary)]">
            Update downloaded and ready to install
          </div>
          <button
            onClick={() => window.watchfire.installUpdate()}
            className="flex items-center gap-1.5 px-3 py-1 rounded-[var(--wf-radius-md)] bg-fire-500 text-white text-xs font-medium hover:bg-fire-600 transition-colors"
          >
            <RefreshCw size={12} />
            Restart & Update
          </button>
        </>
      )}

      {state.status === 'error' && (
        <>
          <div className="flex-1 text-[var(--wf-text-secondary)] truncate">
            {state.message.includes('net::')
              ? 'Update check failed: no internet connection'
              : 'Update check failed'}
          </div>
          <button onClick={() => setDismissed(true)} className="text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)]">
            <X size={14} />
          </button>
        </>
      )}
    </div>
  )
}
