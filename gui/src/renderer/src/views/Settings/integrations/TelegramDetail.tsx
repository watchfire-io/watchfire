import { useEffect, useRef, useState } from 'react'
import type { TelegramIntegration, TelegramPairedChatInfo } from '../../../generated/watchfire_pb'
import { IntegrationKind } from '../../../generated/watchfire_pb'
import { useIntegrationsStore, integrationTestKey } from '../../../stores/integrations-store'
import { useProjectsStore } from '../../../stores/projects-store'
import { useToast } from '../../../components/ui/Toast'
import { Button } from '../../../components/ui/Button'
import { Input } from '../../../components/ui/Input'
import { Toggle } from '../../../components/ui/Toggle'
import { EventCheckboxes } from './EventCheckboxes'
import { encodeQr } from '../../../lib/qr'
import {
  formatCountdown,
  pairingMsLeft,
  shouldPollPairing
} from '../../../lib/telegram-pairing'
import { timestampToMs } from '../../../lib/relative-time'

interface Props {
  /** Undefined when Telegram is not configured yet (or the daemon predates it). */
  initial?: TelegramIntegration
  onClose: () => void
}

// Local QR renderer — the matrix comes from lib/qr.ts, no network, no
// external encoder. Fixed black-on-white regardless of theme: scanners
// need the contrast, so the canvas sits on its own white card.
function QrCanvas({ text }: { text: string }) {
  const ref = useRef<HTMLCanvasElement>(null)

  useEffect(() => {
    const canvas = ref.current
    if (!canvas || !text) return
    let qr
    try {
      qr = encodeQr(text)
    } catch (err) {
      console.warn('QR encode failed', err)
      return
    }
    const moduleSize = 4
    const quiet = 4
    const px = (qr.size + quiet * 2) * moduleSize
    canvas.width = px
    canvas.height = px
    const ctx = canvas.getContext('2d')
    if (!ctx) return
    ctx.fillStyle = '#ffffff'
    ctx.fillRect(0, 0, px, px)
    ctx.fillStyle = '#000000'
    for (let y = 0; y < qr.size; y++) {
      for (let x = 0; x < qr.size; x++) {
        if (qr.modules[y][x]) {
          ctx.fillRect((x + quiet) * moduleSize, (y + quiet) * moduleSize, moduleSize, moduleSize)
        }
      }
    }
  }, [text])

  return (
    <canvas
      ref={ref}
      className="rounded-[var(--wf-radius-md)] border border-[var(--wf-border)]"
      aria-label="Telegram pairing QR code"
    />
  )
}

