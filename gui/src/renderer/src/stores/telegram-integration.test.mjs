// Source-level smoke test for the v10.0 Torch Telegram settings panel.
// Mirrors integrations.test.mjs: loads the .tsx/.ts files as text and
// asserts the exports + wiring, since node --test has no JSX infra.
// The pure logic (QR encoder, pairing state machine) has real unit
// tests in lib/qr.test.mjs and lib/telegram-pairing.test.mjs.

import { describe, test } from 'node:test'
import { strict as assert } from 'node:assert'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const __filename = fileURLToPath(import.meta.url)
const __dirname = dirname(__filename)

const srcRoot = join(__dirname, '..')
const read = (...segs) => readFileSync(join(srcRoot, ...segs), 'utf-8')

describe('TelegramDetail panel (v10.0 Torch)', () => {
  const src = read('views', 'Settings', 'integrations', 'TelegramDetail.tsx')

  test('exports the panel and renders the standard integration controls', () => {
    assert.match(src, /export function TelegramDetail/)
    assert.match(src, /EventCheckboxes/)
    assert.match(src, /Toggle/)
  })

  test('bot token is write-only: password field, "unchanged" placeholder, never echoed', () => {
    assert.match(src, /type="password"/)
    assert.match(src, /'unchanged'/)
    // The token input must be bound to local state, never to a value
    // coming back from the daemon.
    assert.doesNotMatch(src, /value=\{initial/)
    assert.doesNotMatch(src, /initial\?\.botToken/)
    assert.doesNotMatch(src, /initial\.botToken/)
  })

  test('QR is generated locally on a canvas — no external encoder or CDN', () => {
    assert.match(src, /from '\.\.\/\.\.\/\.\.\/lib\/qr'/)
    assert.match(src, /<canvas/)
    // No runtime network fetches; the only http(s) URLs allowed are
    // user-clickable <a href> docs/deep links.
    assert.doesNotMatch(src, /fetch\(/)
    assert.doesNotMatch(src, /XMLHttpRequest/)
    assert.doesNotMatch(src, /<img[^>]*src="http/)
    assert.doesNotMatch(src, /qrserver|quickchart|googleapis/)
  })

  test('pair flow: begin + status polling + countdown + deep link copy', () => {
    assert.match(src, /beginTelegramPairing/)
    assert.match(src, /pollTelegramPairing/)
    assert.match(src, /setInterval/)
    assert.match(src, /formatCountdown/)
    assert.match(src, /clipboard\.writeText/)
  })

  test('paired-chats table: per-chat toggles persist and revoke is confirm-gated', () => {
    assert.match(src, /muted:\s*v/)
    assert.match(src, /watch:\s*v/)
    assert.match(src, /revokeTelegramChat|revokeChat/)
    assert.match(src, /window\.confirm/)
  })

  test('renders sanely without a config (initial is optional)', () => {
    assert.match(src, /initial\?: TelegramIntegration/)
  })
})

describe('IntegrationsSection wiring', () => {
  const src = read('views', 'Settings', 'IntegrationsSection.tsx')

  test('Telegram joins the kind union, dispatch, card and add-picker', () => {
    assert.match(src, /\{ kind: IntegrationKind\.TELEGRAM \}/)
    assert.match(src, /TelegramDetail/)
    assert.match(src, /label: 'Telegram'/)
    // Existing entries stay.
    for (const label of ['Webhook', 'Slack', 'Discord', 'GitHub Auto-PR']) {
      assert.match(src, new RegExp(label), `picker should still offer ${label}`)
    }
  })

  test('card is always listed with an explicit setup CTA / configured state', () => {
    // v10 follow-up: hiding the card until the daemon reported a telegram
    // config buried setup in the Add-integration picker. The card is now a
    // permanent singleton entry; TelegramDetail renders its blank state for
    // an unset config (old daemons included), so this stays crash-free.
    assert.match(src, /Set up Telegram/)
    assert.match(src, /Configured ✓/)
    assert.match(src, /telegramConfigured/)
  })
})

describe('integrations store — telegram actions', () => {
  const src = read('stores', 'integrations-store.ts')

  test('exposes save/pairing/revoke actions and pairing view state', () => {
    for (const member of [
      'saveTelegram',
      'beginTelegramPairing',
      'pollTelegramPairing',
      'resetTelegramPairing',
      'revokeTelegramChat',
      'telegramPairing'
    ]) {
      assert.match(src, new RegExp(`\\b${member}\\b`), `store should expose ${member}`)
    }
  })

  test('telegram save goes through the SaveIntegration oneof', () => {
    assert.match(src, /case: 'telegram'/)
  })

  test('pairing transitions come from the pure lib module', () => {
    assert.match(src, /from '\.\.\/lib\/telegram-pairing'/)
  })
})

describe('settings search', () => {
  const src = read('views', 'Settings', 'searchIndex.ts')

  test('telegram / pair / bot token entries point at the integrations panel', () => {
    assert.match(src, /integrations-telegram/)
    assert.match(src, /'pairing'/)
    assert.match(src, /'bot token'/)
  })
})
