// Unit tests for the Telegram pairing-flow state machine (v10.0 Torch).
// Imports the real .ts module via node's type stripping, same as
// qr.test.mjs.

import { describe, test } from 'node:test'
import assert from 'node:assert/strict'
import {
  IDLE_PAIRING,
  pairingStarted,
  pairingFailed,
  applyPairingStatus,
  pairingMsLeft,
  formatCountdown,
  shouldPollPairing,
  PAIRING_STATE_NONE,
  PAIRING_STATE_PENDING,
  PAIRING_STATE_PAIRED,
  PAIRING_STATE_EXPIRED
} from './telegram-pairing.ts'

const begin = {
  code: 'AB12CD34',
  deepLink: 'https://t.me/wf_bot?start=AB12CD34',
  botUsername: 'wf_bot',
  expiresAtMs: 1_000_000
}

describe('pairing transitions', () => {
  test('begin → pending carries code, link, bot and expiry', () => {
    const v = pairingStarted(begin)
    assert.equal(v.phase, 'pending')
    assert.equal(v.code, 'AB12CD34')
    assert.equal(v.deepLink, begin.deepLink)
    assert.equal(v.botUsername, 'wf_bot')
    assert.equal(v.expiresAtMs, 1_000_000)
    assert.equal(v.pairedChatId, '')
  })

  test('begin failure → error phase with message', () => {
    const v = pairingFailed('bridge not running')
    assert.equal(v.phase, 'error')
    assert.equal(v.error, 'bridge not running')
  })

  test('pending + PAIRED status → paired with chat identity', () => {
    const v = applyPairingStatus(pairingStarted(begin), {
      state: PAIRING_STATE_PAIRED,
      expiresAtMs: null,
      chatUsername: 'nuno',
      chatId: '12345678901',
      botUsername: 'wf_bot'
    })
    assert.equal(v.phase, 'paired')
    assert.equal(v.pairedUsername, 'nuno')
    assert.equal(v.pairedChatId, '12345678901')
    // The pairing code stays visible for context.
    assert.equal(v.code, 'AB12CD34')
  })

  test('pending + PENDING status refreshes expiry and fills bot username', () => {
    const start = { ...pairingStarted(begin), botUsername: '' }
    const v = applyPairingStatus(start, {
      state: PAIRING_STATE_PENDING,
      expiresAtMs: 2_000_000,
      chatUsername: '',
      chatId: '',
      botUsername: 'wf_bot'
    })
    assert.equal(v.phase, 'pending')
    assert.equal(v.expiresAtMs, 2_000_000)
    assert.equal(v.botUsername, 'wf_bot')
  })

  test('pending + PENDING with no expiry keeps the previous expiry', () => {
    const v = applyPairingStatus(pairingStarted(begin), {
      state: PAIRING_STATE_PENDING,
      expiresAtMs: null,
      chatUsername: '',
      chatId: '',
      botUsername: ''
    })
    assert.equal(v.expiresAtMs, 1_000_000)
  })

  test('pending + EXPIRED status → expired', () => {
    const v = applyPairingStatus(pairingStarted(begin), {
      state: PAIRING_STATE_EXPIRED,
      expiresAtMs: null,
      chatUsername: '',
      chatId: '',
      botUsername: ''
    })
    assert.equal(v.phase, 'expired')
  })

  test('pending + NONE (daemon restarted / swept) → expired', () => {
    const v = applyPairingStatus(pairingStarted(begin), {
      state: PAIRING_STATE_NONE,
      expiresAtMs: null,
      chatUsername: '',
      chatId: '',
      botUsername: ''
    })
    assert.equal(v.phase, 'expired')
  })

  test('idle + NONE stays idle (no phantom expiry on first render)', () => {
    const v = applyPairingStatus(IDLE_PAIRING, {
      state: PAIRING_STATE_NONE,
      expiresAtMs: null,
      chatUsername: '',
      chatId: '',
      botUsername: ''
    })
    assert.equal(v.phase, 'idle')
  })

  test('paired is terminal for NONE updates (post-pair sweep must not demote)', () => {
    const paired = applyPairingStatus(pairingStarted(begin), {
      state: PAIRING_STATE_PAIRED,
      expiresAtMs: null,
      chatUsername: 'nuno',
      chatId: '1',
      botUsername: ''
    })
    const after = applyPairingStatus(paired, {
      state: PAIRING_STATE_NONE,
      expiresAtMs: null,
      chatUsername: '',
      chatId: '',
      botUsername: ''
    })
    assert.equal(after.phase, 'paired')
  })
})

describe('countdown helpers', () => {
  test('pairingMsLeft clamps at zero and handles null', () => {
    assert.equal(pairingMsLeft(1_000, 400), 600)
    assert.equal(pairingMsLeft(1_000, 5_000), 0)
    assert.equal(pairingMsLeft(null, 5_000), 0)
  })

  test('formatCountdown renders m:ss', () => {
    assert.equal(formatCountdown(0), '0:00')
    assert.equal(formatCountdown(1_000), '0:01')
    assert.equal(formatCountdown(59_400), '1:00') // ceil to whole seconds
    assert.equal(formatCountdown(125_000), '2:05')
    assert.equal(formatCountdown(600_000), '10:00')
  })

  test('only pending polls', () => {
    assert.equal(shouldPollPairing('pending'), true)
    for (const phase of ['idle', 'starting', 'paired', 'expired', 'error']) {
      assert.equal(shouldPollPairing(phase), false, phase)
    }
  })
})
