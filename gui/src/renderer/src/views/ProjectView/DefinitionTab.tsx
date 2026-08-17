import { useState, useEffect, useRef, useCallback } from 'react'
import { History } from 'lucide-react'
import type { Project } from '../../generated/watchfire_pb'
import { getProjectClient, getTaskClient } from '../../lib/grpc-client'
import { useProjectsStore } from '../../stores/projects-store'
import { useAgentStore } from '../../stores/agent-store'
import { useToast } from '../../components/ui/Toast'
import { Button } from '../../components/ui/Button'
import { Modal } from '../../components/ui/Modal'
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

  const agentStatus = useProjectsStore((s) => s.agentStatuses[projectId])
  const fetchAgentStatus = useProjectsStore((s) => s.fetchAgentStatus)
  const startAgent = useAgentStore((s) => s.startAgent)

  // Confirm-gated archive of folded tasks (v10 Torch). archiveCount is the
  // dry-run candidate count (done tasks at/below the retrofit watermark) —
  // rendered as a persistent affordance so the offer survives tab switches.
  // retrofitRunningRef watches for the running→stopped transition of a
  // retrofit-definition session, which auto-opens the confirmation modal.
  // NEVER archives without the user clicking confirm.
  const retrofitRunningRef = useRef(false)
  const [archiveCount, setArchiveCount] = useState(0)
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [archiving, setArchiving] = useState(false)

  const refreshArchiveCandidates = useCallback(async (): Promise<number> => {
    try {
      const list = await getTaskClient().archiveRetrofitTasks({ projectId, dryRun: true })
      setArchiveCount(list.tasks.length)
      return list.tasks.length
    } catch {
      // dry-run failure just means no archive offer — never block the tab
      return 0
    }
  }, [projectId])

  useEffect(() => {
    refreshArchiveCandidates()
  }, [refreshArchiveCandidates])

  const isRetrofitRunning = !!agentStatus?.isRunning && agentStatus.mode === 'retrofit-definition'

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

  // Detect the retrofit session ending: offer to archive the folded tasks.
  useEffect(() => {
    if (isRetrofitRunning) {
      retrofitRunningRef.current = true
      return
    }
    if (!retrofitRunningRef.current) return
    retrofitRunningRef.current = false
    ;(async () => {
      if ((await refreshArchiveCandidates()) > 0) setConfirmOpen(true)
    })()
  }, [isRetrofitRunning, refreshArchiveCandidates])

  const handleRetrofit = async () => {
    try {
      await startAgent(projectId, 'retrofit-definition')
      await fetchAgentStatus(projectId)
      toast('Retrofit session started — watch the agent pane', 'success')
    } catch (err) {
      toast(String(err), 'error')
    }
  }

  const handleArchive = async () => {
    setArchiving(true)
    try {
      const list = await getTaskClient().archiveRetrofitTasks({ projectId, dryRun: false })
      toast(`Archived ${list.tasks.length} folded task(s) to Trash`, 'success')
      setArchiveCount(0)
      setConfirmOpen(false)
    } catch (err) {
      toast(String(err), 'error')
    } finally {
      setArchiving(false)
    }
  }

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
        <div className="flex items-center gap-3">
          {archiveCount > 0 && !isRetrofitRunning && (
            <Button
              size="sm"
              variant="ghost"
              onClick={() => setConfirmOpen(true)}
              title="Move the tasks already folded into the definition to Trash (reversible; still counted in Insights)"
            >
              Archive {archiveCount} folded task(s)
            </Button>
          )}
          <Button
            size="sm"
            variant="ghost"
            onClick={handleRetrofit}
            disabled={!!agentStatus?.isRunning}
            title="Run an agent session that folds completed tasks back into the definition"
          >
            <History size={12} />
            {isRetrofitRunning ? 'Retrofitting…' : 'Retrofit from completed tasks'}
          </Button>
          <span className="text-xs text-[var(--wf-text-muted)]">
            {saved ? 'Saved' : 'Saving...'}
          </span>
        </div>
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
      <Modal
        open={confirmOpen && archiveCount > 0}
        onClose={() => setConfirmOpen(false)}
        title="Archive folded tasks?"
        footer={
          <>
            <Button variant="ghost" onClick={() => setConfirmOpen(false)} disabled={archiving}>
              Keep tasks
            </Button>
            <Button variant="primary" onClick={handleArchive} disabled={archiving}>
              {archiving ? 'Archiving…' : `Archive ${archiveCount} folded task(s)`}
            </Button>
          </>
        }
      >
        <p className="text-sm text-[var(--wf-text-secondary)]">
          The retrofit folded {archiveCount} completed task(s) into the project definition.
          Archiving moves them to Trash — fully reversible, and they keep counting in
          Insights. Nothing is archived unless you confirm.
        </p>
      </Modal>
    </div>
  )
}
