import { useCallback, useRef } from 'react'
import { useTerminalStore } from '../stores/terminal-store'

interface PanelResizeVerticalOptions {
  defaultHeight?: number
  minHeight?: number
  maxHeight?: number
}

export function usePanelResizeVertical({
  defaultHeight = 250,
  minHeight = 150,
  maxHeight = 500
}: PanelResizeVerticalOptions = {}) {
  const isDragging = useRef(false)
  const height = useTerminalStore((s) => s.panelHeight) || defaultHeight
  const setPanelHeight = useTerminalStore((s) => s.setPanelHeight)

  const handleDragStart = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault()
      isDragging.current = true
      const startY = e.clientY
      const startHeight = height

      const onMouseMove = (ev: MouseEvent) => {
        const delta = startY - ev.clientY
        const newHeight = Math.min(maxHeight, Math.max(minHeight, startHeight + delta))
        setPanelHeight(newHeight)
      }

      const onMouseUp = () => {
        isDragging.current = false
        document.removeEventListener('mousemove', onMouseMove)
        document.removeEventListener('mouseup', onMouseUp)
        document.body.style.cursor = ''
        document.body.style.userSelect = ''
      }

      document.body.style.cursor = 'row-resize'
      document.body.style.userSelect = 'none'
      document.addEventListener('mousemove', onMouseMove)
      document.addEventListener('mouseup', onMouseUp)
    },
    [height, minHeight, maxHeight, setPanelHeight]
  )

  return { height, handleDragStart, isDragging }
}
