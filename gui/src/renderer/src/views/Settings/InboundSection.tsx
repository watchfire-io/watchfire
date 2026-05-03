import { useEffect, useRef, useState } from 'react'
import { useIntegrationsStore } from '../../stores/integrations-store'
import { useToast } from '../../components/ui/Toast'
import { Button } from '../../components/ui/Button'
import { Input } from '../../components/ui/Input'
import { Toggle } from '../../components/ui/Toggle'

const POLL_INTERVAL_MS = 5000

const PROVIDER_PATHS: Record<'github' | 'slack' | 'discord', string> = {
  github: '/echo/github',
  slack: '/echo/slack/commands',
  discord: '/echo/discord/interactions'
}

function formatLastDelivery(unix: bigint | undefined): string {
  if (!unix || unix === 0n) return 'never'
  const ms = Number(unix) * 1000
  const date = new Date(ms)
  const diffMs = Date.now() - ms
  if (diffMs < 60_000) return `${Math.max(1, Math.floor(diffMs / 1000))}s ago`
  if (diffMs < 3_600_000) return `${Math.floor(diffMs / 60_000)}m ago`
  if (diffMs < 86_400_000) return `${Math.floor(diffMs / 3_600_000)}h ago`
  return date.toLocaleString()
}

function joinUrl(base: string, path: string): string {
  if (!base) return ''
  const trimmed = base.replace(/\/+$/, '')
  return trimmed + path
}

