import { useEffect, useRef, useCallback, useState } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { useAgentStore } from '../stores/agent-store'
import { terminalTheme, terminalFontFamily } from '../lib/terminal-theme'

interface UseAgentTerminalOptions {
  projectId: string
  containerRef: React.RefObject<HTMLDivElement | null>
  /** Whether to subscribe to screen updates (agent is running) */
  active?: boolean
}

// activeFlickerDebounceMs is a safety net covering one full 2 s status
// poll plus margin. v7.1.0 removed the main flicker source (agent-store
// no longer fabricates isRunning=false on transient errors, #0101) but
// active can still legitimately drop false → true briefly during
// stop+start sequences (Run All, Wildfire phase transitions). Holding
// the unsubscribe across that window preserves the subscription so the
// generation-change reset in the effect below can take over.
const activeFlickerDebounceMs = 3000

export function useAgentTerminal({ projectId, containerRef, active = false }: UseAgentTerminalOptions) {
  const termRef = useRef<Terminal | null>(null)
  const fitRef = useRef<FitAddon | null>(null)
  const resizeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const lastResizeDimsRef = useRef<{ rows: number; cols: number } | null>(null)
  const abortRef = useRef<AbortController | null>(null)
  const unsubDelayRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const bytesReceivedRef = useRef<number>(0)
  const prevProjectIdRef = useRef<string>(projectId)
  const prevReconnectKeyRef = useRef<number>(0)
  const prevStartedAtKeyRef = useRef<string>('')

  const [reconnectKey, setReconnectKey] = useState(0)
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const subscribeRawOutput = useAgentStore((s) => s.subscribeRawOutput)
  const sendInput = useAgentStore((s) => s.sendInput)
  const resize = useAgentStore((s) => s.resize)

  // Initialize terminal (always mounted for measurement)
  useEffect(() => {
    if (!containerRef.current) return

    const term = new Terminal({
      fontFamily: terminalFontFamily,
      fontSize: 13,
      lineHeight: 1.2,
      cursorBlink: true,
      // 10 000-line scrollback (#0100). Default 1000 truncates multi-minute
      // agent sessions; bumping the ceiling lets the user actually reach
      // earlier output via the mouse wheel without losing context.
      scrollback: 10000,
      theme: terminalTheme
    })

    const fit = new FitAddon()
    term.loadAddon(fit)

    // Defer open() until the container has non-zero dimensions.
    // xterm's Viewport constructor crashes if the container has no layout.
    const container = containerRef.current
    const tryOpen = (): void => {
      if (!container.isConnected || container.clientWidth === 0 || container.clientHeight === 0) {
        requestAnimationFrame(tryOpen)
        return
      }
      term.open(container)
      fit.fit()
    }
    tryOpen()

    termRef.current = term
    fitRef.current = fit

    // Handle user input → send to agent
    term.onData((data) => {
      const encoder = new TextEncoder()
      sendInput(projectId, encoder.encode(data))
    })

    // Handle resize. Bail out when the fitted rows/cols haven't actually
    // changed since the last sent resize (#0100) — scrollbar visibility
    // and tiny container nudges otherwise spam the daemon with no-op
    // RPCs, and each round-trip is a potential interaction surface with
    // the raw-output subscription pipe.
    const observer = new ResizeObserver(() => {
      if (resizeTimerRef.current) clearTimeout(resizeTimerRef.current)
      resizeTimerRef.current = setTimeout(() => {
        fit.fit()
        const dims = fit.proposeDimensions()
        if (!dims || !Number.isFinite(dims.rows) || !Number.isFinite(dims.cols)) return
        const prev = lastResizeDimsRef.current
        if (prev && prev.rows === dims.rows && prev.cols === dims.cols) return
        lastResizeDimsRef.current = { rows: dims.rows, cols: dims.cols }
        resize(projectId, dims.rows, dims.cols)
      }, 100)
    })
    observer.observe(containerRef.current)

    return () => {
      abortRef.current?.abort()
      abortRef.current = null
      if (unsubDelayRef.current) clearTimeout(unsubDelayRef.current)
      unsubDelayRef.current = null
      if (reconnectTimerRef.current) clearTimeout(reconnectTimerRef.current)
      observer.disconnect()
      if (resizeTimerRef.current) clearTimeout(resizeTimerRef.current)
      lastResizeDimsRef.current = null
      term.dispose()
      termRef.current = null
      fitRef.current = null
      bytesReceivedRef.current = 0
      prevStartedAtKeyRef.current = ''
    }
  }, [projectId])

  // Subscribe to AgentStatus.startedAt so the subscribe effect below
  // re-runs when the daemon spawns a new Process (kill+restart from
  // Run All, Wildfire, phase transitions, or a manual ChatTab auto-
  // restart). The proto Timestamp is the only reliable client-visible
  // signal of a daemon-side generation change; without it we'd either
  // miss the new agent's initial prompt (stale cursor races the new
  // rawTotalBytes counter to zero) or, conversely, replay the full
  // buffer on every transient stream blip and stack overlapping
  // banners on top of the existing xterm state.
  const startedAt = useAgentStore((s) => s.statuses[projectId]?.startedAt)
  const startedAtKey = startedAt ? `${startedAt.seconds}.${startedAt.nanos}` : ''

  // Manage the raw-output subscription. The previous (#0100) version of
  // this effect ran term.clear() and aborted + re-subscribed on every
  // dep change, snapping the viewport to byte 0 on every poll. The
  // current behaviour mirrors the TUI's contract:
  //
  //  - active=true with a live subscription already in flight → no-op
  //    (idempotent re-run; preserves scroll position)
  //  - active=true with no subscription → fresh subscribe, passing the
  //    bytesReceived cursor so the daemon only sends bytes past the
  //    client's current position (same-Process catch-up — no replay)
  //  - active=false → schedule a delayed unsubscribe; if active flips
  //    back true within the window, cancel the tear-down
  //  - reconnectKey changed (same Process) → hard re-subscribe with
  //    cursor preserved, no xterm reset
  //  - startedAt changed (new daemon Process — #0102) → term.reset()
  //    + cursor=0 → daemon sends full new-Process buffer onto a fresh
  //    emulator state, no overlap with the previous agent's bytes
  //  - projectId changed → same as startedAt: term.reset() + cursor=0
  //
  // The TUI does the moral equivalent: it always subscribes with
  // bytesReceived=0 and calls terminal.Clear() on AgentStartedMsg
  // (msghandler.go:159), so the vt emulator processes each new
  // generation's bytes from a clean slate. The GUI keeps the cursor
  // optimisation for same-Process reconnects (so a transient stream
  // blip doesn't lose the user's scroll position to a full replay)
  // but matches the TUI's "fresh emulator on generation change" rule
  // — which is what stops the stacked-Claude-Code-banner garbage seen
  // when reset-on-onEnd fired without a corresponding term.reset().
  useEffect(() => {
    const term = termRef.current
    if (!term) return

    const projChanged = prevProjectIdRef.current !== projectId
    const reconnectKeyChanged = prevReconnectKeyRef.current !== reconnectKey
    prevProjectIdRef.current = projectId
    prevReconnectKeyRef.current = reconnectKey

    // Generation-change detection. The first observation of a non-empty
    // startedAt is NOT a transition (the agent didn't restart — we're
    // just learning its identity for the first time); only flip when
    // both sides are set and differ.
    const generationChanged =
      prevStartedAtKeyRef.current !== '' &&
      startedAtKey !== '' &&
      startedAtKey !== prevStartedAtKeyRef.current
    if (startedAtKey) prevStartedAtKeyRef.current = startedAtKey

    if (projChanged) {
      abortRef.current?.abort()
      abortRef.current = null
      if (unsubDelayRef.current) {
        clearTimeout(unsubDelayRef.current)
        unsubDelayRef.current = null
      }
      term.reset()
      bytesReceivedRef.current = 0
      prevStartedAtKeyRef.current = startedAtKey
    } else if (generationChanged) {
      // New daemon Process. Reset xterm AND the byte cursor so the
      // daemon's full buffer replay (cursor=0 → SubscribeRawFrom
      // returns the entire rawBuf) lands on a fresh emulator state.
      // Skipping term.reset() here is what produced the stacked
      // Claude Code banners (#0102) — absolute cursor-positioning
      // escapes from the new agent's UI redraw landed at xterm's
      // current cursor position on top of the previous agent's bytes.
      abortRef.current?.abort()
      abortRef.current = null
      if (unsubDelayRef.current) {
        clearTimeout(unsubDelayRef.current)
        unsubDelayRef.current = null
      }
      term.reset()
      bytesReceivedRef.current = 0
    } else if (reconnectKeyChanged) {
      abortRef.current?.abort()
      abortRef.current = null
      if (unsubDelayRef.current) {
        clearTimeout(unsubDelayRef.current)
        unsubDelayRef.current = null
      }
      // Same Process — onEnd's fetchStatus already confirmed startedAt
      // didn't change. Preserve the cursor so the daemon only sends
      // bytes we haven't seen; do not reset xterm so the user's view
      // and scroll position survive the transient blip.
    }

    if (active) {
      if (unsubDelayRef.current) {
        clearTimeout(unsubDelayRef.current)
        unsubDelayRef.current = null
      }

      if (abortRef.current && !abortRef.current.signal.aborted) {
        // Idempotent: a live subscription already exists. Nothing to do.
        return
      }

      // Send resize immediately so daemon knows our dimensions
      const fit = fitRef.current
      if (fit) {
        fit.fit()
        const dims = fit.proposeDimensions()
        if (dims && Number.isFinite(dims.rows) && Number.isFinite(dims.cols)) {
          const prev = lastResizeDimsRef.current
          if (!prev || prev.rows !== dims.rows || prev.cols !== dims.cols) {
            lastResizeDimsRef.current = { rows: dims.rows, cols: dims.cols }
            resize(projectId, dims.rows, dims.cols)
          }
        }
      }

      const abort = subscribeRawOutput(
        projectId,
        (data) => {
          bytesReceivedRef.current += data.byteLength
          term.write(data)
        },
        () => {
          // Stream closed. Do NOT reset bytesReceivedRef here — that
          // forces the daemon to re-send its full buffer on the next
          // subscribe, and those bytes carry absolute cursor-position
          // escapes that overlap with xterm's existing screen state,
          // producing the stacked-banner garbage from #0102. Instead,
          // refresh the AgentStatus first so the next effect run can
          // make an informed choice via startedAt:
          //   - generation changed → term.reset() + cursor=0 path
          //   - same Process       → reconnectKey path, cursor preserved
          // Either way, no replay-on-stale-state.
          useAgentStore.getState().fetchStatus(projectId).finally(() => {
            const currentStatus = useAgentStore.getState().statuses[projectId]
            if (currentStatus?.isRunning) {
              if (reconnectTimerRef.current) clearTimeout(reconnectTimerRef.current)
              reconnectTimerRef.current = setTimeout(() => {
                setReconnectKey((k) => k + 1)
              }, 200)
            }
          })
        },
        bytesReceivedRef.current
      )
      abortRef.current = abort
      return
    }

    // active=false. Don't tear down immediately — special-mode start
    // (Run All, Wildfire) and phase transitions briefly drop active
    // while the daemon kills the old Process and spawns a new one.
    // Wait one full poll cycle + margin before pulling the subscription
    // so the next active=true cancels the pending tear-down.
    if (!abortRef.current || abortRef.current.signal.aborted) return
    if (unsubDelayRef.current) return
    unsubDelayRef.current = setTimeout(() => {
      abortRef.current?.abort()
      abortRef.current = null
      unsubDelayRef.current = null
    }, activeFlickerDebounceMs)
  }, [projectId, active, reconnectKey, startedAtKey])

  /** Get current terminal dimensions (for passing to startAgent) */
  const getDimensions = useCallback(() => {
    const fit = fitRef.current
    if (!fit) return { rows: 24, cols: 80 }
    const dims = fit.proposeDimensions()
    return dims && Number.isFinite(dims.rows) && Number.isFinite(dims.cols)
      ? { rows: dims.rows, cols: dims.cols }
      : { rows: 24, cols: 80 }
  }, [])

  return { terminal: termRef, getDimensions }
}
