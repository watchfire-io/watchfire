import { useState, useEffect } from 'react'
import type { GitHubIntegration } from '../../../generated/watchfire_pb'
import { IntegrationKind } from '../../../generated/watchfire_pb'
import { useIntegrationsStore, integrationTestKey } from '../../../stores/integrations-store'
import { useProjectsStore } from '../../../stores/projects-store'
import { useToast } from '../../../components/ui/Toast'
import { Button } from '../../../components/ui/Button'
import { Toggle } from '../../../components/ui/Toggle'
import { ProjectMuteSelect } from './ProjectMuteSelect'

interface Props {
  initial: GitHubIntegration
  onClose: () => void
}

export function GitHubDetail({ initial, onClose }: Props) {
  const saveGitHub = useIntegrationsStore((s) => s.saveGitHub)
  const remove = useIntegrationsStore((s) => s.remove)
  const test = useIntegrationsStore((s) => s.test)
  const testResult = useIntegrationsStore(
    (s) => s.testResults[integrationTestKey(IntegrationKind.GITHUB, '')]
  )
  const projects = useProjectsStore((s) => s.projects)
  const { toast } = useToast()

  const [enabled, setEnabled] = useState(initial.enabled)
  const [draftDefault, setDraftDefault] = useState(initial.draftDefault)
  const [scopes, setScopes] = useState<string[]>(initial.projectScopes ?? [])
  const [testing, setTesting] = useState(false)

  useEffect(() => {
    setEnabled(initial.enabled)
    setDraftDefault(initial.draftDefault)
    setScopes(initial.projectScopes ?? [])
  }, [initial.enabled, initial.draftDefault, initial.projectScopes])

  const handleSave = async () => {
    try {
      await saveGitHub({
        enabled,
        draftDefault,
        projectScopes: scopes
      } as never)
      toast('GitHub auto-PR saved', 'success')
      onClose()
    } catch (err) {
      toast(`Save failed: ${(err as Error).message}`, 'error')
    }
  }

  const handleClear = async () => {
    try {
      await remove(IntegrationKind.GITHUB, '')
      toast('GitHub auto-PR reset', 'success')
      onClose()
    } catch (err) {
      toast(`Reset failed: ${(err as Error).message}`, 'error')
    }
  }

  const handleTest = async () => {
    setTesting(true)
    try {
      await test(IntegrationKind.GITHUB, '')
    } finally {
      setTesting(false)
    }
  }

  return (
    <div className="space-y-4 border border-[var(--wf-border)] rounded-[var(--wf-radius-md)] p-4 bg-[var(--wf-bg-elevated)]">
      <h4 className="font-heading font-semibold text-sm">GitHub Auto-PR</h4>
      <p className="text-xs text-[var(--wf-text-muted)]">
        Replaces the silent local merge with a draft PR opened against the project's default branch.
        Authentication uses your local <code className="font-mono">gh</code> CLI session.
      </p>

      <Toggle
        checked={enabled}
        onChange={setEnabled}
        label="Enabled"
        description="When on, completed tasks open a PR instead of merging locally"
      />

      <Toggle
        checked={draftDefault}
        onChange={setDraftDefault}
        label="Open as draft"
        description="Mark new PRs as draft so CI runs but reviews don't auto-request"
        disabled={!enabled}
      />

      <ProjectMuteSelect
        projects={projects}
        value={scopes}
        onChange={setScopes}
        label="Project scopes"
        description="Limit auto-PR to these projects (empty = all projects)"
      />

      <div className="flex items-center gap-2 pt-2">
        <Button onClick={handleSave} variant="primary" size="sm">
          Save
        </Button>
        <Button onClick={handleTest} variant="secondary" size="sm" disabled={testing || !enabled}>
          {testing ? 'Testing…' : 'Test'}
        </Button>
        <Button onClick={handleClear} variant="danger" size="sm">
          Reset
        </Button>
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
