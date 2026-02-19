import { useEffect, useState } from 'react'
import { useSettingsStore } from '../../stores/settings-store'
import { useAppStore } from '../../stores/app-store'
import { DefaultsSection } from './DefaultsSection'
import { AppearanceSection } from './AppearanceSection'
import { ClaudeCliSection } from './ClaudeCliSection'
import { UpdatesSection } from './UpdatesSection'
import { AboutSection } from './AboutSection'

export function GlobalSettings() {
  const settings = useSettingsStore((s) => s.settings)
  const fetchSettings = useSettingsStore((s) => s.fetchSettings)
  const loading = useSettingsStore((s) => s.loading)
  const [version, setVersion] = useState<string>('')

  useEffect(() => {
    fetchSettings()
    window.watchfire.getVersion().then(setVersion)
  }, [])

  if (loading && !settings) {
    return (
      <div className="flex-1 flex items-center justify-center">
        <div className="w-6 h-6 border-2 border-fire-500 border-t-transparent rounded-full animate-spin" />
      </div>
    )
  }

  return (
    <div className="flex-1 overflow-y-auto p-6">
      <h2 className="font-heading text-xl font-semibold mb-6">Settings</h2>
      <div className="max-w-lg space-y-8">
        {settings && (
          <>
            <AppearanceSection settings={settings} />
            <DefaultsSection settings={settings} />
            <ClaudeCliSection settings={settings} />
            <UpdatesSection settings={settings} />
          </>
        )}
        <AboutSection version={version} />
      </div>
    </div>
  )
}
