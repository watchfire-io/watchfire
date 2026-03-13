import { useRef } from 'react'
import { useTerminal } from '../../../hooks/useTerminal'

interface TerminalTabProps {
  sessionId: string
  visible: boolean
}

export function TerminalTab({ sessionId, visible }: TerminalTabProps) {
  const containerRef = useRef<HTMLDivElement | null>(null)

  useTerminal({ sessionId, containerRef, visible })

  return (
    <div
      ref={containerRef}
      className="w-full h-full"
      style={{ display: visible ? 'block' : 'none' }}
    />
  )
}
