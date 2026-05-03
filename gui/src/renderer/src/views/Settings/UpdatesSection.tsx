import { useState } from 'react'
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
  const [checking, setChecking] = useState(false)

  const update = (partial: Record<string, unknown>) => {
    updateSettings({ updates: { ...updates, ...partial } as any })
  }

  const handleCheckNow = async () => {
    setChecking(true)
    try {
      await window.watchfire.checkForUpdates()
    } catch {
      // errors are surfaced via the update-error event → UpdateBanner
    } finally {
      setChecking(false)
    }
  }

  return (
    <section>
      <h3 className="font-heading font-semibold text-sm text-[var(--wf-text-muted)] uppercase tracking-wider mb-3">
        Updates
      </h3>
      <div className="space-y-4">
        <div data-setting-field-id="updates-check-startup">
          <Toggle
            checked={updates?.checkOnStartup ?? true}
            onChange={(v) => update({ checkOnStartup: v })}
            label="Check on startup"
            description="Check for updates when Watchfire launches"
          />
        </div>
        <div data-setting-field-id="updates-auto-download">
          <Toggle
            checked={updates?.autoDownload ?? false}
            onChange={(v) => update({ autoDownload: v })}
            label="Auto-download"
            description="Download updates automatically"
          />
        </div>

        <div className="flex items-center gap-2" data-setting-field-id="updates-frequency">
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

        <div data-setting-field-id="updates-check-now">
          <Button size="sm" variant="secondary" onClick={handleCheckNow} disabled={checking}>
            <RefreshCw size={14} className={checking ? 'animate-spin' : ''} />
            {checking ? 'Checking...' : 'Check Now'}
          </Button>
        </div>
      </div>
    </section>
  )
}
