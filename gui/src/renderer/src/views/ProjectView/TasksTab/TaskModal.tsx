import { useState, useEffect } from 'react'
import { Modal } from '../../../components/ui/Modal'
import { Button } from '../../../components/ui/Button'
import { Input } from '../../../components/ui/Input'
import { useTasksStore } from '../../../stores/tasks-store'
import { useToast } from '../../../components/ui/Toast'
import type { Task } from '../../../generated/watchfire_pb'
import { formatTaskNumber } from '../../../lib/utils'

interface Props {
  open: boolean
  onClose: () => void
  projectId: string
  task?: Task
}

export function TaskModal({ open, onClose, projectId, task }: Props) {
  const createTask = useTasksStore((s) => s.createTask)
  const updateTask = useTasksStore((s) => s.updateTask)
  const { toast } = useToast()

  const [title, setTitle] = useState('')
  const [prompt, setPrompt] = useState('')
  const [criteria, setCriteria] = useState('')
  const [status, setStatus] = useState<'draft' | 'ready'>('draft')
  const [saving, setSaving] = useState(false)

  const isEdit = !!task

  useEffect(() => {
    if (task) {
      setTitle(task.title)
      setPrompt(task.prompt)
      setCriteria(task.acceptanceCriteria)
      setStatus(task.status === 'ready' ? 'ready' : 'draft')
    } else {
      setTitle('')
      setPrompt('')
      setCriteria('')
      setStatus('draft')
    }
  }, [task, open])

  const handleSave = async () => {
    if (!title.trim()) return
    setSaving(true)
    try {
      if (isEdit) {
        await updateTask(projectId, task.taskNumber, {
          title: title.trim(),
          prompt: prompt.trim(),
          acceptanceCriteria: criteria.trim(),
          status
        })
        toast('Task updated', 'success')
      } else {
        await createTask(projectId, title.trim(), prompt.trim(), {
          acceptanceCriteria: criteria.trim(),
          status
        })
        toast('Task created', 'success')
      }
      onClose()
    } catch (err) {
      toast(String(err), 'error')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={isEdit ? `Edit ${formatTaskNumber(task.taskNumber)}` : 'New Task'}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button onClick={handleSave} disabled={saving || !title.trim()}>
            {saving ? 'Saving...' : isEdit ? 'Update' : 'Create'}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Input
          label="Title"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="What needs to be done?"
          autoFocus
        />

        <div>
          <label className="block text-sm font-medium text-[var(--wf-text-secondary)] mb-1.5">
            Prompt
          </label>
          <textarea
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            placeholder="Detailed instructions for the AI agent..."
            rows={4}
            className="w-full px-3 py-2 rounded-[var(--wf-radius-md)] bg-[var(--wf-bg-primary)] border border-[var(--wf-border)] text-sm font-mono text-[var(--wf-text-primary)] placeholder-[var(--wf-text-muted)] focus:outline-none focus:border-fire-500 focus:ring-1 focus:ring-fire-500/30 transition-colors resize-none"
          />
        </div>

        <div>
          <label className="block text-sm font-medium text-[var(--wf-text-secondary)] mb-1.5">
            Acceptance Criteria
          </label>
          <textarea
            value={criteria}
            onChange={(e) => setCriteria(e.target.value)}
            placeholder="How will we know this task is done?"
            rows={3}
            className="w-full px-3 py-2 rounded-[var(--wf-radius-md)] bg-[var(--wf-bg-primary)] border border-[var(--wf-border)] text-sm font-mono text-[var(--wf-text-primary)] placeholder-[var(--wf-text-muted)] focus:outline-none focus:border-fire-500 focus:ring-1 focus:ring-fire-500/30 transition-colors resize-none"
          />
        </div>

        <div>
          <label className="block text-sm font-medium text-[var(--wf-text-secondary)] mb-1.5">
            Status
          </label>
          <div className="flex gap-3">
            {(['draft', 'ready'] as const).map((s) => (
              <label key={s} className="flex items-center gap-2 cursor-pointer">
                <input
                  type="radio"
                  name="status"
                  checked={status === s}
                  onChange={() => setStatus(s)}
                  className="accent-fire-500"
                />
                <span className="text-sm capitalize">{s === 'draft' ? 'Todo (Draft)' : 'Ready (In Dev)'}</span>
              </label>
            ))}
          </div>
        </div>
      </div>
    </Modal>
  )
}
