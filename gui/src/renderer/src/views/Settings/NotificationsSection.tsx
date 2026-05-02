import { useState, useEffect } from 'react'
import type { Settings, NotificationsConfig } from '../../generated/watchfire_pb'
import { useSettingsStore } from '../../stores/settings-store'
import { useToast } from '../../components/ui/Toast'
import { Toggle } from '../../components/ui/Toggle'
import { Input } from '../../components/ui/Input'

interface Props {
  settings: Settings
}

const TIME_RE = /^([01]\d|2[0-3]):[0-5]\d$/

const DEFAULTS: NonNullable<NotificationsConfig> = {
  $typeName: 'watchfire.NotificationsConfig',
  enabled: true,
  events: {
    $typeName: 'watchfire.NotificationsEvents',
    taskFailed: true,
    runComplete: true,
  },
  sounds: {
    $typeName: 'watchfire.NotificationsSounds',
    enabled: true,
    taskFailed: true,
    runComplete: true,
    volume: 0.6,
  },
  quietHours: {
    $typeName: 'watchfire.QuietHoursConfig',
    enabled: false,
    start: '22:00',
    end: '08:00',
  },
} as unknown as NotificationsConfig

function readPrefs(settings: Settings): NotificationsConfig {
  // The daemon always populates the block on GetSettings (Normalize fills in
  // defaults), so this is mostly defensive — but a stale TS binding or test
  // fixture with a missing block must not crash the UI.
  const live = settings.defaults?.notifications
  if (live) return live
  return DEFAULTS
}

