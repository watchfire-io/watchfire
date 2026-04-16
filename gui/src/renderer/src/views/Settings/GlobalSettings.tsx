import { useEffect, useState } from 'react'
import type { AgentInfo } from '../../generated/watchfire_pb'
import { useSettingsStore } from '../../stores/settings-store'
import { useAppStore } from '../../stores/app-store'
import { getSettingsClient } from '../../lib/grpc-client'
import { DefaultsSection } from './DefaultsSection'
import { AppearanceSection } from './AppearanceSection'
import { AgentPathsSection } from './AgentPathsSection'
import { UpdatesSection } from './UpdatesSection'
import { AboutSection } from './AboutSection'

export function GlobalSettings() {
  const settings = useSettingsStore((s) => s.settings)
  const fetchSettings = useSettingsStore((s) => s.fetchSettings)
  const loading = useSettingsStore((s) => s.loading)
  const connected = useAppStore((s) => s.connected)
  const [version, setVersion] = useState<string>('')
  const [agents, setAgents] = useState<AgentInfo[]>([])
  const [agentsLoaded, setAgentsLoaded] = useState(false)

  useEffect(() => {
    fetchSettings()
    window.watchfire.getVersion().then(setVersion)
  }, [])

  // Re-fetch settings when reconnecting after a disconnect
  useEffect(() => {
    if (connected) fetchSettings()
  }, [connected])

  useEffect(() => {
    if (!connected) return
    let cancelled = false
    setAgentsLoaded(false)
    ;(async () => {
      try {
        const res = await getSettingsClient().listAgents({})
        if (!cancelled) {
          setAgents(res.agents)
          setAgentsLoaded(true)
        }
      } catch {
        if (!cancelled) setAgentsLoaded(true)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [connected])

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
            <DefaultsSection settings={settings} agents={agents} agentsLoaded={agentsLoaded} />
            <AgentPathsSection settings={settings} agents={agents} agentsLoaded={agentsLoaded} />
            <UpdatesSection settings={settings} />
          </>
        )}
        <AboutSection version={version} />
      </div>
    </div>
  )
}
