import type { AgentInfo, Settings } from '../../generated/watchfire_pb'
import { useSettingsStore } from '../../stores/settings-store'
import { Input } from '../../components/ui/Input'
import { Badge } from '../../components/ui/Badge'

interface Props {
  settings: Settings
  agents: AgentInfo[]
  agentsLoaded: boolean
}

const PATH_PLACEHOLDERS: Record<string, string> = {
  'claude-code': '/opt/homebrew/bin/claude',
  codex: '/opt/homebrew/bin/codex',
  opencode: '/opt/homebrew/bin/opencode'
}

function placeholderFor(name: string): string {
  return PATH_PLACEHOLDERS[name] ?? `/usr/local/bin/${name}`
}

export function AgentPathsSection({ settings, agents, agentsLoaded }: Props) {
  const updateSettings = useSettingsStore((s) => s.updateSettings)

  const handlePathChange = (name: string, path: string) => {
    const existing = settings.agents ?? {}
    const merged: { [key: string]: { path: string } } = {}
    for (const [k, v] of Object.entries(existing)) {
      merged[k] = { path: v.path }
    }
    merged[name] = { path }
    updateSettings({ agents: merged })
  }

  return (
    <section>
      <h3 className="font-heading font-semibold text-sm text-[var(--wf-text-muted)] uppercase tracking-wider mb-3">
        Agent Binary Paths
      </h3>
      {!agentsLoaded ? (
        <p className="text-sm text-[var(--wf-text-muted)]">Loading agents…</p>
      ) : agents.length === 0 ? (
        <p className="text-sm text-[var(--wf-text-muted)]">No agents available.</p>
      ) : (
        <div className="space-y-5">
          {agents.map((agent) => {
            const path = settings.agents?.[agent.name]?.path || ''
            // Badge states:
            //   - path set: show the configured path (neutral)
            //   - no path + available: auto-detected on PATH/fallbacks (success)
            //   - no path + !available: binary missing; still listed so the
            //     user can install it and pick it — see issue #29 for the
            //     regression this avoids.
            const badgeLabel = path
              ? path
              : agent.available
                ? 'Auto-detected via PATH'
                : 'Not installed'
            const badgeVariant: 'default' | 'success' | 'warning' = path
              ? 'default'
              : agent.available
                ? 'success'
                : 'warning'
            return (
              <div key={agent.name} className="space-y-2">
                <div className="flex items-center justify-between gap-3">
                  <span className="text-sm font-medium text-[var(--wf-text-primary)]">
                    {agent.displayName}
                  </span>
                  <Badge variant={badgeVariant}>{badgeLabel}</Badge>
                </div>
                <Input
                  value={path}
                  onChange={(e) => handlePathChange(agent.name, e.target.value)}
                  placeholder={placeholderFor(agent.name)}
                />
              </div>
            )
          })}
        </div>
      )}
    </section>
  )
}
