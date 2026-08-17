import { create } from 'zustand'
import type {
  IntegrationsConfig,
  WebhookIntegration,
  SlackIntegration,
  DiscordIntegration,
  GitHubIntegration,
  TelegramIntegration,
  InboundConfig,
  InboundStatus,
  TestIntegrationResponse
} from '../generated/watchfire_pb'
import { IntegrationKind } from '../generated/watchfire_pb'
import { getIntegrationsClient } from '../lib/grpc-client'
import {
  IDLE_PAIRING,
  applyPairingStatus,
  pairingStarted,
  pairingFailed,
  type PairingView
} from '../lib/telegram-pairing'
import { timestampToMs } from '../lib/relative-time'

// Test result, keyed by `${kind}:${id}`. The detail panel reads it to
// render the inline "Test ✓ HTTP 200" / "Test ✗ HTTP 4xx" status next
// to the Test button. Cleared on Save / Delete so stale results don't
// linger.
export interface IntegrationTestResult {
  ok: boolean
  message: string
  statusCode: number
  testedAt: number // ms since epoch
}

interface IntegrationsStoreState {
  config: IntegrationsConfig | null
  loading: boolean
  saving: boolean
  // testResults is keyed by `${kind}:${id}` so the detail panel can
  // render the inline "Test ✓ HTTP 200" status next to the Test button.
  testResults: Record<string, IntegrationTestResult>
  // v8.0 Echo inbound listener status — populated by `fetchInbound` and
  // refreshed periodically by InboundSection's polling effect.
  inbound: InboundStatus | null
  inboundLoading: boolean
  // v10.0 Torch — Telegram pairing flow view. Pure transitions live in
  // lib/telegram-pairing.ts; the daemon owns the actual lifecycle.
  telegramPairing: PairingView

  fetch: () => Promise<void>
  saveWebhook: (webhook: Partial<WebhookIntegration>) => Promise<void>
  saveSlack: (slack: Partial<SlackIntegration>) => Promise<void>
  saveDiscord: (discord: Partial<DiscordIntegration>) => Promise<void>
  saveGitHub: (github: Partial<GitHubIntegration>) => Promise<void>
  saveTelegram: (telegram: Partial<TelegramIntegration>) => Promise<void>
  remove: (kind: IntegrationKind, id: string) => Promise<void>
  test: (kind: IntegrationKind, id: string) => Promise<TestIntegrationResponse>
  fetchInbound: () => Promise<void>
  saveInbound: (cfg: Partial<InboundConfig>) => Promise<void>
  beginTelegramPairing: () => Promise<void>
  pollTelegramPairing: () => Promise<void>
  resetTelegramPairing: () => void
  revokeTelegramChat: (chatId: bigint) => Promise<void>
}

function testKey(kind: IntegrationKind, id: string): string {
  return `${kind}:${id}`
}

