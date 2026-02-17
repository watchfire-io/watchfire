import { useEffect } from 'react'
import { GitMerge, Trash2, Scissors, RefreshCw } from 'lucide-react'
import { useBranchesStore } from '../../../stores/branches-store'
import { Button } from '../../../components/ui/Button'
import { Badge } from '../../../components/ui/Badge'
import { formatTaskNumber } from '../../../lib/utils'
import { useToast } from '../../../components/ui/Toast'

const EMPTY: never[] = []

interface Props {
  projectId: string
}

export function BranchesTab({ projectId }: Props) {
  const branches = useBranchesStore((s) => s.branches[projectId] ?? EMPTY)
  const loading = useBranchesStore((s) => s.loading)
  const fetchBranches = useBranchesStore((s) => s.fetchBranches)
  const mergeBranch = useBranchesStore((s) => s.mergeBranch)
  const deleteBranch = useBranchesStore((s) => s.deleteBranch)
  const pruneBranches = useBranchesStore((s) => s.pruneBranches)
  const { toast } = useToast()

  useEffect(() => {
    fetchBranches(projectId)
  }, [projectId])

  const handleMerge = async (name: string) => {
    try {
      await mergeBranch(projectId, name, true)
      toast('Branch merged', 'success')
    } catch (err) {
      toast(String(err), 'error')
    }
  }

  const handleDelete = async (name: string) => {
    try {
      await deleteBranch(projectId, name)
      toast('Branch deleted', 'success')
    } catch (err) {
      toast(String(err), 'error')
    }
  }

  if (branches.length === 0 && !loading) {
    return (
      <div className="flex flex-col items-center justify-center h-full text-[var(--wf-text-muted)]">
        <p className="text-sm">No branches</p>
      </div>
    )
  }

  const statusVariant = (s: string) =>
    s === 'merged' ? 'success' as const : s === 'orphaned' ? 'warning' as const : 'default' as const

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center justify-between px-3 py-2 border-b border-[var(--wf-border)]">
        <span className="text-xs text-[var(--wf-text-muted)]">{branches.length} branches</span>
        <div className="flex gap-1">
          <Button size="sm" variant="ghost" onClick={() => fetchBranches(projectId)}>
            <RefreshCw size={12} />
          </Button>
          <Button size="sm" variant="ghost" onClick={() => pruneBranches(projectId)}>
            <Scissors size={12} />
            Prune
          </Button>
        </div>
      </div>
      <div className="flex-1 overflow-y-auto p-2 space-y-1">
        {branches.map((b) => (
          <div
            key={b.name}
            className="flex items-center gap-2 px-3 py-2 rounded-[var(--wf-radius-md)] hover:bg-[var(--wf-bg-elevated)] transition-colors"
          >
            <div className="flex-1 min-w-0">
              <div className="text-sm font-mono truncate">{b.name}</div>
              {b.taskNumber > 0 && (
                <div className="text-xs text-[var(--wf-text-muted)]">
                  Task {formatTaskNumber(b.taskNumber)}
                </div>
              )}
            </div>
            <Badge variant={statusVariant(b.status)}>{b.status}</Badge>
            {b.status === 'unmerged' && (
              <button
                onClick={() => handleMerge(b.name)}
                title="Merge"
                className="p-1 text-[var(--wf-text-muted)] hover:text-[var(--wf-success)] transition-colors"
              >
                <GitMerge size={14} />
              </button>
            )}
            <button
              onClick={() => handleDelete(b.name)}
              title="Delete"
              className="p-1 text-[var(--wf-text-muted)] hover:text-[var(--wf-error)] transition-colors"
            >
              <Trash2 size={14} />
            </button>
          </div>
        ))}
      </div>
    </div>
  )
}
