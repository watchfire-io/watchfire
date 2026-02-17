import { useEffect, useRef, useCallback } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { useAgentStore } from '../stores/agent-store'

interface UseAgentTerminalOptions {
  projectId: string
  containerRef: React.RefObject<HTMLDivElement | null>
  enabled?: boolean
}

export function useAgentTerminal({ projectId, containerRef, enabled = true }: UseAgentTerminalOptions) {
  const termRef = useRef<Terminal | null>(null)
  const fitRef = useRef<FitAddon | null>(null)
  const resizeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const subscribeScreen = useAgentStore((s) => s.subscribeScreen)
  const sendInput = useAgentStore((s) => s.sendInput)
  const resize = useAgentStore((s) => s.resize)
  const cleanupSubscriptions = useAgentStore((s) => s.cleanupSubscriptions)

  // Initialize terminal
  useEffect(() => {
    if (!containerRef.current || !enabled) return

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
    fit.fit()

    termRef.current = term
    fitRef.current = fit

    // Handle user input → send to agent
    term.onData((data) => {
      const encoder = new TextEncoder()
      sendInput(projectId, encoder.encode(data))
    })

    // Subscribe to screen updates
    const abort = subscribeScreen(
      projectId,
      (ansiContent) => {
        term.write('\x1b[2J\x1b[H' + ansiContent)
      },
      () => {
        term.write('\r\n\x1b[90m[Agent stopped]\x1b[0m\r\n')
      }
    )

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
      abort.abort()
      observer.disconnect()
      if (resizeTimerRef.current) clearTimeout(resizeTimerRef.current)
      term.dispose()
      termRef.current = null
      fitRef.current = null
    }
  }, [projectId, enabled])

  const fitTerminal = useCallback(() => {
    fitRef.current?.fit()
  }, [])

  return { terminal: termRef, fitTerminal }
}
