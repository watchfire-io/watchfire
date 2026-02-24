import { useEffect, useRef, useCallback } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { useAgentStore } from '../stores/agent-store'

interface UseAgentTerminalOptions {
  projectId: string
  containerRef: React.RefObject<HTMLDivElement | null>
  /** Whether to subscribe to screen updates (agent is running) */
  active?: boolean
}

export function useAgentTerminal({ projectId, containerRef, active = false }: UseAgentTerminalOptions) {
  const termRef = useRef<Terminal | null>(null)
  const fitRef = useRef<FitAddon | null>(null)
  const resizeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const abortRef = useRef<AbortController | null>(null)

  const subscribeRawOutput = useAgentStore((s) => s.subscribeRawOutput)
  const sendInput = useAgentStore((s) => s.sendInput)
  const resize = useAgentStore((s) => s.resize)

  // Initialize terminal (always mounted for measurement)
  useEffect(() => {
    if (!containerRef.current) return

    const term = new Terminal({
      fontFamily: "'JetBrains Mono', 'Fira Code', monospace",
      fontSize: 13,
      lineHeight: 1.2,
      cursorBlink: true,
      theme: {
        background: '#16181d',
        foreground: '#ffffff',
        cursor: '#e07040',
        selectionBackground: '#2d3140',
        black: '#16181d',
        red: '#ff5f57',
        green: '#28c940',
        yellow: '#ffbd2e',
        blue: '#007aff',
        magenta: '#e07040',
        cyan: '#5ac8fa',
        white: '#ffffff',
        brightBlack: '#2d3140',
        brightRed: '#ff6b6b',
        brightGreen: '#5bd75b',
        brightYellow: '#ffca4b',
        brightBlue: '#409cff',
        brightMagenta: '#e88050',
        brightCyan: '#70d7ef',
        brightWhite: '#ffffff'
      }
    })

    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(containerRef.current)
    requestAnimationFrame(() => fit.fit())

    termRef.current = term
    fitRef.current = fit

    // Handle user input → send to agent
    term.onData((data) => {
      const encoder = new TextEncoder()
      sendInput(projectId, encoder.encode(data))
    })

    // Handle resize
    const observer = new ResizeObserver(() => {
      if (resizeTimerRef.current) clearTimeout(resizeTimerRef.current)
      resizeTimerRef.current = setTimeout(() => {
        fit.fit()
        const dims = fit.proposeDimensions()
        if (dims) {
          resize(projectId, dims.rows, dims.cols)
        }
      }, 100)
    })
    observer.observe(containerRef.current)

    return () => {
      abortRef.current?.abort()
      abortRef.current = null
      observer.disconnect()
      if (resizeTimerRef.current) clearTimeout(resizeTimerRef.current)
      term.dispose()
      termRef.current = null
      fitRef.current = null
    }
  }, [projectId])

  // Subscribe/unsubscribe to raw output based on active state
  useEffect(() => {
    const term = termRef.current
    if (!term || !active) {
      abortRef.current?.abort()
      abortRef.current = null
      return
    }

    // Send resize immediately so daemon knows our dimensions
    const fit = fitRef.current
    if (fit) {
      fit.fit()
      const dims = fit.proposeDimensions()
      if (dims) {
        resize(projectId, dims.rows, dims.cols)
      }
    }

    const abort = subscribeRawOutput(
      projectId,
      (data) => {
        term.write(data)
      },
      () => {
        term.write('\r\n\x1b[90m[Agent stopped]\x1b[0m\r\n')
      }
    )
    abortRef.current = abort

    return () => {
      abort.abort()
      abortRef.current = null
    }
  }, [projectId, active])

  /** Get current terminal dimensions (for passing to startAgent) */
  const getDimensions = useCallback(() => {
    const fit = fitRef.current
    if (!fit) return { rows: 24, cols: 80 }
    const dims = fit.proposeDimensions()
    return dims ? { rows: dims.rows, cols: dims.cols } : { rows: 24, cols: 80 }
  }, [])

  return { terminal: termRef, getDimensions }
}
