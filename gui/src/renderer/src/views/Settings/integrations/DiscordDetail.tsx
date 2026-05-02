import { useState, useEffect } from 'react'
import type { DiscordIntegration } from '../../../generated/watchfire_pb'
import { IntegrationKind } from '../../../generated/watchfire_pb'
import { useIntegrationsStore, integrationTestKey } from '../../../stores/integrations-store'
import { useProjectsStore } from '../../../stores/projects-store'
import { useToast } from '../../../components/ui/Toast'
import { Button } from '../../../components/ui/Button'
import { Input } from '../../../components/ui/Input'
import { EventCheckboxes } from './EventCheckboxes'
import { ProjectMuteSelect } from './ProjectMuteSelect'

interface Props {
  initial?: DiscordIntegration
  onClose: () => void
}

export function DiscordDetail({ initial, onClose }: Props) {
  const saveDiscord = useIntegrationsStore((s) => s.saveDiscord)
  const remove = useIntegrationsStore((s) => s.remove)
  const test = useIntegrationsStore((s) => s.test)
  const testResult = useIntegrationsStore(
    (s) => s.testResults[integrationTestKey(IntegrationKind.DISCORD, initial?.id ?? '')]
  )
  const projects = useProjectsStore((s) => s.projects)
  const { toast } = useToast()

  const [label, setLabel] = useState(initial?.label ?? '')
  const [url, setUrl] = useState('')
  const [events, setEvents] = useState({
    taskFailed: initial?.enabledEvents?.taskFailed ?? true,
    runComplete: initial?.enabledEvents?.runComplete ?? true,
    weeklyDigest: initial?.enabledEvents?.weeklyDigest ?? false
  })
  const [muteIds, setMuteIds] = useState<string[]>(initial?.projectMuteIds ?? [])
  const [testing, setTesting] = useState(false)

  useEffect(() => {
    setLabel(initial?.label ?? '')
    setMuteIds(initial?.projectMuteIds ?? [])
  }, [initial?.id])

  const handleSave = async () => {
    try {
      await saveDiscord({
        id: initial?.id ?? '',
        label,
        url,
        enabledEvents: { ...events } as never,
        projectMuteIds: muteIds
      } as never)
      toast('Discord endpoint saved', 'success')
      onClose()
    } catch (err) {
      toast(`Save failed: ${(err as Error).message}`, 'error')
    }
  }

  const handleDelete = async () => {
    if (!initial?.id) {
      onClose()
      return
    }
    try {
      await remove(IntegrationKind.DISCORD, initial.id)
      toast('Discord endpoint deleted', 'success')
      onClose()
    } catch (err) {
      toast(`Delete failed: ${(err as Error).message}`, 'error')
    }
  }

  const handleTest = async () => {
    if (!initial?.id) return
    setTesting(true)
    try {
      await test(IntegrationKind.DISCORD, initial.id)
    } finally {
      setTesting(false)
    }
  }

  return (
    <div className="space-y-4 border border-[var(--wf-border)] rounded-[var(--wf-radius-md)] p-4 bg-[var(--wf-bg-elevated)]">
      <h4 className="font-heading font-semibold text-sm">Discord</h4>

      <Input
        label={initial?.urlSet ? 'Webhook URL — leave blank to keep' : 'Discord webhook URL'}
        type="password"
        value={url}
        onChange={(e) => setUrl(e.target.value)}
        placeholder={initial?.urlLabel || 'https://discord.com/api/webhooks/…'}
      />
      <a
        href="https://support.discord.com/hc/en-us/articles/228383668-Intro-to-Webhooks"
        target="_blank"
        rel="noopener noreferrer"
        className="text-xs text-fire-500 hover:underline"
      >
        How to create a Discord webhook URL →
      </a>

      <Input
        label="Label (optional)"
        value={label}
        onChange={(e) => setLabel(e.target.value)}
        placeholder="#alerts"
      />

      <EventCheckboxes value={events} onChange={setEvents} />

      <ProjectMuteSelect projects={projects} value={muteIds} onChange={setMuteIds} />

      <div className="flex items-center gap-2 pt-2">
        <Button onClick={handleSave} variant="primary" size="sm">
          Save
        </Button>
        {initial?.id && (
          <>
            <Button onClick={handleTest} variant="secondary" size="sm" disabled={testing}>
              {testing ? 'Testing…' : 'Test'}
            </Button>
            <Button onClick={handleDelete} variant="danger" size="sm">
              Delete
            </Button>
          </>
        )}
        <Button onClick={onClose} variant="ghost" size="sm">
          Cancel
        </Button>
        {testResult && (
          <span
            className={
              testResult.ok
                ? 'text-xs text-green-500 ml-auto'
                : 'text-xs text-red-500 ml-auto'
            }
          >
            {testResult.ok ? '✓' : '✗'} {testResult.message}
          </span>
        )}
      </div>
    </div>
  )
}