export const useIntegrationsStore = create<IntegrationsStoreState>((set, get) => ({
  config: null,
  loading: false,
  saving: false,
  testResults: {},
  inbound: null,
  inboundLoading: false,
  telegramPairing: IDLE_PAIRING,

  fetch: async () => {
    set({ loading: true })
    try {
      const client = getIntegrationsClient()
      const config = await client.listIntegrations({})
      set({ config, loading: false })
    } catch (err) {
      console.warn('listIntegrations failed', err)
      set({ loading: false })
    }
  },

  saveWebhook: async (webhook) => {
    set({ saving: true })
    try {
      const client = getIntegrationsClient()
      const config = await client.saveIntegration({
        payload: { case: 'webhook', value: webhook as WebhookIntegration }
      } as never)
      set({ config, saving: false, testResults: {} })
    } catch (err) {
      set({ saving: false })
      throw err
    }
  },

  saveSlack: async (slack) => {
    set({ saving: true })
    try {
      const client = getIntegrationsClient()
      const config = await client.saveIntegration({
        payload: { case: 'slack', value: slack as SlackIntegration }
      } as never)
      set({ config, saving: false, testResults: {} })
    } catch (err) {
      set({ saving: false })
      throw err
    }
  },

  saveDiscord: async (discord) => {
    set({ saving: true })
    try {
      const client = getIntegrationsClient()
      const config = await client.saveIntegration({
        payload: { case: 'discord', value: discord as DiscordIntegration }
      } as never)
      set({ config, saving: false, testResults: {} })
    } catch (err) {
      set({ saving: false })
      throw err
    }
  },

  saveGitHub: async (github) => {
    set({ saving: true })
    try {
      const client = getIntegrationsClient()
      const config = await client.saveIntegration({
        payload: { case: 'github', value: github as GitHubIntegration }
      } as never)
      set({ config, saving: false, testResults: {} })
    } catch (err) {
      set({ saving: false })
      throw err
    }
  },

  saveTelegram: async (telegram) => {
    set({ saving: true })
    try {
      const client = getIntegrationsClient()
      const config = await client.saveIntegration({
        payload: { case: 'telegram', value: telegram as TelegramIntegration }
      } as never)
      set({ config, saving: false, testResults: {} })
    } catch (err) {
      set({ saving: false })
      throw err
    }
  },

  remove: async (kind, id) => {
    const client = getIntegrationsClient()
    const config = await client.deleteIntegration({ kind, id })
    set({ config, testResults: {} })
  },

  test: async (kind, id) => {
    const client = getIntegrationsClient()
    const resp = await client.testIntegration({ kind, id })
    const key = testKey(kind, id)
    set({
      testResults: {
        ...get().testResults,
        [key]: {
          ok: resp.ok,
          message: resp.message,
          statusCode: resp.statusCode,
          testedAt: Date.now()
        }
      }
    })
    return resp
  },

  fetchInbound: async () => {
    set({ inboundLoading: true })
    try {
      const client = getIntegrationsClient()
      const inbound = await client.getInboundStatus({})
      set({ inbound, inboundLoading: false })
    } catch (err) {
      console.warn('getInboundStatus failed', err)
      set({ inboundLoading: false })
    }
  },

  saveInbound: async (cfg) => {
    set({ saving: true })
    try {
      const client = getIntegrationsClient()
      const inbound = await client.saveInboundConfig({ config: cfg as InboundConfig } as never)
      set({ inbound, saving: false })
    } catch (err) {
      set({ saving: false })
      throw err
    }
  },

  beginTelegramPairing: async () => {
    set({ telegramPairing: { ...IDLE_PAIRING, phase: 'starting' } })
    try {
      const client = getIntegrationsClient()
      const resp = await client.beginTelegramPairing({})
      set({
        telegramPairing: pairingStarted({
          code: resp.code,
          deepLink: resp.deepLink,
          botUsername: resp.botUsername,
          expiresAtMs: timestampToMs(resp.expiresAt)
        })
      })
    } catch (err) {
      set({ telegramPairing: pairingFailed((err as Error).message) })
    }
  },

  pollTelegramPairing: async () => {
    const prev = get().telegramPairing
    if (prev.phase !== 'pending') return
    try {
      const client = getIntegrationsClient()
      const st = await client.getTelegramPairingStatus({})
      const next = applyPairingStatus(prev, {
        state: st.state,
        expiresAtMs: timestampToMs(st.expiresAt),
        chatUsername: st.chat?.username ?? '',
        chatId: st.chat ? String(st.chat.chatId) : '',
        botUsername: st.botUsername
      })
      set({ telegramPairing: next })
      // A successful pair adds a chat to the config — refresh so the
      // paired-chats table picks it up without a manual reload.
      if (next.phase === 'paired') await get().fetch()
    } catch (err) {
      console.warn('getTelegramPairingStatus failed', err)
    }
  },

  resetTelegramPairing: () => {
    set({ telegramPairing: IDLE_PAIRING })
  },

  revokeTelegramChat: async (chatId) => {
    const client = getIntegrationsClient()
    const config = await client.revokeTelegramChat({ chatId })
    set({ config, testResults: {} })
  }
}))

export { testKey as integrationTestKey }
