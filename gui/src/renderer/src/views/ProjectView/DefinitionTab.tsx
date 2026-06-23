import { useState, useEffect, useRef, useCallback } from 'react'
import type { Project } from '../../generated/watchfire_pb'
import { getProjectClient } from '../../lib/grpc-client'
import { useToast } from '../../components/ui/Toast'
import { MarkdownEditor } from '../../components/ui/MarkdownEditor'

interface Props {
  projectId: string
  project: Project
}

export function DefinitionTab({ projectId, project }: Props) {
  const [value, setValue] = useState(project.definition || '')
  const [saved, setSaved] = useState(true)
  const { toast } = useToast()
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const dirtyRef = useRef(false)

  useEffect(() => {
    setValue(project.definition || '')
    setSaved(true)
  }, [project.definition, projectId])

  // Poll for external changes every 3s
  useEffect(() => {
    const interval = setInterval(async () => {
      if (dirtyRef.current) return
      try {
        const client = getProjectClient()
        const proj = await client.getProject({ projectId })
        const remote = proj.definition || ''
        setValue((current) => {
          if (!dirtyRef.current && current !== remote) return remote
          return current
        })
      } catch {
        // ignore polling errors
      }
    }, 3000)
    return () => clearInterval(interval)
  }, [projectId])

  const save = useCallback(async (text: string) => {
    try {
      const client = getProjectClient()
      await client.updateProject({ projectId, definition: text })
      setSaved(true)
      dirtyRef.current = false
    } catch (err) {
      toast('Failed to save definition', 'error')
    }
  }, [projectId])

  const handleChange = (text: string) => {
    setValue(text)
    setSaved(false)
    dirtyRef.current = true
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
      <div className="flex-1 min-h-0 p-3">
        <MarkdownEditor
          value={value}
          onChange={handleChange}
          minHeight="100%"
          className="h-full"
          ariaLabel="Project definition"
          placeholder="Describe your project, its architecture, coding conventions..."
        />
      </div>
    </div>
  )
}