export function TelegramDetail({ initial, onClose }: Props) {
  const saveTelegram = useIntegrationsStore((s) => s.saveTelegram)
  const remove = useIntegrationsStore((s) => s.remove)
  const test = useIntegrationsStore((s) => s.test)
  const testResult = useIntegrationsStore(
    (s) => s.testResults[integrationTestKey(IntegrationKind.TELEGRAM, '')]
  )
  const pairing = useIntegrationsStore((s) => s.telegramPairing)
  const beginPairing = useIntegrationsStore((s) => s.beginTelegramPairing)
  const pollPairing = useIntegrationsStore((s) => s.pollTelegramPairing)
  const resetPairing = useIntegrationsStore((s) => s.resetTelegramPairing)
  const revokeChat = useIntegrationsStore((s) => s.revokeTelegramChat)
  const projects = useProjectsStore((s) => s.projects)
  const { toast } = useToast()

  const [enabled, setEnabled] = useState(initial?.enabled ?? false)
  const [token, setToken] = useState('')
  const [events, setEvents] = useState({
    taskFailed: initial?.enabledEvents?.taskFailed ?? true,
    runComplete: initial?.enabledEvents?.runComplete ?? true,
    weeklyDigest: initial?.enabledEvents?.weeklyDigest ?? false
  })
  const [testing, setTesting] = useState(false)
  const [nowMs, setNowMs] = useState(() => Date.now())

  const tokenSet = initial?.tokenSet ?? false
  const pairedChats = initial?.pairedChats ?? []

  useEffect(() => {
    setEnabled(initial?.enabled ?? false)
    setToken('')
  }, [initial?.enabled, initial?.tokenSet])

  // Poll GetTelegramPairingStatus while a pairing is in flight; the
  // 1s tick below keeps the countdown moving between polls.
  useEffect(() => {
    if (!shouldPollPairing(pairing.phase)) return
    const t = setInterval(() => {
      void pollPairing()
    }, 2000)
    return () => clearInterval(t)
  }, [pairing.phase, pollPairing])

  useEffect(() => {
    if (pairing.phase !== 'pending') return
    const t = setInterval(() => setNowMs(Date.now()), 1000)
    return () => clearInterval(t)
  }, [pairing.phase])

  // Closing the panel abandons the local pairing view (the daemon's
  // expiry sweep reclaims the code on its own).
  useEffect(() => () => resetPairing(), [resetPairing])

  const handleSave = async () => {
    try {
      await saveTelegram({
        enabled,
        // Write-only convention: empty means "keep the stored token".
        botToken: token,
        enabledEvents: { ...events } as never,
        pairedChats
      } as never)
      toast('Telegram integration saved', 'success')
      onClose()
    } catch (err) {
      toast(`Save failed: ${(err as Error).message}`, 'error')
    }
  }

  const handleDelete = async () => {
    if (!initial) {
      onClose()
      return
    }
    if (!window.confirm('Remove the Telegram integration? Paired chats and the stored bot token are deleted.')) return
    try {
      await remove(IntegrationKind.TELEGRAM, '')
      toast('Telegram integration removed', 'success')
      onClose()
    } catch (err) {
      toast(`Delete failed: ${(err as Error).message}`, 'error')
    }
  }

  const handleTest = async () => {
    setTesting(true)
    try {
      await test(IntegrationKind.TELEGRAM, '')
    } finally {
      setTesting(false)
    }
  }

  const handleCopyLink = async () => {
    try {
      await navigator.clipboard.writeText(pairing.deepLink)
      toast('Deep link copied', 'success')
    } catch {
      toast('Copy failed', 'error')
    }
  }

  // Per-chat toggles persist immediately through SaveIntegration. The
  // daemon merges muted/watch/default-project by chat id and keeps the
  // stored token (empty bot_token = keep), so only the flipped flag
  // changes; enabled/events are sent from the server-side state to
  // avoid persisting unsaved form edits as a side effect.
  const persistChat = async (chatId: bigint, patch: Partial<TelegramPairedChatInfo>) => {
    if (!initial) return
    try {
      await saveTelegram({
        enabled: initial.enabled,
        botToken: '',
        enabledEvents: initial.enabledEvents,
        pairedChats: pairedChats.map((c) =>
          c.chatId === chatId ? ({ ...c, ...patch } as TelegramPairedChatInfo) : c
        )
      } as never)
    } catch (err) {
      toast(`Update failed: ${(err as Error).message}`, 'error')
    }
  }

  const handleRevoke = async (chat: TelegramPairedChatInfo) => {
    const who = chat.username ? `@${chat.username}` : `chat ${chat.chatId}`
    if (!window.confirm(`Revoke ${who}? The chat loses access immediately and must re-pair to return.`)) return
    try {
      await revokeChat(chat.chatId)
      toast(`${who} revoked`, 'success')
    } catch (err) {
      toast(`Revoke failed: ${(err as Error).message}`, 'error')
    }
  }

  const projectName = (id: string): string => {
    if (!id) return '—'
    return projects.find((p) => p.projectId === id)?.name ?? id
  }

  const pairedDate = (chat: TelegramPairedChatInfo): string => {
    const ms = timestampToMs(chat.pairedAt)
    return ms === null ? '—' : new Date(ms).toLocaleDateString()
  }

  const canPair = tokenSet && (initial?.enabled ?? false)

  const renderPairing = () => {
    switch (pairing.phase) {
      case 'starting':
        return <p className="text-xs text-[var(--wf-text-muted)]">Requesting pairing code…</p>
      case 'pending': {
        const msLeft = pairingMsLeft(pairing.expiresAtMs, nowMs)
        return (
          <div className="flex gap-4 items-start">
            <QrCanvas text={pairing.deepLink} />
            <div className="space-y-2 min-w-0">
              <p className="text-xs text-[var(--wf-text-muted)]">
                Scan the QR code or open the link, then send the code to
                {pairing.botUsername ? ` @${pairing.botUsername}` : ' the bot'}. /start via the
                link sends it for you.
              </p>
              <div className="font-mono text-lg tracking-widest text-[var(--wf-text-primary)]">
                {pairing.code}
              </div>
              <div className="flex items-center gap-2 min-w-0">
                <a
                  href={pairing.deepLink}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-xs text-fire-500 hover:underline truncate font-mono"
                >
                  {pairing.deepLink}
                </a>
                <Button onClick={handleCopyLink} variant="ghost" size="sm">
                  Copy
                </Button>
              </div>
              <p className="text-xs text-[var(--wf-text-secondary)]">
                Code expires in{' '}
                <span className="font-mono">{formatCountdown(msLeft)}</span>
              </p>
              <Button onClick={resetPairing} variant="ghost" size="sm">
                Cancel pairing
              </Button>
            </div>
          </div>
        )
      }
      case 'paired':
        return (
          <div className="flex items-center gap-3">
            <span className="text-sm text-green-500">
              ✓ Paired{pairing.pairedUsername ? ` with @${pairing.pairedUsername}` : ''}
            </span>
            <Button onClick={resetPairing} variant="ghost" size="sm">
              Done
            </Button>
          </div>
        )
      case 'expired':
        return (
          <div className="flex items-center gap-3">
            <span className="text-sm text-[var(--wf-text-muted)]">Pairing code expired.</span>
            <Button onClick={() => void beginPairing()} variant="secondary" size="sm">
              Pair again
            </Button>
          </div>
        )
      case 'error':
        return (
          <div className="flex items-center gap-3">
            <span className="text-xs text-red-500">{pairing.error}</span>
            <Button onClick={() => void beginPairing()} variant="secondary" size="sm">
              Retry
            </Button>
          </div>
        )
      default:
        return (
          <div className="flex items-center gap-3">
            <Button
              onClick={() => void beginPairing()}
              variant="secondary"
              size="sm"
              disabled={!canPair}
            >
              Pair a chat
            </Button>
            {!canPair && (
              <span className="text-xs text-[var(--wf-text-muted)]">
                Save the integration enabled with a bot token first.
              </span>
            )}
          </div>
        )
    }
  }

  return (
    <div className="space-y-4 border border-[var(--wf-border)] rounded-[var(--wf-radius-md)] p-4 bg-[var(--wf-bg-elevated)]">
      <h4 className="font-heading font-semibold text-sm">Telegram</h4>
      <p className="text-xs text-[var(--wf-text-muted)]">
        Notifications and read-only commands over a Telegram bot. Only chats paired with a
        one-time code can talk to it.
      </p>

      <Toggle
        checked={enabled}
        onChange={setEnabled}
        label="Enabled"
        description="Run the bridge and fan out enabled events to paired chats"
      />

      <Input
        label={tokenSet ? 'Bot token — leave blank to keep' : 'Bot token (from @BotFather)'}
        type="password"
        value={token}
        onChange={(e) => setToken(e.target.value)}
        placeholder={tokenSet ? 'unchanged' : '123456789:AA…'}
      />
      <a
        href="https://core.telegram.org/bots/features#botfather"
        target="_blank"
        rel="noopener noreferrer"
        className="text-xs text-fire-500 hover:underline"
      >
        How to create a bot with @BotFather →
      </a>

      <EventCheckboxes value={events} onChange={setEvents} />

      <div className="flex items-center gap-2 pt-2">
        <Button onClick={handleSave} variant="primary" size="sm">
          Save
        </Button>
        {initial && (
          <>
            <Button onClick={handleTest} variant="secondary" size="sm" disabled={testing || !tokenSet}>
              {testing ? 'Testing…' : 'Test'}
            </Button>
            <Button onClick={handleDelete} variant="danger" size="sm">
              Delete
            </Button>
          </>
        )}
        <Button onClick={onClose} variant="ghost" size="sm">
          Cancel
        </Button>
      </div>

      {testResult && (
        <div className="text-xs space-y-0.5">
          {/* The daemon reports one result per chat, joined with " · ". */}
          {testResult.message.split(' · ').map((line, i) => (
            <div key={i} className={testResult.ok ? 'text-green-500' : 'text-red-500'}>
              {testResult.ok ? '✓' : '✗'} {line}
            </div>
          ))}
        </div>
      )}

      {initial && (
        <div className="space-y-2 pt-2 border-t border-[var(--wf-border)]">
          <h5 className="text-xs font-semibold uppercase tracking-wider text-[var(--wf-text-muted)]">
            Pairing
          </h5>
          {renderPairing()}
        </div>
      )}

      {pairedChats.length > 0 && (
        <div className="space-y-2 pt-2 border-t border-[var(--wf-border)]">
          <h5 className="text-xs font-semibold uppercase tracking-wider text-[var(--wf-text-muted)]">
            Paired chats
          </h5>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-xs text-[var(--wf-text-muted)]">
                  <th className="font-medium py-1 pr-3">Chat</th>
                  <th className="font-medium py-1 pr-3">Paired</th>
                  <th className="font-medium py-1 pr-3">Default project</th>
                  <th className="font-medium py-1 pr-3">Muted</th>
                  <th className="font-medium py-1 pr-3">Watch</th>
                  <th className="py-1" />
                </tr>
              </thead>
              <tbody>
                {pairedChats.map((chat) => (
                  <tr key={String(chat.chatId)} className="border-t border-[var(--wf-border)]">
                    <td className="py-1.5 pr-3">
                      <div className="text-[var(--wf-text-primary)]">
                        {chat.username ? `@${chat.username}` : '(no username)'}
                      </div>
                      <div className="text-xs text-[var(--wf-text-muted)] font-mono">
                        {String(chat.chatId)}
                      </div>
                    </td>
                    <td className="py-1.5 pr-3 text-xs text-[var(--wf-text-secondary)]">
                      {pairedDate(chat)}
                    </td>
                    <td className="py-1.5 pr-3 text-xs text-[var(--wf-text-secondary)]">
                      {projectName(chat.defaultProjectId)}
                    </td>
                    <td className="py-1.5 pr-3">
                      <Toggle
                        checked={chat.muted}
                        onChange={(v) => void persistChat(chat.chatId, { muted: v })}
                      />
                    </td>
                    <td className="py-1.5 pr-3">
                      <Toggle
                        checked={chat.watch}
                        onChange={(v) => void persistChat(chat.chatId, { watch: v })}
                      />
                    </td>
                    <td className="py-1.5 text-right">
                      <Button onClick={() => void handleRevoke(chat)} variant="danger" size="sm">
                        Revoke
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  )
}
