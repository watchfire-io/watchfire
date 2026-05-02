import { useEffect, useState } from 'react'
import type {
  WebhookIntegration,
  SlackIntegration,
  DiscordIntegration,
  IntegrationEvents
} from '../../generated/watchfire_pb'
import { IntegrationKind } from '../../generated/watchfire_pb'
import { useIntegrationsStore } from '../../stores/integrations-store'
import { Button } from '../../components/ui/Button'
import { WebhookDetail } from './integrations/WebhookDetail'
import { SlackDetail } from './integrations/SlackDetail'
import { DiscordDetail } from './integrations/DiscordDetail'
import { GitHubDetail } from './integrations/GitHubDetail'

type DetailTarget =
  | { kind: IntegrationKind.WEBHOOK; id: string | null }
  | { kind: IntegrationKind.SLACK; id: string | null }
  | { kind: IntegrationKind.DISCORD; id: string | null }
  | { kind: IntegrationKind.GITHUB }
  | null

function formatEvents(events?: IntegrationEvents): string {
  if (!events) return '(none)'
  const parts: string[] = []
  if (events.taskFailed) parts.push('TASK_FAILED')
  if (events.runComplete) parts.push('RUN_COMPLETE')
  if (events.weeklyDigest) parts.push('WEEKLY_DIGEST')
  return parts.length === 0 ? '(no events)' : parts.join(' · ')
}

interface CardProps {
  kind: string
  label: string
  urlLabel: string
  events?: IntegrationEvents
  muteCount: number
  onEdit: () => void
}

function IntegrationCard({ kind, label, urlLabel, events, muteCount, onEdit }: CardProps) {
  return (
    <div
      className="flex flex-col gap-1 p-3 rounded-[var(--wf-radius-md)] border border-[var(--wf-border)] hover:border-fire-500/50 cursor-pointer bg-[var(--wf-bg-primary)]"
      onClick={onEdit}
      role="button"
      tabIndex={0}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') onEdit()
      }}
    >
      <div className="flex items-center gap-2">
        <span className="text-xs uppercase tracking-wider text-[var(--wf-text-muted)] font-semibold">
          {kind}
        </span>
        <span className="text-sm font-medium text-[var(--wf-text-primary)]">{label || '(unnamed)'}</span>
      </div>
      <div className="text-xs text-[var(--wf-text-muted)] truncate font-mono">{urlLabel || '—'}</div>
      <div className="flex items-center gap-2 mt-1">
        <span className="text-xs px-2 py-0.5 rounded-full bg-[var(--wf-bg-elevated)] text-[var(--wf-text-secondary)]">
          {formatEvents(events)}
        </span>
        {muteCount > 0 && (
          <span className="text-xs px-2 py-0.5 rounded-full bg-[var(--wf-bg-elevated)] text-[var(--wf-text-muted)]">
            {muteCount} muted
          </span>
        )}
      </div>
    </div>
  )
}

