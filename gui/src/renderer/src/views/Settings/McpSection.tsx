import { useCallback, useEffect, useState } from 'react'
import { Check, Copy, RefreshCw } from 'lucide-react'
import type { McpClientStatus } from '../../generated/watchfire_pb'
import { getSettingsClient } from '../../lib/grpc-client'
import { useToast } from '../../components/ui/Toast'
import { Button } from '../../components/ui/Button'

// MCP onboarding panel (v9.0 Firestorm).
//
// The panel holds no MCP truth of its own: every badge, path and instruction
// block below is a verbatim copy of what SettingsService.GetMcpClientStatus /
// InstallMcpClient returned. Nothing here reads or writes a harness config
// directly — the daemon owns that (it runs as the same user, so it can write
// the same user-level files `watchfire mcp install` does).
export function McpSection() {
  const { toast } = useToast()

  const [clients, setClients] = useState<McpClientStatus[]>([])
  const [customSnippet, setCustomSnippet] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  // Client key with an InstallMcpClient RPC in flight. One at a time — the
  // daemon writes user-level config files on our behalf.
  const [installing, setInstalling] = useState<string | null>(null)
  // Per-client install outcome. For a harness that could not be configured
  // automatically this is the manual instructions + snippet, which is why it
  // renders inline rather than as a transient error toast.
  const [messages, setMessages] = useState<Record<string, string>>({})

  const refresh = useCallback(async () => {
    setLoading(true)
    try {
      const res = await getSettingsClient().getMcpClientStatus({})
      setClients(res.clients)
      setCustomSnippet(res.customSnippet)
      setError('')
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    refresh()
  }, [refresh])

  const handleInstall = async (client: McpClientStatus) => {
    const key = client.client
    setInstalling(key)
    setMessages((prev) => {
      const next = { ...prev }
      delete next[key]
      return next
    })
    try {
      const st = await getSettingsClient().installMcpClient({
        meta: { origin: 'gui' },
        client: key
      })
      // Optimistic splice so the row settles before the refresh lands.
      setClients((prev) => prev.map((c) => (c.client === key ? st : c)))
      setMessages((prev) => ({ ...prev, [key]: st.message }))
      if (st.configured) {
        toast(`${st.displayName} configured`, 'success')
      } else {
        toast(`${st.displayName} needs manual setup — see the instructions below`, 'info')
      }
    } catch (err) {
      // Only an unknown client key is a gRPC error; every install problem comes
      // back as a normal response carrying manual instructions. Render whatever
      // we got inline all the same.
      setMessages((prev) => ({ ...prev, [key]: (err as Error).message }))
    } finally {
      setInstalling(null)
      refresh()
    }
  }

  const handleCopySnippet = async () => {
    if (!customSnippet) return
    try {
      await navigator.clipboard.writeText(customSnippet)
      toast('Snippet copied to clipboard', 'success')
    } catch {
      toast('Copy failed — clipboard unavailable', 'error')
    }
  }

  return (
    <section>
      <h3 className="font-heading font-semibold text-sm text-[var(--wf-text-muted)] uppercase tracking-wider mb-1">
        MCP Server
      </h3>
      <p className="text-xs text-[var(--wf-text-muted)] mb-4">
        Watchfire can act as a local MCP server so coding agents — Claude Code, Codex, Gemini CLI,
        opencode, Copilot CLI — can create and run Watchfire tasks themselves. Connections are{' '}
        <span className="font-medium text-[var(--wf-text-secondary)]">stdio-only and host-local</span>:
        nothing is exposed on the network.
      </p>

      {error && (
        <div
          className="mb-4 flex items-start justify-between gap-3 border border-red-700/40 bg-red-900/20 rounded-[var(--wf-radius-md)] p-3"
          role="alert"
        >
          <p className="text-xs text-red-400">Couldn&apos;t load MCP client status: {error}</p>
          <Button variant="secondary" size="sm" onClick={refresh} disabled={loading}>
            <RefreshCw size={12} className={loading ? 'animate-spin' : ''} />
            Retry
          </Button>
        </div>
      )}

      {loading && clients.length === 0 && !error && (
        <p className="text-xs text-[var(--wf-text-muted)] italic mb-4">Loading MCP client status…</p>
      )}

      <div className="space-y-3 mb-4" data-setting-field-id="mcp-clients">
        {clients.map((c) => (
          <McpClientCard
            key={c.client}
            client={c}
            installing={installing === c.client}
            busy={installing !== null}
            message={messages[c.client] ?? ''}
            onInstall={() => handleInstall(c)}
          />
        ))}
      </div>

      <div
        className="border border-[var(--wf-border)] rounded-[var(--wf-radius-md)] p-3 bg-[var(--wf-bg-elevated)]"
        data-setting-field-id="mcp-custom-snippet"
      >
        <div className="flex items-center justify-between gap-3 mb-1">
          <h4 className="font-heading font-semibold text-sm">Custom</h4>
          <Button
            variant="secondary"
            size="sm"
            onClick={handleCopySnippet}
            disabled={!customSnippet}
          >
            <Copy size={12} />
            Copy
          </Button>
        </div>
        <p className="text-xs text-[var(--wf-text-muted)] mb-2">
          For any other MCP client — paste this into its server config.{' '}
          <code className="font-mono">watchfire mcp install --print</code> prints the same block from
          the CLI.
        </p>
        <pre className="font-mono text-xs whitespace-pre-wrap break-all text-[var(--wf-text-secondary)] bg-[var(--wf-bg-primary)] border border-[var(--wf-border)] rounded-[var(--wf-radius-md)] p-2 overflow-x-auto">
          {customSnippet || '—'}
        </pre>
      </div>
    </section>
  )
}

interface McpClientCardProps {
  client: McpClientStatus
  installing: boolean
  busy: boolean
  message: string
  onInstall: () => void
}

// One harness row: display name + state badge, config path, the daemon's
// explanation of what installing would do, and the action. A harness that
// isn't on this machine gets a disabled button with a tooltip plus explicit
// manual steps, rather than a live button that can only dead-end.
function McpClientCard({ client, installing, busy, message, onInstall }: McpClientCardProps) {
  const { configured, detected, displayName, configPath } = client
  // Prefer the post-install message when we have one; otherwise the read-only
  // status line the daemon sent with GetMcpClientStatus.
  const detail = message || client.message

  return (
    <div className="border border-[var(--wf-border)] rounded-[var(--wf-radius-md)] p-3 bg-[var(--wf-bg-elevated)]">
      <div className="flex items-center justify-between gap-3 mb-2">
        <div className="flex items-center gap-2 min-w-0">
          <h4 className="font-heading font-semibold text-sm truncate">{displayName}</h4>
          <StateBadge configured={configured} detected={detected} />
        </div>
        {configured ? (
          <Button
            variant="ghost"
            size="sm"
            className="shrink-0"
            onClick={onInstall}
            disabled={busy}
            title="Re-write the watchfire entry into this client's config. Safe to repeat."
          >
            {installing ? 'Reinstalling…' : 'Reinstall'}
          </Button>
        ) : detected ? (
          <Button
            variant="primary"
            size="sm"
            className="shrink-0"
            onClick={onInstall}
            disabled={busy}
          >
            {installing ? (
              <>
                <RefreshCw size={12} className="animate-spin" />
                Installing…
              </>
            ) : (
              'Install'
            )}
          </Button>
        ) : (
          // The button carries `disabled:pointer-events-none`, so the tooltip
          // lives on the wrapper — otherwise hovering a dead button says
          // nothing at all.
          <span
            className="shrink-0"
            title={`${displayName} was not found on this machine — set it up manually instead.`}
          >
            <Button variant="secondary" size="sm" disabled>
              Install
            </Button>
          </span>
        )}
      </div>

      {configPath && (
        <p className="text-xs font-mono text-[var(--wf-text-muted)] break-all mb-1">{configPath}</p>
      )}
      {detail && (
        <pre className="text-xs text-[var(--wf-text-muted)] whitespace-pre-wrap break-words font-sans">
          {detail}
        </pre>
      )}
      {!detected && !configured && (
        <p className="text-xs text-[var(--wf-text-muted)] mt-1">
          Manual setup: install {displayName}, then paste the{' '}
          <span className="font-medium text-[var(--wf-text-secondary)]">Custom</span> snippet below
          into {configPath || 'its MCP server config'}.
        </p>
      )}
    </div>
  )
}

function StateBadge({ configured, detected }: { configured: boolean; detected: boolean }) {
  if (configured) {
    return (
      <span className="shrink-0 inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded-full bg-green-900/30 text-green-400">
        <Check size={11} />
        Configured
      </span>
    )
  }
  if (detected) {
    return (
      <span className="shrink-0 text-xs px-2 py-0.5 rounded-full bg-fire-500/15 text-fire-500">
        Detected
      </span>
    )
  }
  return (
    <span className="shrink-0 text-xs px-2 py-0.5 rounded-full bg-[var(--wf-bg-primary)] text-[var(--wf-text-muted)]">
      Not detected
    </span>
  )
}
