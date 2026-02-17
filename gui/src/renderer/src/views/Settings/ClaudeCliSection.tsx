import type { Settings } from '../../generated/watchfire_pb'
import { useSettingsStore } from '../../stores/settings-store'
import { Input } from '../../components/ui/Input'
import { Badge } from '../../components/ui/Badge'

interface Props {
  settings: Settings
}

export function ClaudeCliSection({ settings }: Props) {
  const updateSettings = useSettingsStore((s) => s.updateSettings)
  const claudeConfig = settings.agents?.['claude-code']
  const path = claudeConfig?.path || ''

  return (
    <section>
      <h3 className="font-heading font-semibold text-sm text-[var(--wf-text-muted)] uppercase tracking-wider mb-3">
        Claude Code CLI
      </h3>
      <div className="space-y-3">
        <div className="flex items-center gap-2">
          <span className="text-sm text-[var(--wf-text-secondary)]">Detection:</span>
          <Badge variant={path ? 'default' : 'success'}>
            {path || 'Auto-detected via PATH'}
          </Badge>
        </div>
        <Input
          label="Custom Path (leave empty for auto-detect)"
          value={path}
          onChange={(e) =>
            updateSettings({
              agents: { 'claude-code': { path: e.target.value } } as any
            })
          }
          placeholder="/usr/local/bin/claude"
        />
      </div>
    </section>
  )
}
