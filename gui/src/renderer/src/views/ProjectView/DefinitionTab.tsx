import { useState, useEffect, useRef, useCallback } from 'react'
import type { Project } from '../../generated/watchfire_pb'
import { getProjectClient } from '../../lib/grpc-client'
import { useToast } from '../../components/ui/Toast'

interface Props {
  projectId: string
  project: Project
}

export function DefinitionTab({ projectId, project }: Props) {
  const [value, setValue] = useState(project.definition || '')
  const [saved, setSaved] = useState(true)
  const { toast } = useToast()
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    setValue(project.definition || '')
    setSaved(true)
  }, [project.definition, projectId])

  const save = useCallback(async (text: string) => {
    try {
      const client = getProjectClient()
      await client.updateProject({ projectId, definition: text })
      setSaved(true)
    } catch (err) {
      toast('Failed to save definition', 'error')
    }
  }, [projectId])

  const handleChange = (text: string) => {
    setValue(text)
    setSaved(false)
    if (timerRef.current) clearTimeout(timerRef.current)
    timerRef.current = setTimeout(() => save(text), 1000)
  }

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center justify-between px-4 py-2 border-b border-[var(--wf-border)]">
        <span className="text-xs text-[var(--wf-text-muted)]">
          Project definition — Markdown
        </span>
        <span className="text-xs text-[var(--wf-text-muted)]">
          {saved ? 'Saved' : 'Saving...'}
        </span>
      </div>
      <textarea
        value={value}
        onChange={(e) => handleChange(e.target.value)}
        className="flex-1 w-full px-4 py-3 bg-[var(--wf-bg-primary)] text-sm font-mono leading-relaxed text-[var(--wf-text-primary)] placeholder-[var(--wf-text-muted)] focus:outline-none resize-none"
        placeholder="Describe your project, its architecture, coding conventions..."
      />
    </div>
  )
}
