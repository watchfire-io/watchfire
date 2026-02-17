import { useState, useEffect, useRef, useCallback } from 'react'
import type { Project } from '../../generated/watchfire_pb'
import { getProjectClient } from '../../lib/grpc-client'
import { Input } from '../../components/ui/Input'
import { Toggle } from '../../components/ui/Toggle'
import { useToast } from '../../components/ui/Toast'

interface Props {
  projectId: string
  project: Project
}

export function SettingsTab({ projectId, project }: Props) {
  const { toast } = useToast()
  const [name, setName] = useState(project.name)
  const [color, setColor] = useState(project.color || '#e07040')
  const [defaultBranch, setDefaultBranch] = useState(project.defaultBranch)
  const [autoMerge, setAutoMerge] = useState(project.autoMerge)
  const [autoDelete, setAutoDelete] = useState(project.autoDeleteBranch)
  const [autoStart, setAutoStart] = useState(project.autoStartTasks)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    setName(project.name)
    setColor(project.color || '#e07040')
    setDefaultBranch(project.defaultBranch)
    setAutoMerge(project.autoMerge)
    setAutoDelete(project.autoDeleteBranch)
    setAutoStart(project.autoStartTasks)
  }, [project])

  const save = useCallback(async (updates: Record<string, unknown>) => {
    try {
      const client = getProjectClient()
      await client.updateProject({ projectId, ...updates })
    } catch (err) {
      toast('Failed to save settings', 'error')
    }
  }, [projectId])

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
        <div className="flex items-center gap-3">
          <input
            type="color"
            value={color}
            onChange={(e) => { setColor(e.target.value); debouncedSave({ color: e.target.value }) }}
            className="w-8 h-8 rounded-[var(--wf-radius-md)] border border-[var(--wf-border)] cursor-pointer bg-transparent"
          />
          <span className="text-sm font-mono text-[var(--wf-text-muted)]">{color}</span>
        </div>
      </div>

      <Input
        label="Default Branch"
        value={defaultBranch}
        onChange={(e) => { setDefaultBranch(e.target.value); debouncedSave({ defaultBranch: e.target.value }) }}
      />

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
    </div>
  )
}
