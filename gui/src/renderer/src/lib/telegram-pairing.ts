// Pure pairing-flow state machine for the v10.0 Torch Telegram panel.
// The daemon is the authority on pairing lifecycle (single active code,
// expiry sweep) — this module only folds Begin/GetStatus responses into
// a view the panel can render, so the transitions are unit-testable
// without React or gRPC.

// Numeric mirror of proto TelegramPairingState (kept as plain numbers
// so this module has no generated-code import and stays loadable from
// node --test).
export const PAIRING_STATE_NONE = 0
export const PAIRING_STATE_PENDING = 1
export const PAIRING_STATE_PAIRED = 2
export const PAIRING_STATE_EXPIRED = 3

export type PairingPhase = 'idle' | 'starting' | 'pending' | 'paired' | 'expired' | 'error'

export interface PairingView {
  phase: PairingPhase
  code: string
  deepLink: string
  botUsername: string
  expiresAtMs: number | null
  pairedUsername: string
  /** Stringified chat id (int64 doesn't fit a JS number). */
  pairedChatId: string
  error: string
}

export const IDLE_PAIRING: PairingView = {
  phase: 'idle',
  code: '',
  deepLink: '',
  botUsername: '',
  expiresAtMs: null,
  pairedUsername: '',
  pairedChatId: '',
  error: ''
}

export interface PairingBegin {
  code: string
  deepLink: string
  botUsername: string
  expiresAtMs: number | null
}

export function pairingStarted(begin: PairingBegin): PairingView {
  return {
    ...IDLE_PAIRING,
    phase: 'pending',
    code: begin.code,
    deepLink: begin.deepLink,
    botUsername: begin.botUsername,
    expiresAtMs: begin.expiresAtMs
  }
}

export function pairingFailed(message: string): PairingView {
  return { ...IDLE_PAIRING, phase: 'error', error: message }
}

export interface PairingStatusUpdate {
  state: number
  expiresAtMs: number | null
  chatUsername: string
  chatId: string
  botUsername: string
}

/** Fold a GetTelegramPairingStatus poll result into the current view. */
export function applyPairingStatus(prev: PairingView, status: PairingStatusUpdate): PairingView {
  switch (status.state) {
    case PAIRING_STATE_PAIRED:
      return {
        ...prev,
        phase: 'paired',
        pairedUsername: status.chatUsername,
        pairedChatId: status.chatId,
        error: ''
      }
    case PAIRING_STATE_EXPIRED:
      return { ...prev, phase: 'expired' }
    case PAIRING_STATE_PENDING:
      return {
        ...prev,
        phase: 'pending',
        expiresAtMs: status.expiresAtMs ?? prev.expiresAtMs,
        botUsername: status.botUsername || prev.botUsername
      }
    case PAIRING_STATE_NONE:
    default:
      // The daemon no longer knows about our pairing (restart, or the
      // expiry sweep already discarded it). If we thought one was in
      // flight, surface it as expired; otherwise nothing changes.
      return prev.phase === 'pending' || prev.phase === 'starting'
        ? { ...prev, phase: 'expired' }
        : prev
  }
}

/** Milliseconds until expiry, clamped at 0. Null expiry → 0. */
export function pairingMsLeft(expiresAtMs: number | null, nowMs: number): number {
  if (expiresAtMs === null) return 0
  return Math.max(0, expiresAtMs - nowMs)
}

/** "m:ss" countdown label. */
export function formatCountdown(msLeft: number): string {
  const total = Math.max(0, Math.ceil(msLeft / 1000))
  const m = Math.floor(total / 60)
  const s = total % 60
  return `${m}:${String(s).padStart(2, '0')}`
}

/** Only an in-flight pairing warrants polling GetTelegramPairingStatus. */
export function shouldPollPairing(phase: PairingPhase): boolean {
  return phase === 'pending'
}
