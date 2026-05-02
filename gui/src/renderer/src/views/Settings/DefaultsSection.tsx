import { useEffect, useState } from 'react'
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

  // Local-edit state for the terminal shell field — we only send the RPC
  // on blur (or after a Browse… pick) so a partial path doesn't trigger
  // a daemon-side validation error on every keystroke.
  const [terminalShell, setTerminalShell] = useState<string>(defaults?.terminalShell ?? '')
  const [terminalShellError, setTerminalShellError] = useState<string | null>(null)
  // Re-sync local state when settings reload (initial fetch, reconnect).
  useEffect(() => {
    setTerminalShell(defaults?.terminalShell ?? '')
    setTerminalShellError(null)
  }, [defaults?.terminalShell])

  const commitTerminalShell = async (value: string) => {
    setTerminalShellError(null)
    const trimmed = value.trim()
    try {
      await updateSettings({ defaults: { ...defaults, terminalShell: trimmed } as any })
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err)
      setTerminalShellError(msg)
      // Roll the field back to whatever the daemon last accepted so the
      // input doesn't lie about the persisted state.
      setTerminalShell(defaults?.terminalShell ?? '')
    }
  }

  const onBrowseShell = async () => {
    const picked = await window.watchfire.browseShellBinary()
    if (!picked) return
    setTerminalShell(picked)
    await commitTerminalShell(picked)
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
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-[var(--wf-text-secondary)]">
            Terminal shell
          </label>
          <div className="flex gap-2">
            <input
              type="text"
              value={terminalShell}
              placeholder="(empty — use $SHELL)"
              onChange={(e) => setTerminalShell(e.target.value)}
              onBlur={() => {
                if ((terminalShell.trim()) !== (defaults?.terminalShell ?? '')) {
                  void commitTerminalShell(terminalShell)
                }
              }}
              className="flex-1 px-3 py-2 rounded-[var(--wf-radius-md)] text-sm bg-[var(--wf-bg-primary)] border border-[var(--wf-border)] text-[var(--wf-text-primary)] placeholder-[var(--wf-text-muted)] focus:outline-none focus:border-fire-500 focus:ring-1 focus:ring-fire-500/30 transition-colors font-mono"
            />
            <button
              type="button"
              onClick={onBrowseShell}
              className="px-3 py-2 rounded-[var(--wf-radius-md)] text-sm bg-[var(--wf-bg-elevated)] text-[var(--wf-text-primary)] border border-[var(--wf-border)] hover:border-fire-500 transition-colors"
            >
              Browse…
            </button>
          </div>
          <p className="text-xs text-[var(--wf-text-muted)]">
            Custom shell path. Leave empty to use $SHELL.
          </p>
          {terminalShellError && (
            <p className="text-xs text-[var(--wf-error)]">{terminalShellError}</p>
          )}
        </div>
      </div>
    </section>
  )
}
