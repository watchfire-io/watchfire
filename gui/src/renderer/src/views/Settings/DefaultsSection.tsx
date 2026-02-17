import type { Settings } from '../../generated/watchfire_pb'
import { useSettingsStore } from '../../stores/settings-store'
import { Input } from '../../components/ui/Input'
import { Toggle } from '../../components/ui/Toggle'

interface Props {
  settings: Settings
}

export function DefaultsSection({ settings }: Props) {
  const updateSettings = useSettingsStore((s) => s.updateSettings)
  const defaults = settings.defaults

  const update = (partial: Record<string, unknown>) => {
    updateSettings({ defaults: { ...defaults, ...partial } as any })
  }

  return (
    <section>
      <h3 className="font-heading font-semibold text-sm text-[var(--wf-text-muted)] uppercase tracking-wider mb-3">
        Defaults for New Projects
      </h3>
      <div className="space-y-4">
        <Input
          label="Default Branch"
          value={defaults?.defaultBranch || 'main'}
          onChange={(e) => update({ defaultBranch: e.target.value })}
        />
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
