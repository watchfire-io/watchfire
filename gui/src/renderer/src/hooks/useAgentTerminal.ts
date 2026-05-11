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

// activeFlickerDebounceMs covers a single 2 s status-poll cycle plus a
// margin. A transient getAgentStatus error briefly sets isRunning=false
// in the store (agent-store.ts:56-62), which propagates to `active` and
// previously caused the subscribe effect to abort + re-subscribe + clear
// the terminal — snapping the viewport to the start of the session.
// Holding the unsubscribe for this long preserves the subscription
// across the flicker; the next poll resolves and active flips back true
// inside the window, cancelling the pending tear-down.
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
    }
  }, [projectId])

  // Manage the raw-output subscription (#0100). The previous version of
  // this effect ran term.clear() and aborted + re-subscribed on every
  // dep change, which snapped the viewport to byte 0 whenever `active`
  // flickered (transient status-poll errors flip isRunning false→true
  // within one 2 s cycle) or reconnectKey bumped. The new behaviour:
  //
  //  - active=true with a live subscription already in flight → no-op
  //    (idempotent re-run; preserves scroll position)
  //  - active=true with no subscription → fresh subscribe, passing the
  //    bytesReceived cursor so the daemon only sends bytes past the
  //    client's current position (no full-buffer replay on reconnect)
  //  - active=false → schedule a delayed unsubscribe; if active flips
  //    back true within the window, cancel the tear-down
  //  - reconnectKey changed → hard re-subscribe (deliberate reconnect
  //    from the onEnd path), still preserving the cursor so the catch-up
  //    starts from where we left off rather than byte 0
  //  - projectId changed → hard re-subscribe and reset the cursor (new
  //    project = new session = no shared history)
  //
  // The xterm terminal is never .clear()ed here; the daemon-side cursor
  // (proto SubscribeRawOutputRequest.bytes_received) guarantees we don't
  // double-write any byte we already have, so the prior "clear to avoid
  // duplicates on wildfire phase transitions" reasoning no longer applies.
  useEffect(() => {
    const term = termRef.current
    if (!term) return

    const projChanged = prevProjectIdRef.current !== projectId
    const reconnectKeyChanged = prevReconnectKeyRef.current !== reconnectKey
    prevProjectIdRef.current = projectId
    prevReconnectKeyRef.current = reconnectKey

    if (projChanged) {
      abortRef.current?.abort()
      abortRef.current = null
      if (unsubDelayRef.current) {
        clearTimeout(unsubDelayRef.current)
        unsubDelayRef.current = null
      }
      bytesReceivedRef.current = 0
    } else if (reconnectKeyChanged) {
      abortRef.current?.abort()
      abortRef.current = null
      if (unsubDelayRef.current) {
        clearTimeout(unsubDelayRef.current)
        unsubDelayRef.current = null
      }
      // Deliberately leave bytesReceivedRef alone — the daemon-side cursor
      // resumes the catch-up at this offset, so the user keeps their place.
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
          term.write('\r\n\x1b[90m[Agent stopped]\x1b[0m\r\n')
          // Only reconnect if agent is still running (wildfire phase transitions)
          // Don't reconnect if agent has truly stopped to avoid infinite loop
          const currentStatus = useAgentStore.getState().statuses[projectId]
          if (currentStatus?.isRunning) {
            if (reconnectTimerRef.current) clearTimeout(reconnectTimerRef.current)
            reconnectTimerRef.current = setTimeout(() => {
              setReconnectKey((k) => k + 1)
            }, 2000)
          }
        },
        bytesReceivedRef.current
      )
      abortRef.current = abort
      return
    }

    // active=false. Don't tear down immediately — a 2 s status-poll error
    // briefly flips isRunning false (agent-store.ts:56-62) and the next
    // poll restores it. Wait one full poll cycle + margin before pulling
    // the subscription.
    if (!abortRef.current || abortRef.current.signal.aborted) return
    if (unsubDelayRef.current) return
    unsubDelayRef.current = setTimeout(() => {
      abortRef.current?.abort()
      abortRef.current = null
      unsubDelayRef.current = null
    }, activeFlickerDebounceMs)
  }, [projectId, active, reconnectKey])

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
