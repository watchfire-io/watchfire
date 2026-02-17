import { Monitor, Sun, Moon } from 'lucide-react'
import type { Settings } from '../../generated/watchfire_pb'
import { useSettingsStore } from '../../stores/settings-store'
import { useAppStore } from '../../stores/app-store'
import { cn } from '../../lib/utils'

interface Props {
  settings: Settings
}

const themes = [
  { key: 'system', label: 'System', icon: Monitor },
  { key: 'light', label: 'Light', icon: Sun },
  { key: 'dark', label: 'Dark', icon: Moon }
] as const

export function AppearanceSection({ settings }: Props) {
  const updateSettings = useSettingsStore((s) => s.updateSettings)
  const setTheme = useAppStore((s) => s.setTheme)
  const currentTheme = settings.appearance?.theme || 'system'

  const handleChange = (theme: string) => {
    setTheme(theme as 'system' | 'light' | 'dark')
    updateSettings({ appearance: { theme } })
  }

  return (
    <section>
      <h3 className="font-heading font-semibold text-sm text-[var(--wf-text-muted)] uppercase tracking-wider mb-3">
        Appearance
      </h3>
      <div className="flex gap-3">
        {themes.map(({ key, label, icon: Icon }) => (
          <button
            key={key}
            onClick={() => handleChange(key)}
            className={cn(
              'flex flex-col items-center gap-2 px-4 py-3 rounded-[var(--wf-radius-lg)] border transition-all flex-1',
              currentTheme === key
                ? 'border-fire-500 bg-fire-500/10'
                : 'border-[var(--wf-border)] hover:border-[var(--wf-border-light)]'
            )}
          >
            <Icon size={20} className={currentTheme === key ? 'text-fire-500' : 'text-[var(--wf-text-muted)]'} />
            <span className="text-xs font-medium">{label}</span>
          </button>
        ))}
      </div>
    </section>
  )
}
