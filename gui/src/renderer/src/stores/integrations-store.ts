import { create } from 'zustand'
import type {
  IntegrationsConfig,
  WebhookIntegration,
  SlackIntegration,
  DiscordIntegration,
  GitHubIntegration,
  TestIntegrationResponse
} from '../generated/watchfire_pb'
import { IntegrationKind } from '../generated/watchfire_pb'
import { getIntegrationsClient } from '../lib/grpc-client'

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

  fetch: () => Promise<void>
  saveWebhook: (webhook: Partial<WebhookIntegration>) => Promise<void>
  saveSlack: (slack: Partial<SlackIntegration>) => Promise<void>
  saveDiscord: (discord: Partial<DiscordIntegration>) => Promise<void>
  saveGitHub: (github: Partial<GitHubIntegration>) => Promise<void>
  remove: (kind: IntegrationKind, id: string) => Promise<void>
  test: (kind: IntegrationKind, id: string) => Promise<TestIntegrationResponse>
}

function testKey(kind: IntegrationKind, id: string): string {
  return `${kind}:${id}`
}

export const useIntegrationsStore = create<IntegrationsStoreState>((set, get) => ({
  config: null,
  loading: false,
  saving: false,
  testResults: {},

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
  }
}))

export { testKey as integrationTestKey }
