import { useState, useCallback, useRef, useEffect } from 'react'

interface PanelResizeOptions {
  storageKey: string
  defaultWidth?: number
  minWidth?: number
  maxWidth?: number
}

/**
 * Hook for drag-resizable panels with localStorage persistence.
 */
export function usePanelResize({
  storageKey,
  defaultWidth = 520,
  minWidth = 350,
  maxWidth = 800
}: PanelResizeOptions) {
  const [width, setWidth] = useState(() => {
    const saved = localStorage.getItem(storageKey)
    return saved ? Number(saved) : defaultWidth
  })
  const isDragging = useRef(false)

  const handleDragStart = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault()
      isDragging.current = true
      const startX = e.clientX
      const startWidth = width

      const onMouseMove = (ev: MouseEvent) => {
        const delta = startX - ev.clientX
        const newWidth = Math.min(maxWidth, Math.max(minWidth, startWidth + delta))
        setWidth(newWidth)
      }

      const onMouseUp = () => {
        isDragging.current = false
        document.removeEventListener('mousemove', onMouseMove)
        document.removeEventListener('mouseup', onMouseUp)
        document.body.style.cursor = ''
        document.body.style.userSelect = ''
      }

      document.body.style.cursor = 'col-resize'
      document.body.style.userSelect = 'none'
      document.addEventListener('mousemove', onMouseMove)
      document.addEventListener('mouseup', onMouseUp)
    },
    [width, minWidth, maxWidth]
  )

  useEffect(() => {
    localStorage.setItem(storageKey, String(width))
  }, [width, storageKey])

  return { width, handleDragStart, isDragging }
}
