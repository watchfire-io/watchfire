import { useEffect, useState, useCallback, createContext, useContext, type ReactNode } from 'react'
import { X, CheckCircle, AlertTriangle, XCircle, Info } from 'lucide-react'
import { cn } from '../../lib/utils'

type ToastType = 'success' | 'error' | 'warning' | 'info'

interface ToastItem {
  id: number
  message: string
  type: ToastType
}

interface ToastContextType {
  toast: (message: string, type?: ToastType) => void
}

const ToastContext = createContext<ToastContextType>({ toast: () => {} })

export function useToast() {
  return useContext(ToastContext)
}

let nextId = 0

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<ToastItem[]>([])

  const toast = useCallback((message: string, type: ToastType = 'info') => {
    const id = nextId++
    setToasts((prev) => [...prev, { id, message, type }])
    setTimeout(() => {
      setToasts((prev) => prev.filter((t) => t.id !== id))
    }, 5000)
  }, [])

  const dismiss = useCallback((id: number) => {
    setToasts((prev) => prev.filter((t) => t.id !== id))
  }, [])

  return (
    <ToastContext.Provider value={{ toast }}>
      {children}
      <div className="fixed bottom-4 right-4 z-[300] flex flex-col gap-2 pointer-events-none">
        {toasts.map((t) => (
          <ToastNotification key={t.id} item={t} onDismiss={() => dismiss(t.id)} />
        ))}
      </div>
    </ToastContext.Provider>
  )
}

const icons: Record<ToastType, typeof Info> = {
  success: CheckCircle,
  error: XCircle,
  warning: AlertTriangle,
  info: Info
}

const colors: Record<ToastType, string> = {
  success: 'border-emerald-700/40 bg-emerald-900/40',
  error: 'border-red-700/40 bg-red-900/40',
  warning: 'border-amber-700/40 bg-amber-900/40',
  info: 'border-blue-700/40 bg-blue-900/40'
}

function ToastNotification({ item, onDismiss }: { item: ToastItem; onDismiss: () => void }) {
  const Icon = icons[item.type]
  return (
    <div
      className={cn(
        'pointer-events-auto flex items-center gap-3 px-4 py-3 rounded-[var(--wf-radius-lg)] border shadow-wf min-w-[300px]',
        colors[item.type]
      )}
    >
      <Icon size={16} className="shrink-0" />
      <span className="flex-1 text-sm">{item.message}</span>
      <button onClick={onDismiss} className="text-[var(--wf-text-muted)] hover:text-[var(--wf-text-primary)]">
        <X size={14} />
      </button>
    </div>
  )
}