export function InboundSection() {
  const inbound = useIntegrationsStore((s) => s.inbound)
  const fetchInbound = useIntegrationsStore((s) => s.fetchInbound)
  const saveInbound = useIntegrationsStore((s) => s.saveInbound)
  const saving = useIntegrationsStore((s) => s.saving)
  const { toast } = useToast()
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null)

  // Form state — controlled inputs that mirror the persisted config but
  // can be edited locally before pressing Save / Restart server.
  const [listenAddr, setListenAddr] = useState('')
  const [publicUrl, setPublicUrl] = useState('')
  const [disabled, setDisabled] = useState(false)
  const [discordAppId, setDiscordAppId] = useState('')
  const [githubSecret, setGithubSecret] = useState('')
  const [slackSecret, setSlackSecret] = useState('')
  const [discordPubKey, setDiscordPubKey] = useState('')
  const [discordBotToken, setDiscordBotToken] = useState('')

  // Hydrate form from latest config whenever it changes server-side.
  useEffect(() => {
    if (!inbound?.config) return
    setListenAddr(inbound.config.listenAddr ?? '')
    setPublicUrl(inbound.config.publicUrl ?? '')
    setDisabled(inbound.config.disabled ?? false)
    setDiscordAppId(inbound.config.discordAppId ?? '')
  }, [
    inbound?.config?.listenAddr,
    inbound?.config?.publicUrl,
    inbound?.config?.disabled,
    inbound?.config?.discordAppId
  ])

  // Initial fetch + poll on a 5s interval while mounted.
  useEffect(() => {
    fetchInbound()
    pollRef.current = setInterval(() => {
      fetchInbound()
    }, POLL_INTERVAL_MS)
    return () => {
      if (pollRef.current) clearInterval(pollRef.current)
    }
  }, [fetchInbound])

  const handleSaveAddress = async () => {
    try {
      await saveInbound({
        listenAddr,
        publicUrl,
        disabled,
        discordAppId,
        githubSecret: '',
        slackSecret: '',
        discordPublicKey: '',
        discordBotToken: ''
      })
      toast('Inbound listener restarted', 'success')
    } catch (err) {
      toast(`Save failed: ${(err as Error).message}`, 'error')
    }
  }

  const handleSaveSecret = async (
    field: 'githubSecret' | 'slackSecret' | 'discordPublicKey' | 'discordBotToken',
    value: string,
    label: string,
    clear: () => void
  ) => {
    if (!value) {
      toast('Enter a value before pressing Set', 'info')
      return
    }
    try {
      await saveInbound({
        listenAddr,
        publicUrl,
        disabled,
        discordAppId,
        githubSecret: '',
        slackSecret: '',
        discordPublicKey: '',
        discordBotToken: '',
        [field]: value
      })
      clear()
      toast(`${label} saved`, 'success')
    } catch (err) {
      toast(`${label} save failed: ${(err as Error).message}`, 'error')
    }
  }

  const handleCopyProviderUrl = async (provider: 'github' | 'slack' | 'discord') => {
    const url = joinUrl(publicUrl, PROVIDER_PATHS[provider])
    if (!url) {
      toast('Set a Public URL first', 'info')
      return
    }
    try {
      await navigator.clipboard.writeText(url)
      toast(`Copied: ${url}`, 'success')
    } catch {
      toast('Copy failed — clipboard unavailable', 'error')
    }
  }

  const listening = inbound?.listening ?? false
  const bindError = inbound?.bindError ?? ''

  return (
    <section>
      <h3 className="font-heading font-semibold text-sm text-[var(--wf-text-muted)] uppercase tracking-wider mb-1">
        Inbound (Echo)
      </h3>
      <p className="text-xs text-[var(--wf-text-muted)] mb-3">
        Receive signed webhook deliveries from GitHub / Slack / Discord. Closes the loop on
        auto-PR merges and powers in-channel slash commands like <code className="font-mono">/watchfire status</code>.
      </p>

      {/* Listening status pill */}
      <div className="flex items-center gap-3 mb-4">
        <span
          className={
            listening
              ? 'inline-flex items-center gap-1.5 text-xs px-2 py-0.5 rounded-full bg-green-900/30 text-green-400'
              : 'inline-flex items-center gap-1.5 text-xs px-2 py-0.5 rounded-full bg-red-900/30 text-red-400'
          }
        >
          <span
            className={
              listening
                ? 'w-1.5 h-1.5 rounded-full bg-green-400'
                : 'w-1.5 h-1.5 rounded-full bg-red-400'
            }
          />
          {listening ? 'Listening' : disabled ? 'Disabled' : 'Not listening'}
        </span>
        <span className="text-xs text-[var(--wf-text-muted)] font-mono">
          {inbound?.listenAddr ?? '—'}
        </span>
        {bindError && <span className="text-xs text-red-400">{bindError}</span>}
      </div>

      {/* Master toggle */}
      <div className="mb-4" data-setting-field-id="inbound-enabled">
        <Toggle
          checked={!disabled}
          onChange={(on) => setDisabled(!on)}
          label="Enable inbound"
          description="When off, the Echo HTTP server doesn't bind any port."
        />
      </div>

      {/* Listen address */}
      <div className="space-y-3 mb-4 border border-[var(--wf-border)] rounded-[var(--wf-radius-md)] p-3 bg-[var(--wf-bg-elevated)]">
        <div data-setting-field-id="inbound-listen-addr">
          <Input
            label="Listen address"
            value={listenAddr}
            onChange={(e) => setListenAddr(e.target.value)}
            placeholder="127.0.0.1:8765"
          />
        </div>
        <div data-setting-field-id="inbound-public-url">
          <Input
            label="Public URL"
            value={publicUrl}
            onChange={(e) => setPublicUrl(e.target.value)}
            placeholder="https://your-tunnel.ngrok.app"
          />
        </div>
        <p className="text-xs text-[var(--wf-text-muted)]">
          Set this to your tunneled URL (ngrok / Cloudflare Tunnel) so providers reach the
          listener over the public internet. Must start with <code className="font-mono">https://</code>.
        </p>
        <div className="flex flex-wrap gap-2 pt-1">
          <Button variant="secondary" size="sm" onClick={() => handleCopyProviderUrl('github')}>
            Copy as GitHub URL
          </Button>
          <Button variant="secondary" size="sm" onClick={() => handleCopyProviderUrl('slack')}>
            Copy as Slack URL
          </Button>
          <Button variant="secondary" size="sm" onClick={() => handleCopyProviderUrl('discord')}>
            Copy as Discord URL
          </Button>
        </div>
        <div className="flex items-center gap-2 pt-2 border-t border-[var(--wf-border)]">
          <Button onClick={handleSaveAddress} variant="primary" size="sm" disabled={saving}>
            {saving ? 'Saving…' : 'Restart server'}
          </Button>
        </div>
      </div>

      {/* Per-provider secrets */}
      <div className="space-y-3 mb-3">
        <div data-setting-field-id="inbound-github-secret">
          <ProviderSecretRow
            title="GitHub webhook secret"
            help="Configured under Settings → Webhooks on each project on github.com (HMAC-SHA256)."
            set={inbound?.config?.githubSecretSet ?? false}
            lastDelivery={formatLastDelivery(inbound?.lastGithubDeliveryUnix)}
            value={githubSecret}
            onChange={setGithubSecret}
            onSave={() =>
              handleSaveSecret('githubSecret', githubSecret, 'GitHub secret', () => setGithubSecret(''))
            }
            saving={saving}
          />
        </div>
        <div data-setting-field-id="inbound-slack-secret">
          <ProviderSecretRow
            title="Slack signing secret"
            help="From the Slack app config under Basic Information → Signing Secret."
            set={inbound?.config?.slackSecretSet ?? false}
            lastDelivery={formatLastDelivery(inbound?.lastSlackDeliveryUnix)}
            value={slackSecret}
            onChange={setSlackSecret}
            onSave={() =>
              handleSaveSecret('slackSecret', slackSecret, 'Slack secret', () => setSlackSecret(''))
            }
            saving={saving}
          />
        </div>
        <div data-setting-field-id="inbound-discord-public-key">
          <ProviderSecretRow
            title="Discord application public key"
            help="From the Discord developer portal → General Information → Public Key (Ed25519)."
            set={inbound?.config?.discordPublicKeySet ?? false}
            lastDelivery={formatLastDelivery(inbound?.lastDiscordDeliveryUnix)}
            value={discordPubKey}
            onChange={setDiscordPubKey}
            onSave={() =>
              handleSaveSecret(
                'discordPublicKey',
                discordPubKey,
                'Discord public key',
                () => setDiscordPubKey('')
              )
            }
            saving={saving}
          />
        </div>
      </div>

      {/* Discord bot extras (app ID + bot token) — drives the v8.x Echo
          auto-registrar. Once both are set the daemon opens a Gateway
          connection and registers slash commands against every guild
          the bot joins (≤ 30s after the bot accepts the invite). The
          `watchfire integrations register-discord <guild>` CLI is the
          manual fallback. */}
      <div className="space-y-3 mb-3 border border-[var(--wf-border)] rounded-[var(--wf-radius-md)] p-3 bg-[var(--wf-bg-elevated)]">
        <h4 className="font-heading font-semibold text-sm">Discord slash-command registration</h4>
        <p className="text-xs text-[var(--wf-text-muted)]">
          When both fields below are set, the daemon auto-registers the three Watchfire slash
          commands against every guild the bot is in — and against new guilds within ~30s of
          the bot being added. The <code className="font-mono">watchfire integrations register-discord</code>
          CLI remains as a manual fallback.
        </p>
        <div data-setting-field-id="inbound-discord-app-id">
          <Input
            label="Discord application ID"
            value={discordAppId}
            onChange={(e) => setDiscordAppId(e.target.value)}
            placeholder="123456789012345678"
          />
        </div>
        <div data-setting-field-id="inbound-discord-bot-token">
          <ProviderSecretRow
            title="Discord bot token"
            help="From the Discord developer portal → Bot → Token."
            set={inbound?.config?.discordBotTokenSet ?? false}
            lastDelivery=""
            value={discordBotToken}
            onChange={setDiscordBotToken}
            onSave={() =>
              handleSaveSecret(
                'discordBotToken',
                discordBotToken,
                'Discord bot token',
                () => setDiscordBotToken('')
              )
            }
            saving={saving}
          />
        </div>
        <DiscordGuildList guilds={inbound?.discordGuilds ?? []} />
      </div>
    </section>
  )
}

