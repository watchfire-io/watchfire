import { useEffect, useState } from 'react'
import { ScrollText, ArrowLeft, RefreshCw, Trash2, Loader2 } from 'lucide-react'
import { useLogsStore } from '../../../stores/logs-store'
import { Button } from '../../../components/ui/Button'
import { Badge } from '../../../components/ui/Badge'
import { useToast } from '../../../components/ui/Toast'
import { formatTaskNumber } from '../../../lib/utils'

const EMPTY: never[] = []

interface Props {
  projectId: string
}

export function LogsTab({ projectId }: Props) {
  const logs = useLogsStore((s) => s.logs[projectId] ?? EMPTY)
  const loading = useLogsStore((s) => s.loading)
  const error = useLogsStore((s) => s.error[projectId] ?? null)
  const fetchLogs = useLogsStore((s) => s.fetchLogs)
  const getLogContent = useLogsStore((s) => s.getLogContent)
  const deleteLog = useLogsStore((s) => s.deleteLog)
  const { toast } = useToast()

  const [selectedLogId, setSelectedLogId] = useState<string | null>(null)
  const [content, setContent] = useState<string | null>(null)
  const [loadingContent, setLoadingContent] = useState(false)
  const [deletingId, setDeletingId] = useState<string | null>(null)

  useEffect(() => {
    fetchLogs(projectId)
  }, [projectId])

  const handleSelectLog = async (logId: string) => {
    setSelectedLogId(logId)
    setLoadingContent(true)
    try {
      const c = await getLogContent(projectId, logId)
      setContent(c)
    } catch {
      setContent('Failed to load log content')
    } finally {
      setLoadingContent(false)
    }
  }

  const handleDelete = async (logId: string, navigateBack: boolean) => {
    if (!window.confirm('Delete this session log? This cannot be undone.')) return
    setDeletingId(logId)
    try {
      await deleteLog(projectId, logId)
      toast('Log deleted', 'success')
      if (navigateBack || selectedLogId === logId) {
        setSelectedLogId(null)
        setContent(null)
      }
    } catch (err) {
      toast(`Failed to delete log: ${String(err)}`, 'error')
    } finally {
      setDeletingId(null)
    }
  }

  // Detail view
  if (selectedLogId && content !== null) {
    const log = logs.find((l) => l.logId === selectedLogId)
    const isDeleting = deletingId === selectedLogId
    return (
      <div className="flex flex-col h-full">
        <div className="flex items-center gap-2 px-3 py-2 border-b border-[var(--wf-border)]">
          <button
            onClick={() => { setSelectedLogId(null); setContent(null) }}
            className="text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)] transition-colors"
          >
            <ArrowLeft size={16} />
          </button>
          <div className="flex-1 min-w-0">
            <div className="text-xs font-medium truncate">
              {log?.taskNumber ? `Task ${formatTaskNumber(log.taskNumber)}` : 'Chat'}
              {log?.sessionNumber ? ` — Session ${log.sessionNumber}` : ''}
            </div>
            <div className="text-[10px] text-[var(--wf-text-muted)]">{log?.startedAt}</div>
          </div>
          <button
            onClick={() => handleDelete(selectedLogId, true)}
            disabled={isDeleting}
            title="Delete log"
            className="p-1 text-[var(--wf-text-muted)] hover:text-[var(--wf-error)] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {isDeleting ? <Loader2 size={14} className="animate-spin" /> : <Trash2 size={14} />}
          </button>
        </div>
        <pre className="flex-1 overflow-auto p-3 text-xs font-mono text-[var(--wf-text-secondary)] whitespace-pre-wrap">
          {loadingContent ? 'Loading...' : content}
        </pre>
      </div>
    )
  }

  // List view
  if (logs.length === 0 && !loading) {
    return (
      <div className="flex flex-col items-center justify-center h-full text-[var(--wf-text-muted)]">
        <ScrollText size={32} className="mb-3 opacity-30" />
        {error ? (
          <>
            <p className="text-sm text-[var(--wf-text-danger)]">Failed to load logs</p>
            <p className="text-[10px] mt-1 max-w-[240px] text-center break-words">{error}</p>
            <Button size="sm" variant="ghost" className="mt-2" onClick={() => fetchLogs(projectId)}>
              <RefreshCw size={12} className="mr-1" /> Retry
            </Button>
          </>
        ) : (
          <p className="text-sm">No session logs</p>
        )}
      </div>
    )
  }

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center justify-between px-3 py-2 border-b border-[var(--wf-border)]">
        <span className="text-xs text-[var(--wf-text-muted)]">{logs.length} logs</span>
        <Button size="sm" variant="ghost" onClick={() => fetchLogs(projectId)}>
          <RefreshCw size={12} />
        </Button>
      </div>
      <div className="flex-1 overflow-y-auto p-2 space-y-1">
        {logs.map((log) => {
          const isDeleting = deletingId === log.logId
          return (
            <div
              key={log.logId}
              className="group flex items-center gap-2 w-full px-3 py-2 rounded-[var(--wf-radius-md)] hover:bg-[var(--wf-bg-elevated)] transition-colors"
            >
              <button
                onClick={() => handleSelectLog(log.logId)}
                className="flex items-center gap-2 flex-1 min-w-0 text-left"
              >
                <div className="flex-1 min-w-0">
                  <div className="text-sm truncate">
                    {log.taskNumber ? `Task ${formatTaskNumber(log.taskNumber)}` : 'Chat'}
                    {log.sessionNumber ? ` — Session ${log.sessionNumber}` : ''}
                  </div>
                  <div className="text-[10px] text-[var(--wf-text-muted)]">
                    {log.startedAt} — {log.mode}
                  </div>
                </div>
                <Badge variant={log.status === 'done' ? 'success' : 'default'}>
                  {log.status || 'ended'}
                </Badge>
              </button>
              <button
                onClick={(e) => { e.stopPropagation(); handleDelete(log.logId, false) }}
                disabled={isDeleting}
                title="Delete log"
                className="p-1 text-[var(--wf-text-muted)] hover:text-[var(--wf-error)] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              >
                {isDeleting ? <Loader2 size={12} className="animate-spin" /> : <Trash2 size={12} />}
              </button>
            </div>
          )
        })}
      </div>
    </div>
  )
}
