import { useEffect, useState } from 'react'
import { Check } from 'lucide-react'
import type { AgentInfo } from '../../generated/watchfire_pb'
import { getSettingsClient } from '../../lib/grpc-client'
import type { WizardData } from './AddProjectWizard'

interface Props {
  data: WizardData
  onChange: (partial: Partial<WizardData>) => void
  onValidChange: (valid: boolean) => void
}

const DESCRIPTIONS: Record<string, string> = {
  'claude-code': 'Anthropic Claude Code CLI',
  codex: 'OpenAI Codex CLI',
  opencode: 'opencode (SST)'
}

export function StepAgent({ data, onChange, onValidChange }: Props) {
  const [agents, setAgents] = useState<AgentInfo[]>([])
  const [agentsLoaded, setAgentsLoaded] = useState(false)
  const [globalDefault, setGlobalDefault] = useState<string>('')
  const [initialized, setInitialized] = useState(false)

  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        const [agentsRes, settingsRes] = await Promise.all([
          getSettingsClient().listAgents({}),
          getSettingsClient().getSettings({})
        ])
        if (cancelled) return
        setAgents(agentsRes.agents)
        setGlobalDefault(settingsRes.defaults?.defaultAgent ?? '')
        setAgentsLoaded(true)
      } catch {
        if (!cancelled) setAgentsLoaded(true)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [])

  // Compute the initial pre-selection once agents + globalDefault load.
  useEffect(() => {
    if (!agentsLoaded || initialized) return
    setInitialized(true)
    if (data.defaultAgent) return // Preserve any earlier selection
    if (agents.length === 0) return

    const isKnown = (name: string) => agents.some((a) => a.name === name)
    if (globalDefault && isKnown(globalDefault)) {
      onChange({ defaultAgent: globalDefault })
      return
    }
    if (globalDefault === '') {
      // "Ask per project" — no pre-selection.
      return
    }
    // Global default set but unknown — fall back to first registered backend.
    onChange({ defaultAgent: agents[0].name })
  }, [agentsLoaded, initialized, agents, globalDefault, data.defaultAgent, onChange])

  // Emit validity for the wizard's Next button.
  useEffect(() => {
    if (!agentsLoaded) {
      onValidChange(false)
      return
    }
    if (agents.length === 0) {
      onValidChange(true)
      return
    }
    onValidChange(!!data.defaultAgent)
  }, [agentsLoaded, agents, data.defaultAgent, onValidChange])

  if (!agentsLoaded) {
    return (
      <div className="max-w-md flex items-center justify-center py-12">
        <div className="w-5 h-5 border-2 border-fire-500 border-t-transparent rounded-full animate-spin" />
      </div>
    )
  }

  if (agents.length === 0) {
    return (
      <div className="max-w-md space-y-3">
        <p className="text-sm text-[var(--wf-text-primary)]">
          No agents are currently registered.
        </p>
        <p className="text-xs text-[var(--wf-text-muted)]">
          You can continue — the default agent will be assigned at runtime
          (typically <span className="font-mono">claude-code</span>). Configure
          agents in global settings later.
        </p>
      </div>
    )
  }

  return (
    <div className="max-w-md space-y-4">
      <div>
        <h3 className="text-sm font-medium text-[var(--wf-text-primary)] mb-1">
          Which agent should run tasks?
        </h3>
        <p className="text-xs text-[var(--wf-text-muted)]">
          Pick which coding agent Watchfire will launch for tasks in this
          project. You can change this later in project settings.
        </p>
      </div>

      <div className="space-y-2" role="radiogroup" aria-label="Agent selection">
        {agents.map((agent) => {
          const selected = data.defaultAgent === agent.name
          const isGlobalDefault =
            globalDefault !== '' && globalDefault === agent.name
          const description =
            DESCRIPTIONS[agent.name] ?? `${agent.displayName} backend`
          return (
            <button
              key={agent.name}
              type="button"
              role="radio"
              aria-checked={selected}
              onClick={() => onChange({ defaultAgent: agent.name })}
              className={`w-full text-left flex items-start gap-3 p-3 rounded-[var(--wf-radius-md)] border transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-fire-500/50 ${
                selected
                  ? 'border-fire-500 bg-fire-500/5'
                  : 'border-[var(--wf-border)] bg-[var(--wf-bg-primary)] hover:border-[var(--wf-text-muted)]'
              }`}
            >
              <div
                className={`mt-0.5 flex items-center justify-center w-4 h-4 rounded-full border transition-colors ${
                  selected
                    ? 'bg-fire-500 border-fire-500 text-white'
                    : 'border-[var(--wf-border)]'
                }`}
              >
                {selected && <Check size={10} strokeWidth={3} />}
              </div>
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-2">
                  <span className="text-sm font-medium text-[var(--wf-text-primary)]">
                    {agent.displayName}
                  </span>
                  {isGlobalDefault && (
                    <span className="text-[10px] uppercase tracking-wider text-[var(--wf-text-muted)]">
                      (global default)
                    </span>
                  )}
                </div>
                <p className="text-xs text-[var(--wf-text-muted)] mt-0.5">
                  {description}
                </p>
              </div>
            </button>
          )
        })}
      </div>

      {globalDefault === '' && !data.defaultAgent && (
        <p className="text-xs text-[var(--wf-text-muted)]">
          Your global default is set to &ldquo;Ask per project&rdquo; — pick an
          agent to continue.
        </p>
      )}
    </div>
  )
}