interface DiscordGuildListProps {
  guilds: ReadonlyArray<{
    guildId: string
    guildName: string
    registered: boolean
    error: string
    registeredAtUnix: bigint
  }>
}

function DiscordGuildList({ guilds }: DiscordGuildListProps) {
  if (guilds.length === 0) {
    return (
      <p className="text-xs text-[var(--wf-text-muted)] italic">
        No guilds yet — invite the bot to a Discord guild to populate this list.
      </p>
    )
  }
  return (
    <div className="border-t border-[var(--wf-border)] pt-3">
      <h5 className="font-heading font-semibold text-xs uppercase tracking-wider text-[var(--wf-text-muted)] mb-2">
        Registered guilds
      </h5>
      <ul className="space-y-1.5">
        {guilds.map((g) => (
          <li
            key={g.guildId}
            className="flex items-center justify-between gap-3 text-xs px-2 py-1 rounded bg-[var(--wf-bg-primary)]"
          >
            <div className="flex items-center gap-2 min-w-0">
              <span
                className={
                  g.registered
                    ? 'inline-flex items-center justify-center w-4 h-4 rounded-full bg-green-900/30 text-green-400'
                    : 'inline-flex items-center justify-center w-4 h-4 rounded-full bg-red-900/30 text-red-400'
                }
                title={g.registered ? 'Registered' : g.error || 'Not registered'}
              >
                {g.registered ? '✓' : '✗'}
              </span>
              <span className="truncate">{g.guildName || '(unknown)'}</span>
              <span className="font-mono text-[var(--wf-text-muted)]">{g.guildId}</span>
            </div>
            <span className="text-[var(--wf-text-muted)] shrink-0">
              {formatLastDelivery(g.registeredAtUnix)}
            </span>
          </li>
        ))}
      </ul>
    </div>
  )
}

