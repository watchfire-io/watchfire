// Source-level smoke test for the v7.0 Relay integrations settings UI.
// Mirrors the pattern from digest-markdown.test.mjs — loads the .tsx
// files as raw text and runs string-level assertions on the exports
// and key hook calls. We don't exercise JSX rendering here because
// node --test doesn't have Vite / JSX transform infra in the project.
//
// What it pins:
//   - IntegrationsSection.tsx exports IntegrationsSection
//   - per-type detail panels exist (Webhook / Slack / Discord / GitHub)
//   - the section pulls config through useIntegrationsStore
//   - the per-project Auto-PR pill is wired into ProjectView.tsx
//   - the Zustand store exposes the four save methods + fetch + remove + test

import { describe, test } from 'node:test'
import { strict as assert } from 'node:assert'
import { readFileSync, existsSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const __filename = fileURLToPath(import.meta.url)
const __dirname = dirname(__filename)

// Test file lives alongside other store tests at one-level depth so the
// npm `test` glob picks it up; resolve everything relative to repo
// roots from here.
const settingsDir = join(__dirname, '..', 'views', 'Settings')
const integrationsDir = join(settingsDir, 'integrations')

function readSrc(...segs) {
  return readFileSync(join(settingsDir, ...segs), 'utf-8')
}

describe('IntegrationsSection (v7.0 Relay)', () => {
  test('section exports IntegrationsSection and uses the integrations store', () => {
    const src = readSrc('IntegrationsSection.tsx')
    assert.match(src, /export function IntegrationsSection/)
    assert.match(src, /useIntegrationsStore/)
    // Picker dropdown enumerates all four types.
    assert.match(src, /Webhook/)
    assert.match(src, /Slack/)
    assert.match(src, /Discord/)
    assert.match(src, /GitHub Auto-PR/)
  })

  test('per-type detail panels exist and export', () => {
    const panels = ['WebhookDetail', 'SlackDetail', 'DiscordDetail', 'GitHubDetail']
    for (const p of panels) {
      const path = join(integrationsDir, `${p}.tsx`)
      assert.equal(existsSync(path), true, `${p}.tsx must exist`)
      const src = readFileSync(path, 'utf-8')
      assert.match(src, new RegExp(`export function ${p}`))
    }
  })

  test('Webhook + Slack + Discord detail panels render the EventCheckboxes', () => {
    for (const p of ['WebhookDetail', 'SlackDetail', 'DiscordDetail']) {
      const src = readFileSync(join(integrationsDir, `${p}.tsx`), 'utf-8')
      assert.match(src, /EventCheckboxes/, `${p} should render EventCheckboxes`)
      assert.match(src, /ProjectMuteSelect/, `${p} should render ProjectMuteSelect`)
    }
  })

  test('Slack + Discord URL fields are write-only (type=password)', () => {
    for (const p of ['SlackDetail', 'DiscordDetail']) {
      const src = readFileSync(join(integrationsDir, `${p}.tsx`), 'utf-8')
      assert.match(src, /type="password"/, `${p} URL field must be password-style (write-only)`)
    }
  })

  test('GitHub detail does not expose a URL field — relies on gh CLI auth', () => {
    const src = readFileSync(join(integrationsDir, 'GitHubDetail.tsx'), 'utf-8')
    assert.doesNotMatch(src, /Webhook URL/)
    assert.match(src, /gh.*CLI/)
  })

  test('GlobalSettings.tsx mounts the IntegrationsSection sibling-of NotificationsSection', () => {
    const src = readSrc('GlobalSettings.tsx')
    assert.match(src, /IntegrationsSection/)
    assert.match(src, /NotificationsSection/, 'NotificationsSection must remain mounted alongside the new sibling')
  })

  test('NotificationsSection is unmodified by this task', () => {
    // Lock that this task does not touch the v5.0 Pulse component.
    const src = readSrc('NotificationsSection.tsx')
    assert.doesNotMatch(src, /Integrations/)
  })

  test('integrations-store exports the four save methods + fetch + remove + test', () => {
    const src = readFileSync(join(__dirname, 'integrations-store.ts'), 'utf-8')
    for (const method of ['fetch', 'saveWebhook', 'saveSlack', 'saveDiscord', 'saveGitHub', 'remove', 'test']) {
      assert.match(src, new RegExp(`\\b${method}\\b`), `store should expose ${method}`)
    }
  })

  test('ProjectView wires the Auto-PR pill into the project header', () => {
    const src = readFileSync(
      join(__dirname, '..', 'views', 'ProjectView', 'ProjectView.tsx'),
      'utf-8'
    )
    assert.match(src, /useIntegrationsStore/, 'ProjectView must read the integrations store')
    assert.match(src, /Auto-PR/, 'project header must surface an Auto-PR pill')
    assert.match(src, /autoPRApplies/, 'header must gate the pill on autoPRApplies')
  })
})
