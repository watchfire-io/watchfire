import type { AgentInfo, Settings } from '../../generated/watchfire_pb'
import { useSettingsStore } from '../../stores/settings-store'
import { Toggle } from '../../components/ui/Toggle'
import { Select, type SelectOption } from '../../components/ui/Select'

interface Props {
  settings: Settings
  agents: AgentInfo[]
  agentsLoaded: boolean
}

export function DefaultsSection({ settings, agents, agentsLoaded }: Props) {
  const updateSettings = useSettingsStore((s) => s.updateSettings)
  const defaults = settings.defaults

  const update = (partial: Record<string, unknown>) => {
    updateSettings({ defaults: { ...defaults, ...partial } as any })
  }

  const currentAgent = defaults?.defaultAgent ?? ''
  const knownAgent = agents.some((a) => a.name === currentAgent)

  const agentOptions: SelectOption[] = [
    { value: '', label: 'Ask per project' },
    ...agents.map((a) => ({
      value: a.name,
      label: a.available ? a.displayName : `${a.displayName} (not installed)`,
    }))
  ]
  if (currentAgent && !knownAgent) {
    agentOptions.push({ value: currentAgent, label: `${currentAgent} (unknown)` })
  }

  return (
    <section>
      <h3 className="font-heading font-semibold text-sm text-[var(--wf-text-muted)] uppercase tracking-wider mb-3">
        Defaults for New Projects
      </h3>
      <div className="space-y-4">
        <div className="space-y-1.5">
          <Select
            label="Default Agent"
            value={currentAgent}
            options={agentOptions}
            disabled={!agentsLoaded}
            onChange={(v) => update({ defaultAgent: v })}
          />
          <p className="text-xs text-[var(--wf-text-muted)]">
            Used for new projects when not set per-project. &ldquo;Ask per project&rdquo; makes the
            Add Project wizard prompt every time.
          </p>
        </div>
        <Toggle
          checked={defaults?.autoMerge ?? true}
          onChange={(v) => update({ autoMerge: v })}
          label="Auto-merge"
          description="Merge task branches when done"
        />
        <Toggle
          checked={defaults?.autoDeleteBranch ?? true}
          onChange={(v) => update({ autoDeleteBranch: v })}
          label="Auto-delete branches"
          description="Remove worktree branches after merge"
        />
        <Toggle
          checked={defaults?.autoStartTasks ?? true}
          onChange={(v) => update({ autoStartTasks: v })}
          label="Auto-start tasks"
          description="Chain to the next ready task automatically"
        />
      </div>
    </section>
  )
}
