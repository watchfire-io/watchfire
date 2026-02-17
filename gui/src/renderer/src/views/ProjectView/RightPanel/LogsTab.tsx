import { useEffect, useState } from 'react'
import { ScrollText, ArrowLeft, RefreshCw } from 'lucide-react'
import { useLogsStore } from '../../../stores/logs-store'
import { Button } from '../../../components/ui/Button'
import { Badge } from '../../../components/ui/Badge'
import { formatTaskNumber } from '../../../lib/utils'

const EMPTY: never[] = []

interface Props {
  projectId: string
}

export function LogsTab({ projectId }: Props) {
  const logs = useLogsStore((s) => s.logs[projectId] ?? EMPTY)
  const loading = useLogsStore((s) => s.loading)
  const fetchLogs = useLogsStore((s) => s.fetchLogs)
  const getLogContent = useLogsStore((s) => s.getLogContent)

  const [selectedLogId, setSelectedLogId] = useState<string | null>(null)
  const [content, setContent] = useState<string | null>(null)
  const [loadingContent, setLoadingContent] = useState(false)

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

  // Detail view
  if (selectedLogId && content !== null) {
    const log = logs.find((l) => l.logId === selectedLogId)
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
        <p className="text-sm">No session logs</p>
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
        {logs.map((log) => (
          <button
            key={log.logId}
            onClick={() => handleSelectLog(log.logId)}
            className="flex items-center gap-2 w-full px-3 py-2 rounded-[var(--wf-radius-md)] hover:bg-[var(--wf-bg-elevated)] transition-colors text-left"
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
        ))}
      </div>
    </div>
  )
}
