import { useState, useEffect, useRef, useCallback } from 'react'
import type { Project } from '../../generated/watchfire_pb'
import { getProjectClient } from '../../lib/grpc-client'
import { Input } from '../../components/ui/Input'
import { Toggle } from '../../components/ui/Toggle'
import { Modal } from '../../components/ui/Modal'
import { useToast } from '../../components/ui/Toast'
import { useProjectsStore } from '../../stores/projects-store'
import { useAppStore } from '../../stores/app-store'

const PROJECT_COLORS = [
  '#ef4444', '#f97316', '#eab308', '#22c55e', '#14b8a6',
  '#06b6d4', '#3b82f6', '#8b5cf6', '#a855f7', '#ec4899',
]

interface Props {
  projectId: string
  project: Project
}

export function SettingsTab({ projectId, project }: Props) {
  const { toast } = useToast()
  const removeProject = useProjectsStore((s) => s.removeProject)
  const setView = useAppStore((s) => s.setView)
  const [showRemoveConfirm, setShowRemoveConfirm] = useState(false)
  const [name, setName] = useState(project.name)
  const [color, setColor] = useState(project.color || '#e07040')
  const [autoMerge, setAutoMerge] = useState(project.autoMerge)
  const [autoDelete, setAutoDelete] = useState(project.autoDeleteBranch)
  const [autoStart, setAutoStart] = useState(project.autoStartTasks)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    setName(project.name)
    setColor(project.color || '#e07040')
    setAutoMerge(project.autoMerge)
    setAutoDelete(project.autoDeleteBranch)
    setAutoStart(project.autoStartTasks)
  }, [project])

  const fetchProjects = useProjectsStore((s) => s.fetchProjects)

  const save = useCallback(async (updates: Record<string, unknown>) => {
    try {
      const client = getProjectClient()
      await client.updateProject({ projectId, ...updates })
      await fetchProjects()
    } catch (err) {
      toast('Failed to save settings', 'error')
    }
  }, [projectId, fetchProjects])

  const debouncedSave = (updates: Record<string, unknown>) => {
    if (timerRef.current) clearTimeout(timerRef.current)
    timerRef.current = setTimeout(() => save(updates), 500)
  }

  return (
    <div className="flex-1 overflow-y-auto p-6 max-w-lg space-y-6">
      <h3 className="font-heading font-semibold text-sm text-[var(--wf-text-muted)] uppercase tracking-wider">
        Project Settings
      </h3>

      <Input
        label="Project Name"
        value={name}
        onChange={(e) => { setName(e.target.value); debouncedSave({ name: e.target.value }) }}
      />

      <div className="flex flex-col gap-1.5">
        <label className="text-sm font-medium text-[var(--wf-text-secondary)]">Color</label>
        <div className="flex items-center gap-2 flex-wrap">
          {PROJECT_COLORS.map((c) => (
            <button
              key={c}
              type="button"
              onClick={() => { setColor(c); save({ color: c }) }}
              className="w-7 h-7 rounded-full cursor-pointer transition-transform hover:scale-110 focus:outline-none"
              style={{
                backgroundColor: c,
                boxShadow: color === c ? `0 0 0 2px var(--wf-bg), 0 0 0 4px ${c}` : 'none',
              }}
            />
          ))}
        </div>
      </div>

      <div className="space-y-4 pt-2">
        <Toggle
          checked={autoMerge}
          onChange={(v) => { setAutoMerge(v); save({ autoMerge: v }) }}
          label="Auto-merge"
          description="Merge task branches when done"
        />
        <Toggle
          checked={autoDelete}
          onChange={(v) => { setAutoDelete(v); save({ autoDeleteBranch: v }) }}
          label="Auto-delete branches"
          description="Remove worktree branches after merge"
        />
        <Toggle
          checked={autoStart}
          onChange={(v) => { setAutoStart(v); save({ autoStartTasks: v }) }}
          label="Auto-start tasks"
          description="Chain to the next ready task automatically"
        />
      </div>

      <div className="pt-4 border-t border-[var(--wf-border)]">
        <p className="text-xs text-[var(--wf-text-muted)]">
          Project path: <span className="font-mono">{project.path}</span>
        </p>
      </div>

      {/* Danger zone */}
      <div className="pt-4 border-t border-[var(--wf-error)]/30">
        <h4 className="text-sm font-semibold text-[var(--wf-error)] mb-2">Danger Zone</h4>
        <p className="text-xs text-[var(--wf-text-muted)] mb-3">
          Removing a project unregisters it from Watchfire. No files are deleted.
        </p>
        <button
          className="px-3 py-1.5 text-sm rounded-[var(--wf-radius-md)] border border-[var(--wf-error)] text-[var(--wf-error)] hover:bg-[var(--wf-error)] hover:text-white transition-colors"
          onClick={() => setShowRemoveConfirm(true)}
        >
          Remove Project
        </button>
      </div>

      <Modal
        open={showRemoveConfirm}
        onClose={() => setShowRemoveConfirm(false)}
        title="Remove Project"
        footer={
          <>
            <button
              className="px-3 py-1.5 text-sm rounded-[var(--wf-radius-md)] text-[var(--wf-text-secondary)] hover:bg-[var(--wf-bg-elevated)] transition-colors"
              onClick={() => setShowRemoveConfirm(false)}
            >
              Cancel
            </button>
            <button
              className="px-3 py-1.5 text-sm rounded-[var(--wf-radius-md)] bg-[var(--wf-error)] text-white hover:opacity-90 transition-colors"
              onClick={async () => {
                await removeProject(projectId)
                setShowRemoveConfirm(false)
                setView('dashboard')
              }}
            >
              Remove
            </button>
          </>
        }
      >
        <p className="text-sm text-[var(--wf-text-secondary)]">
          This will remove <strong>{project.name}</strong> from Watchfire. No files will be deleted — you can re-add the project folder later.
        </p>
      </Modal>
    </div>
  )
}