export function IntegrationsSection() {
  const config = useIntegrationsStore((s) => s.config)
  const fetchConfig = useIntegrationsStore((s) => s.fetch)
  const loading = useIntegrationsStore((s) => s.loading)
  const [target, setTarget] = useState<DetailTarget>(null)
  const [pickerOpen, setPickerOpen] = useState(false)

  useEffect(() => {
    fetchConfig()
  }, [fetchConfig])

  const closeDetail = () => setTarget(null)

  const renderDetail = () => {
    if (!target) return null
    if (target.kind === IntegrationKind.GITHUB) {
      const gh = config?.github ?? { $typeName: 'watchfire.GitHubIntegration', enabled: false, draftDefault: false, projectScopes: [] }
      return <GitHubDetail initial={gh as never} onClose={closeDetail} />
    }
    if (target.kind === IntegrationKind.WEBHOOK) {
      const initial = target.id
        ? (config?.webhooks ?? []).find((w) => w.id === target.id) as WebhookIntegration | undefined
        : undefined
      return <WebhookDetail initial={initial} onClose={closeDetail} />
    }
    if (target.kind === IntegrationKind.SLACK) {
      const initial = target.id
        ? (config?.slack ?? []).find((s) => s.id === target.id) as SlackIntegration | undefined
        : undefined
      return <SlackDetail initial={initial} onClose={closeDetail} />
    }
    if (target.kind === IntegrationKind.DISCORD) {
      const initial = target.id
        ? (config?.discord ?? []).find((d) => d.id === target.id) as DiscordIntegration | undefined
        : undefined
      return <DiscordDetail initial={initial} onClose={closeDetail} />
    }
    return null
  }

  return (
    <section>
      <h3 className="font-heading font-semibold text-sm text-[var(--wf-text-muted)] uppercase tracking-wider mb-1">
        Integrations
      </h3>
      <p className="text-xs text-[var(--wf-text-muted)] mb-3">
        Forward Watchfire's notifications to outside channels.
      </p>

      <div className="space-y-3 mb-3">
        {(config?.webhooks ?? []).map((w) => (
          <IntegrationCard
            key={`w-${w.id}`}
            kind="Webhook"
            label={w.label}
            urlLabel={w.urlLabel}
            events={w.enabledEvents}
            muteCount={w.projectMuteIds?.length ?? 0}
            onEdit={() => setTarget({ kind: IntegrationKind.WEBHOOK, id: w.id })}
          />
        ))}
        {(config?.slack ?? []).map((s) => (
          <IntegrationCard
            key={`s-${s.id}`}
            kind="Slack"
            label={s.label}
            urlLabel={s.urlLabel}
            events={s.enabledEvents}
            muteCount={s.projectMuteIds?.length ?? 0}
            onEdit={() => setTarget({ kind: IntegrationKind.SLACK, id: s.id })}
          />
        ))}
        {(config?.discord ?? []).map((d) => (
          <IntegrationCard
            key={`d-${d.id}`}
            kind="Discord"
            label={d.label}
            urlLabel={d.urlLabel}
            events={d.enabledEvents}
            muteCount={d.projectMuteIds?.length ?? 0}
            onEdit={() => setTarget({ kind: IntegrationKind.DISCORD, id: d.id })}
          />
        ))}
        <IntegrationCard
          kind="GitHub"
          label={config?.github?.enabled ? 'Auto-PR enabled' : 'Auto-PR (disabled)'}
          urlLabel={
            config?.github?.enabled
              ? `${config.github.draftDefault ? 'draft · ' : ''}${(config.github.projectScopes?.length ?? 0) > 0 ? `${config.github.projectScopes.length} project(s)` : 'all projects'}`
              : '—'
          }
          muteCount={0}
          onEdit={() => setTarget({ kind: IntegrationKind.GITHUB })}
        />
      </div>

      {!target && (
        <div className="relative">
          <Button variant="secondary" size="sm" onClick={() => setPickerOpen((o) => !o)}>
            Add integration ▾
          </Button>
          {pickerOpen && (
            <div className="absolute z-10 mt-1 bg-[var(--wf-bg-elevated)] border border-[var(--wf-border)] rounded-[var(--wf-radius-md)] shadow-lg">
              {[
                { label: 'Webhook', kind: IntegrationKind.WEBHOOK },
                { label: 'Slack', kind: IntegrationKind.SLACK },
                { label: 'Discord', kind: IntegrationKind.DISCORD },
                { label: 'GitHub Auto-PR', kind: IntegrationKind.GITHUB }
              ].map((p) => (
                <button
                  key={p.label}
                  className="block w-full text-left px-3 py-1.5 text-sm hover:bg-[var(--wf-bg-primary)] text-[var(--wf-text-primary)]"
                  onClick={() => {
                    setPickerOpen(false)
                    setTarget(
                      p.kind === IntegrationKind.GITHUB
                        ? { kind: IntegrationKind.GITHUB }
                        : ({ kind: p.kind, id: null } as DetailTarget)
                    )
                  }}
                >
                  {p.label}
                </button>
              ))}
            </div>
          )}
        </div>
      )}

      {target && <div className="mt-3">{renderDetail()}</div>}

      {loading && (config?.webhooks ?? []).length === 0 && (
        <p className="text-xs text-[var(--wf-text-muted)] mt-2">Loading…</p>
      )}
    </section>
  )
}
