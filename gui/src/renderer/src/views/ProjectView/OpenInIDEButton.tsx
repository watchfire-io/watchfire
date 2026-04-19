import { useEffect, useRef, useState } from 'react'
import { ChevronDown, Code, Folder, ExternalLink } from 'lucide-react'
import { Button } from '../../components/ui/Button'
import { useToast } from '../../components/ui/Toast'
import { cn } from '../../lib/utils'

type IDEId =
  | 'vscode'
  | 'cursor'
  | 'windsurf'
  | 'zed'
  | 'webstorm'
  | 'idea'
  | 'sublime'
  | 'fleet'
  | 'xcode'
  | 'finder'

interface IDEOption {
  id: IDEId
  label: string
  macOnly?: boolean
}

const IDE_OPTIONS: IDEOption[] = [
  { id: 'vscode', label: 'VS Code' },
  { id: 'cursor', label: 'Cursor' },
  { id: 'windsurf', label: 'Windsurf' },
  { id: 'zed', label: 'Zed' },
  { id: 'webstorm', label: 'WebStorm' },
  { id: 'idea', label: 'IntelliJ IDEA' },
  { id: 'sublime', label: 'Sublime Text' },
  { id: 'fleet', label: 'Fleet' },
  { id: 'xcode', label: 'Xcode', macOnly: true },
  { id: 'finder', label: 'File Manager' }
]

const STORAGE_KEY = 'wf-preferred-ide'
const isMac = typeof navigator !== 'undefined' && /Mac/i.test(navigator.platform)

interface Props {
  projectPath: string
}

export function OpenInIDEButton({ projectPath }: Props) {
  const { toast } = useToast()
  const [open, setOpen] = useState(false)
  const [preferred, setPreferred] = useState<IDEId>(() => {
    const saved = localStorage.getItem(STORAGE_KEY) as IDEId | null
    if (saved && IDE_OPTIONS.some((o) => o.id === saved)) return saved
    return 'vscode'
  })
  const rootRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])

  const preferredOption = IDE_OPTIONS.find((o) => o.id === preferred) ?? IDE_OPTIONS[0]

  const launch = async (ide: IDEId) => {
    const res = await window.watchfire.openInIDE(ide, projectPath)
    if (!res.ok) {
      toast(res.error || 'Failed to open IDE', 'error')
      return false
    }
    localStorage.setItem(STORAGE_KEY, ide)
    setPreferred(ide)
    return true
  }

  const available = IDE_OPTIONS.filter((o) => !o.macOnly || isMac)
  const Icon = preferred === 'finder' ? Folder : Code

  return (
    <div ref={rootRef} className="relative flex items-stretch">
      <Button
        size="sm"
        variant="secondary"
        onClick={() => launch(preferred)}
        title={`Open in ${preferredOption.label}`}
        className="rounded-r-none border-r-0"
      >
        <Icon size={12} />
        Open
      </Button>
      <Button
        size="sm"
        variant="secondary"
        onClick={() => setOpen((v) => !v)}
        title="Choose IDE"
        className="rounded-l-none px-1.5"
        aria-label="Choose IDE"
      >
        <ChevronDown size={12} />
      </Button>
      {open && (
        <div
          className={cn(
            'absolute right-0 top-full mt-1 z-20 min-w-[180px]',
            'rounded-[var(--wf-radius-md)] border border-[var(--wf-border)] bg-[var(--wf-bg-secondary)] shadow-lg',
            'py-1'
          )}
        >
          {available.map((opt) => {
            const OptIcon = opt.id === 'finder' ? Folder : opt.id === preferred ? Code : ExternalLink
            return (
              <button
                key={opt.id}
                onClick={() => {
                  setOpen(false)
                  launch(opt.id)
                }}
                className={cn(
                  'flex items-center gap-2 w-full px-3 py-1.5 text-xs text-left transition-colors',
                  opt.id === preferred
                    ? 'text-[var(--wf-text-primary)] bg-[var(--wf-bg-elevated)]'
                    : 'text-[var(--wf-text-secondary)] hover:bg-[var(--wf-bg-elevated)] hover:text-[var(--wf-text-primary)]'
                )}
              >
                <OptIcon size={12} />
                {opt.label}
              </button>
            )
          })}
        </div>
      )}
    </div>
  )
}