export function NotificationsSection({ settings }: Props) {
  const updateSettings = useSettingsStore((s) => s.updateSettings)
  const { toast } = useToast()
  const prefs = readPrefs(settings)

  const [start, setStart] = useState(prefs.quietHours?.start || '22:00')
  const [end, setEnd] = useState(prefs.quietHours?.end || '08:00')
  const [startErr, setStartErr] = useState<string | undefined>()
  const [endErr, setEndErr] = useState<string | undefined>()

  // Sync local input state when the store updates from elsewhere (fsnotify
  // re-read, another window edited the file). Without this the local input
  // state would drift from the saved value.
  useEffect(() => {
    setStart(prefs.quietHours?.start || '22:00')
    setEnd(prefs.quietHours?.end || '08:00')
  }, [prefs.quietHours?.start, prefs.quietHours?.end])

  const push = async (next: NotificationsConfig) => {
    try {
      // Spread the existing defaults so unrelated flags (auto_merge, agent
      // selection, etc.) survive the partial update — the daemon's
      // UpdateSettings overwrites every field in the defaults block, so we
      // resend the rest verbatim.
      const existing = settings.defaults ?? {}
      await updateSettings({
        defaults: {
          ...(existing as Record<string, unknown>),
          notifications: next
        } as never
      })
    } catch (err) {
      toast(`Failed to save notifications: ${(err as Error).message}`, 'error')
    }
  }

  const setEnabled = (v: boolean) => push({ ...prefs, enabled: v })
  const setEventTaskFailed = (v: boolean) =>
    push({
      ...prefs,
      events: { ...prefs.events!, taskFailed: v }
    } as NotificationsConfig)
  const setEventRunComplete = (v: boolean) =>
    push({
      ...prefs,
      events: { ...prefs.events!, runComplete: v }
    } as NotificationsConfig)
  const setSoundsEnabled = (v: boolean) =>
    push({
      ...prefs,
      sounds: { ...prefs.sounds!, enabled: v }
    } as NotificationsConfig)
  const setSoundTaskFailed = (v: boolean) =>
    push({
      ...prefs,
      sounds: { ...prefs.sounds!, taskFailed: v }
    } as NotificationsConfig)
  const setSoundRunComplete = (v: boolean) =>
    push({
      ...prefs,
      sounds: { ...prefs.sounds!, runComplete: v }
    } as NotificationsConfig)
  const setVolume = (v: number) =>
    push({
      ...prefs,
      sounds: { ...prefs.sounds!, volume: v }
    } as NotificationsConfig)
  const setQuietEnabled = (v: boolean) =>
    push({
      ...prefs,
      quietHours: { ...prefs.quietHours!, enabled: v }
    } as NotificationsConfig)

  const commitStart = (v: string) => {
    if (!TIME_RE.test(v)) {
      setStartErr('Use HH:MM (00:00–23:59)')
      return
    }
    setStartErr(undefined)
    push({
      ...prefs,
      quietHours: { ...prefs.quietHours!, start: v }
    } as NotificationsConfig)
  }
  const commitEnd = (v: string) => {
    if (!TIME_RE.test(v)) {
      setEndErr('Use HH:MM (00:00–23:59)')
      return
    }
    setEndErr(undefined)
    push({
      ...prefs,
      quietHours: { ...prefs.quietHours!, end: v }
    } as NotificationsConfig)
  }

  const volumePct = Math.round((prefs.sounds?.volume ?? 0) * 100)

  return (
    <section>
      <h3 className="font-heading font-semibold text-sm text-[var(--wf-text-muted)] uppercase tracking-wider mb-3">
        Notifications
      </h3>
      <div className="space-y-4">
        <Toggle
          checked={prefs.enabled}
          onChange={setEnabled}
          label="Enable notifications"
          description="Master toggle for desktop notifications"
        />
        <Toggle
          checked={prefs.events?.taskFailed ?? true}
          onChange={setEventTaskFailed}
          label="Notify on task failure"
          disabled={!prefs.enabled}
        />
        <Toggle
          checked={prefs.events?.runComplete ?? true}
          onChange={setEventRunComplete}
          label="Notify on run complete"
          disabled={!prefs.enabled}
        />
      </div>

      <h4 className="font-heading font-semibold text-xs text-[var(--wf-text-muted)] uppercase tracking-wider mt-6 mb-3">
        Sounds
      </h4>
      <div className="space-y-4">
        <Toggle
          checked={prefs.sounds?.enabled ?? true}
          onChange={setSoundsEnabled}
          label="Play sounds"
          disabled={!prefs.enabled}
        />
        <Toggle
          checked={prefs.sounds?.taskFailed ?? true}
          onChange={setSoundTaskFailed}
          label="Sound on task failure"
          disabled={!prefs.enabled || !(prefs.sounds?.enabled ?? true)}
        />
        <Toggle
          checked={prefs.sounds?.runComplete ?? true}
          onChange={setSoundRunComplete}
          label="Sound on run complete"
          disabled={!prefs.enabled || !(prefs.sounds?.enabled ?? true)}
        />
        <div className="flex flex-col gap-1.5">
          <label className="text-sm font-medium text-[var(--wf-text-secondary)]">
            Volume <span className="text-[var(--wf-text-muted)]">— {volumePct}%</span>
          </label>
          <input
            type="range"
            min={0}
            max={100}
            value={volumePct}
            disabled={!prefs.enabled || !(prefs.sounds?.enabled ?? true)}
            onChange={(e) => setVolume(Number(e.target.value) / 100)}
            className="w-full accent-fire-500"
          />
        </div>
      </div>

      <h4 className="font-heading font-semibold text-xs text-[var(--wf-text-muted)] uppercase tracking-wider mt-6 mb-3">
        Quiet Hours
      </h4>
      <div className="space-y-4">
        <Toggle
          checked={prefs.quietHours?.enabled ?? false}
          onChange={setQuietEnabled}
          label="Mute during a daily window"
          description="Notifications fired inside the window are silently dropped"
        />
        <div className="grid grid-cols-2 gap-3">
          <Input
            label="Start"
            value={start}
            onChange={(e) => setStart(e.target.value)}
            onBlur={() => commitStart(start)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') commitStart(start)
            }}
            placeholder="22:00"
            error={startErr}
            disabled={!prefs.enabled || !(prefs.quietHours?.enabled ?? false)}
          />
          <Input
            label="End"
            value={end}
            onChange={(e) => setEnd(e.target.value)}
            onBlur={() => commitEnd(end)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') commitEnd(end)
            }}
            placeholder="08:00"
            error={endErr}
            disabled={!prefs.enabled || !(prefs.quietHours?.enabled ?? false)}
          />
        </div>
      </div>
    </section>
  )
}
