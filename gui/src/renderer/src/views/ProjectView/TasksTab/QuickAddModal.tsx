import { useEffect, useState } from 'react'
import { SlidePanel } from '../../../components/ui/SlidePanel'
import { Button } from '../../../components/ui/Button'
import { useTasksStore } from '../../../stores/tasks-store'
import { useToast } from '../../../components/ui/Toast'
import { countQuickAddTasks } from '../../../lib/quickadd'
import { formatTaskNumber } from '../../../lib/utils'

interface Props {
  open: boolean
  onClose: () => void
  projectId: string
}

const PLACEHOLDER = `- One task per top-level bullet — plain language is fine
- Nested bullets fold into the task above
  - like this detail line
  - AC: lines starting with "AC:" become acceptance criteria
- Numbered lists (1. / 1)) and * bullets work too

No bullets at all? The whole text becomes a single task.`

/**
 * Quick-add modal (v10 Torch, GitHub issue #19): one textarea, a draft/ready
 * toggle, zero per-task form friction. The daemon's shared parser splits the
 * text into tasks server-side (TaskService.CreateTasksBatch); the count shown
 * here is a client-side preview of the same block-splitting rule.
 */
export function QuickAddModal({ open, onClose, projectId }: Props) {
  const createTasksBatch = useTasksStore((s) => s.createTasksBatch)
  const { toast } = useToast()

  const [text, setText] = useState('')
  const [status, setStatus] = useState<'draft' | 'ready'>('ready')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (!open) {
      setText('')
      setStatus('ready')
    }
  }, [open])

  const count = countQuickAddTasks(text)

  const handleCreate = async () => {
    if (count === 0) return
    setSaving(true)
    try {
      const created = await createTasksBatch(projectId, text, status)
      const numbers = created.map((t) => formatTaskNumber(t.taskNumber)).join(' ')
      toast(`Created ${created.length} task${created.length === 1 ? '' : 's'}: ${numbers}`, 'success')
      onClose()
    } catch (err) {
      toast(String(err), 'error')
    } finally {
      setSaving(false)
    }
  }

  return (
    <SlidePanel
      open={open}
      onClose={onClose}
      title="Quick Add Tasks"
      footer={
        <>
          <span className="text-xs text-[var(--wf-text-muted)] mr-auto">
            {count === 0
              ? 'Nothing to create yet'
              : `Will create ${count} task${count === 1 ? '' : 's'}`}
          </span>
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button onClick={handleCreate} disabled={saving || count === 0}>
            {saving ? 'Creating...' : count > 1 ? `Create ${count} Tasks` : 'Create Task'}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4 h-full">
        <textarea
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={PLACEHOLDER}
          autoFocus
          spellCheck={false}
          className="flex-1 min-h-[280px] w-full resize-none p-3 text-sm font-mono rounded-[var(--wf-radius-md)] bg-[var(--wf-bg-primary)] border border-[var(--wf-border)] text-[var(--wf-text-primary)] placeholder-[var(--wf-text-muted)] focus:outline-none focus:border-fire-500 transition-colors"
          aria-label="Quick add task list"
        />

        <div>
          <label className="block text-sm font-medium text-[var(--wf-text-secondary)] mb-1.5">
            Create as
          </label>
          <div className="flex gap-3">
            {(['ready', 'draft'] as const).map((s) => (
              <label key={s} className="flex items-center gap-2 cursor-pointer">
                <input
                  type="radio"
                  name="quick-add-status"
                  checked={status === s}
                  onChange={() => setStatus(s)}
                  className="accent-fire-500"
                />
                <span className="text-sm">{s === 'draft' ? 'Todo (Draft)' : 'Ready (In Dev)'}</span>
              </label>
            ))}
          </div>
        </div>
      </div>
    </SlidePanel>
  )
}
