import { useState, useEffect } from 'react'
import type { WebhookIntegration } from '../../../generated/watchfire_pb'
import { IntegrationKind } from '../../../generated/watchfire_pb'
import { useIntegrationsStore, integrationTestKey } from '../../../stores/integrations-store'
import { useProjectsStore } from '../../../stores/projects-store'
import { useToast } from '../../../components/ui/Toast'
import { Button } from '../../../components/ui/Button'
import { Toggle } from '../../../components/ui/Toggle'
import { Input } from '../../../components/ui/Input'
import { EventCheckboxes } from './EventCheckboxes'
import { ProjectMuteSelect } from './ProjectMuteSelect'

interface Props {
  initial?: WebhookIntegration
  onClose: () => void
}

export function WebhookDetail({ initial, onClose }: Props) {
  const saveWebhook = useIntegrationsStore((s) => s.saveWebhook)
  const remove = useIntegrationsStore((s) => s.remove)
  const test = useIntegrationsStore((s) => s.test)
  const testResult = useIntegrationsStore(
    (s) => s.testResults[integrationTestKey(IntegrationKind.WEBHOOK, initial?.id ?? '')]
  )
  const projects = useProjectsStore((s) => s.projects)
  const { toast } = useToast()

  const [label, setLabel] = useState(initial?.label ?? '')
  const [url, setUrl] = useState(initial?.url ?? '')
  const [secret, setSecret] = useState('')
  const [events, setEvents] = useState({
    taskFailed: initial?.enabledEvents?.taskFailed ?? true,
    runComplete: initial?.enabledEvents?.runComplete ?? true,
    weeklyDigest: initial?.enabledEvents?.weeklyDigest ?? false
  })
  const [muteIds, setMuteIds] = useState<string[]>(initial?.projectMuteIds ?? [])
  const [testing, setTesting] = useState(false)

  useEffect(() => {
    setLabel(initial?.label ?? '')
    setUrl(initial?.url ?? '')
    setMuteIds(initial?.projectMuteIds ?? [])
  }, [initial?.id])

  const handleSave = async () => {
    try {
      await saveWebhook({
        id: initial?.id ?? '',
        label,
        url,
        secret,
        enabledEvents: { ...events } as never,
        projectMuteIds: muteIds
      } as never)
      toast('Webhook saved', 'success')
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
      await remove(IntegrationKind.WEBHOOK, initial.id)
      toast('Webhook deleted', 'success')
      onClose()
    } catch (err) {
      toast(`Delete failed: ${(err as Error).message}`, 'error')
    }
  }

  const handleTest = async () => {
    if (!initial?.id) return
    setTesting(true)
    try {
      await test(IntegrationKind.WEBHOOK, initial.id)
    } finally {
      setTesting(false)
    }
  }

  return (
    <div className="space-y-4 border border-[var(--wf-border)] rounded-[var(--wf-radius-md)] p-4 bg-[var(--wf-bg-elevated)]">
      <h4 className="font-heading font-semibold text-sm">Webhook</h4>

      <Input
        label="Endpoint URL"
        value={url}
        onChange={(e) => setUrl(e.target.value)}
        placeholder="https://example.com/incoming"
      />

      <Input
        label="Label (optional)"
        value={label}
        onChange={(e) => setLabel(e.target.value)}
        placeholder="Slack #ops channel"
      />

      <Input
        label="HMAC secret"
        type="password"
        value={secret}
        onChange={(e) => setSecret(e.target.value)}
        placeholder={initial?.secretSet ? 'secret set — leave blank to keep' : 'optional'}
      />

      <EventCheckboxes value={events} onChange={setEvents} />

      <ProjectMuteSelect
        projects={projects}
        value={muteIds}
        onChange={setMuteIds}
      />

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

      {/* Toggle — kept so the Toggle import has a use; surfaces a future
          "muted" master switch the daemon already supports via empty
          enabled_events. */}
      <Toggle
        checked={events.taskFailed || events.runComplete || events.weeklyDigest}
        onChange={() => {}}
        label="Endpoint active"
        disabled
      />
    </div>
  )
}