interface ProviderSecretRowProps {
  title: string
  help: string
  set: boolean
  lastDelivery: string
  value: string
  onChange: (v: string) => void
  onSave: () => void
  saving: boolean
}

function ProviderSecretRow({
  title,
  help,
  set,
  lastDelivery,
  value,
  onChange,
  onSave,
  saving
}: ProviderSecretRowProps) {
  return (
    <div className="border border-[var(--wf-border)] rounded-[var(--wf-radius-md)] p-3 bg-[var(--wf-bg-elevated)]">
      <div className="flex items-center justify-between mb-2">
        <h4 className="font-heading font-semibold text-sm">{title}</h4>
        <span
          className={
            set
              ? 'text-xs px-2 py-0.5 rounded-full bg-green-900/30 text-green-400'
              : 'text-xs px-2 py-0.5 rounded-full bg-[var(--wf-bg-primary)] text-[var(--wf-text-muted)]'
          }
        >
          {set ? 'set' : 'not set'}
        </span>
      </div>
      <p className="text-xs text-[var(--wf-text-muted)] mb-2">{help}</p>
      <div className="flex items-end gap-2">
        <div className="flex-1">
          <Input
            type="password"
            value={value}
            onChange={(e) => onChange(e.target.value)}
            placeholder={set ? 'Enter a new value to rotate…' : 'Paste secret here'}
          />
        </div>
        <Button onClick={onSave} variant="primary" size="sm" disabled={saving || !value}>
          Set
        </Button>
      </div>
      {lastDelivery && (
        <p className="text-xs text-[var(--wf-text-muted)] mt-2">
          Last delivery: <span className="font-mono">{lastDelivery}</span>
        </p>
      )}
    </div>
  )
}
