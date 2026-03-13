import { useEffect, useRef } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { useTerminalStore } from '../stores/terminal-store'
import { terminalTheme, terminalFontFamily } from '../lib/terminal-theme'

interface UseTerminalOptions {
  sessionId: string
  containerRef: React.RefObject<HTMLDivElement | null>
  visible: boolean
}

export function useTerminal({ sessionId, containerRef, visible }: UseTerminalOptions) {
  const termRef = useRef<Terminal | null>(null)
  const fitRef = useRef<FitAddon | null>(null)
  const resizeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  // Initialize terminal
  useEffect(() => {
    if (!containerRef.current) return

    const term = new Terminal({
      fontFamily: terminalFontFamily,
      fontSize: 13,
      lineHeight: 1.2,
      cursorBlink: true,
      theme: terminalTheme
    })

    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(containerRef.current)
    requestAnimationFrame(() => fit.fit())

    termRef.current = term
    fitRef.current = fit

    // User input → PTY
    term.onData((data) => {
      window.watchfire.ptyWrite(sessionId, data)
    })

    // Handle resize
    const observer = new ResizeObserver(() => {
      if (resizeTimerRef.current) clearTimeout(resizeTimerRef.current)
      resizeTimerRef.current = setTimeout(() => {
        fit.fit()
        const dims = fit.proposeDimensions()
        if (dims) {
          window.watchfire.ptyResize(sessionId, dims.cols, dims.rows)
        }
      }, 100)
    })
    observer.observe(containerRef.current)

    // Register output callback
    const store = useTerminalStore.getState()
    store.registerOutputCallback(sessionId, (data) => {
      term.write(data)
      term.scrollToBottom()
    })
    store.registerExitCallback(sessionId, (_exitCode) => {
      term.write('\r\n\x1b[90m[Process exited]\x1b[0m\r\n')
    })

    return () => {
      observer.disconnect()
      if (resizeTimerRef.current) clearTimeout(resizeTimerRef.current)
      const s = useTerminalStore.getState()
      s.unregisterOutputCallback(sessionId)
      s.unregisterExitCallback(sessionId)
      term.dispose()
      termRef.current = null
      fitRef.current = null
    }
  }, [sessionId])

  // Re-fit when visibility changes
  useEffect(() => {
    if (visible && fitRef.current) {
      requestAnimationFrame(() => {
        fitRef.current?.fit()
        const dims = fitRef.current?.proposeDimensions()
        if (dims) {
          window.watchfire.ptyResize(sessionId, dims.cols, dims.rows)
        }
      })
    }
  }, [visible, sessionId])
}
