import { useState, useEffect } from 'react'
import { ScanSearch, Pencil } from 'lucide-react'
import { SlidePanel } from '../../../components/ui/SlidePanel'
import { Button } from '../../../components/ui/Button'
import { Input } from '../../../components/ui/Input'
import { Select, type SelectOption } from '../../../components/ui/Select'
import { useTasksStore } from '../../../stores/tasks-store'
import { useProjectsStore } from '../../../stores/projects-store'
import { useAgentsStore } from '../../../stores/agents-store'
import { useToast } from '../../../components/ui/Toast'
import { InspectTab } from '../../../components/task/InspectTab'
import { cn } from '../../../lib/utils'
import type { Task } from '../../../generated/watchfire_pb'
import { formatTaskNumber } from '../../../lib/utils'

interface Props {
  open: boolean
  onClose: () => void
  projectId: string
  task?: Task
}

type DetailTab = 'edit' | 'inspect'

/**
 * Picks the tab that should be active when the modal first commits.
 * Done tasks land directly on Inspect — that's where the diff and the
 * red failure banner live, and it's why someone reopens a finished
 * task. Anything else opens on the Edit form.
 *
 * Used both as the lazy initializer for the `tab` state (so the very
 * first paint is correct — no form-tab flicker before the effect
 * re-syncs) and from the effect that handles modal re-opens.
 */
export function initialDetailTab(task?: Task): DetailTab {
  return task?.status === 'done' ? 'inspect' : 'edit'
}

export function TaskModal({ open, onClose, projectId, task }: Props) {
  const createTask = useTasksStore((s) => s.createTask)
  const updateTask = useTasksStore((s) => s.updateTask)
  const project = useProjectsStore((s) =>
    s.projects.find((p) => p.projectId === projectId)
  )
  const agents = useAgentsStore((s) => s.agents)
  const agentsLoaded = useAgentsStore((s) => s.loaded)
  const ensureAgentsLoaded = useAgentsStore((s) => s.ensureLoaded)
  const { toast } = useToast()

  const [title, setTitle] = useState('')
  const [prompt, setPrompt] = useState('')
  const [criteria, setCriteria] = useState('')
  const [status, setStatus] = useState<'draft' | 'ready'>('draft')
  const [agent, setAgent] = useState('')
  const [saving, setSaving] = useState(false)
  const [tab, setTab] = useState<DetailTab>(() => initialDetailTab(task))

  const isEdit = !!task
  const isDone = task?.status === 'done'

  useEffect(() => {
    if (open) ensureAgentsLoaded()
  }, [open, ensureAgentsLoaded])

  useEffect(() => {
    if (open && task) {
      setTitle(task.title)
      setPrompt(task.prompt)
      setCriteria(task.acceptanceCriteria)
      setStatus(task.status === 'ready' ? 'ready' : 'draft')
      setAgent(task.agent ?? '')
      setTab(initialDetailTab(task))
    } else if (!open) {
      setTitle('')
      setPrompt('')
      setCriteria('')
      setStatus('draft')
      setAgent('')
      setTab('edit')
    }
  }, [task?.taskNumber, open])

  const handleSave = async () => {
    if (!title.trim()) return
    setSaving(true)
    try {
      if (isEdit) {
        await updateTask(projectId, task.taskNumber, {
          title: title.trim(),
          prompt: prompt.trim(),
          acceptanceCriteria: criteria.trim(),
          status,
          agent
        })
        toast('Task updated', 'success')
      } else {
        await createTask(projectId, title.trim(), prompt.trim(), {
          acceptanceCriteria: criteria.trim(),
          status,
          agent
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

  const projectDefault = (project?.defaultAgent ?? '').trim()
  const projectDefaultAgent = agents.find((a) => a.name === projectDefault)
  const projectDefaultLabel = projectDefaultAgent?.displayName || projectDefault || 'unspecified'
  const agentOptions: SelectOption[] = [
    { value: '', label: `Project default (${projectDefaultLabel})` },
    ...agents.map((a) => ({ value: a.name, label: a.displayName }))
  ]

  // Wider panel when showing the diff — file-list + side-by-side body
  // doesn't breathe at 560px.
  const widthClass = isEdit && isDone && tab === 'inspect' ? 'w-[1100px]' : 'w-[560px]'

  return (
    <SlidePanel
      open={open}
      onClose={onClose}
      widthClass={widthClass}
      title={isEdit ? `${formatTaskNumber(task.taskNumber)} · ${task.title}` : 'New Task'}
      bodyPadding={isEdit && isDone && tab === 'inspect' ? 'none' : 'default'}
      headerSlot={
        isEdit && isDone ? (
          <div className="flex items-center gap-1 ml-3">
            <DetailTabButton
              active={tab === 'edit'}
              onClick={() => setTab('edit')}
              icon={<Pencil size={12} />}
              label="Edit"
            />
            <DetailTabButton
              active={tab === 'inspect'}
              onClick={() => setTab('inspect')}
              icon={<ScanSearch size={12} />}
              label="Inspect"
            />
          </div>
        ) : null
      }
      footer={
        tab === 'edit' ? (
          <>
            <Button variant="ghost" onClick={onClose}>Cancel</Button>
            <Button onClick={handleSave} disabled={saving || !title.trim()}>
              {saving ? 'Saving...' : isEdit ? 'Update' : 'Create'}
            </Button>
          </>
        ) : null
      }
    >
      {tab === 'inspect' && task ? (
        <>
          {(task.mergeFailureReason ?? '') !== '' && (
            <div className="mx-4 mt-4 mb-2 rounded-[var(--wf-radius-md)] border border-red-500/30 bg-red-900/20 p-3 text-sm">
              <div className="font-medium text-red-300">Auto-merge failed</div>
              <div className="mt-1 font-mono text-xs text-red-200">
                {task.mergeFailureReason}
              </div>
              <div className="mt-2 text-xs text-[var(--wf-text-muted)]">
                The agent finished cleanly; only the merge into the default
                branch failed. The worktree is still on disk so you can
                resolve the conflict and merge by hand.
              </div>
            </div>
          )}
          {(task.failureReason ?? '') !== '' && (
            <div className="mx-4 mt-4 mb-2 rounded-[var(--wf-radius-md)] border border-red-500/30 bg-red-900/20 p-3 text-sm">
              <div className="font-medium text-red-300">Task failed</div>
              <div className="mt-1 text-xs text-[var(--wf-text-secondary)]">
                {task.failureReason}
              </div>
            </div>
          )}
          <InspectTab projectId={projectId} taskNumber={task.taskNumber} />
        </>
      ) : (
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
              rows={8}
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
              rows={6}
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

          <Select
            label="Agent"
            value={agent}
            options={agentOptions}
            onChange={setAgent}
            disabled={!agentsLoaded}
          />
        </div>
      )}
    </SlidePanel>
  )
}

function DetailTabButton({
  active,
  onClick,
  icon,
  label
}: {
  active: boolean
  onClick: () => void
  icon: React.ReactNode
  label: string
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        'flex items-center gap-1.5 px-2.5 py-1 text-xs rounded-[var(--wf-radius-sm)] transition-colors',
        active
          ? 'bg-[var(--wf-bg-elevated)] text-[var(--wf-text-primary)]'
          : 'text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)]'
      )}
    >
      {icon}
      {label}
    </button>
  )
}
