import type { Settings } from '../../generated/watchfire_pb'
import { useSettingsStore } from '../../stores/settings-store'
import { Toggle } from '../../components/ui/Toggle'
import { Button } from '../../components/ui/Button'
import { RefreshCw } from 'lucide-react'

interface Props {
  settings: Settings
}

export function UpdatesSection({ settings }: Props) {
  const updateSettings = useSettingsStore((s) => s.updateSettings)
  const updates = settings.updates

  const update = (partial: Record<string, unknown>) => {
    updateSettings({ updates: { ...updates, ...partial } as any })
  }

  return (
    <section>
      <h3 className="font-heading font-semibold text-sm text-[var(--wf-text-muted)] uppercase tracking-wider mb-3">
        Updates
      </h3>
      <div className="space-y-4">
        <Toggle
          checked={updates?.checkOnStartup ?? true}
          onChange={(v) => update({ checkOnStartup: v })}
          label="Check on startup"
          description="Check for updates when Watchfire launches"
        />
        <Toggle
          checked={updates?.autoDownload ?? false}
          onChange={(v) => update({ autoDownload: v })}
          label="Auto-download"
          description="Download updates automatically"
        />

        <div className="flex items-center gap-2">
          <label className="text-sm font-medium text-[var(--wf-text-secondary)]">Frequency:</label>
          <select
            value={updates?.checkFrequency || 'every_launch'}
            onChange={(e) => update({ checkFrequency: e.target.value })}
            className="px-2 py-1 text-sm rounded-[var(--wf-radius-md)] bg-[var(--wf-bg-primary)] border border-[var(--wf-border)] text-[var(--wf-text-primary)] focus:outline-none focus:border-fire-500"
          >
            <option value="every_launch">Every launch</option>
            <option value="daily">Daily</option>
            <option value="weekly">Weekly</option>
          </select>
        </div>

        <Button size="sm" variant="secondary">
          <RefreshCw size={14} />
          Check Now
        </Button>
      </div>
    </section>
  )
}
