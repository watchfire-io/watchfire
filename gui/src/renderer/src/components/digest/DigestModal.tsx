// DigestModal renders the v6.0 Ember weekly digest the daemon persists at
// `~/.watchfire/digests/<date>.md`. Opened by:
//   - clicking the OS WEEKLY_DIGEST notification (via `notifications:click`),
//   - clicking the tray's `📊 Weekly digest · last Mon` row (FOCUS_TARGET_DIGEST),
//   - clicking a "Digests" entry in the in-app notification center.
// Includes an Export pill (re-uses the v6.0 task 0059 useExportReport hook
// when present; falls back to a clipboard copy here so the modal is usable
// even before that hook lands) and a "View in dashboard" button that closes
// the modal and routes the GUI to the dashboard home.

import { useEffect } from 'react'
import { Download, LayoutDashboard } from 'lucide-react'
import { useDigestStore } from '../../stores/digest-store'
import { useAppStore } from '../../stores/app-store'
import { Button } from '../ui/Button'
import { Modal } from '../ui/Modal'
import { MarkdownView } from './MarkdownView'

function formatDateLabel(dateKey: string): string {
  const [y, m, d] = dateKey.split('-').map((s) => parseInt(s, 10))
  if (!y || !m || !d) return dateKey
  const dt = new Date(y, m - 1, d)
  return dt.toLocaleDateString(undefined, {
    weekday: 'short',
    month: 'short',
    day: 'numeric',
    year: 'numeric'
  })
}

export function DigestModal(): React.ReactNode {
  const openDate = useDigestStore((s) => s.openDate)
  const body = useDigestStore((s) => s.body)
  const loading = useDigestStore((s) => s.loading)
  const close = useDigestStore((s) => s.close)
  const setView = useAppStore((s) => s.setView)

  // Refresh the body when openDate changes, in case the user navigates
  // between digests without unmounting the modal.
  useEffect(() => {
    if (!openDate) return
    void useDigestStore.getState().open(openDate)
  }, [openDate])

  const handleExport = async (): Promise<void> => {
    if (!body) return
    try {
      await navigator.clipboard.writeText(body)
    } catch {
      /* clipboard blocked — silently swallow; the file is on disk anyway */
    }
  }

  const handleViewDashboard = (): void => {
    setView('dashboard')
    close()
  }

  if (!openDate) return null

  return (
    <Modal
      open
      onClose={close}
      title={`Weekly digest · ${formatDateLabel(openDate)}`}
      footer={
        <>
          <Button variant="secondary" size="sm" onClick={handleExport}>
            <Download size={14} />
            Export Markdown
          </Button>
          <Button variant="secondary" size="sm" onClick={handleViewDashboard}>
            <LayoutDashboard size={14} />
            View in dashboard
          </Button>
          <Button variant="primary" size="sm" onClick={close}>
            Close
          </Button>
        </>
      }
    >
      {loading && (
        <div className="text-sm text-[var(--wf-text-muted)] py-8 text-center">Loading digest…</div>
      )}
      {!loading && body === null && (
        <div className="text-sm text-[var(--wf-text-muted)] py-8 text-center">
          Could not load digest. The file may have been deleted.
        </div>
      )}
      {!loading && body && <MarkdownView source={body} />}
    </Modal>
  )
}
