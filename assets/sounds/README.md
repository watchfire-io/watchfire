# Notification Sounds

Two short audio cues shipped with the Watchfire GUI for v5.0 "Pulse"
notifications. Both are mono, 22050 Hz, 16-bit PCM WAV files — under 50 KB
and under 700 ms each.

| File              | Purpose                                        | Length |
|-------------------|------------------------------------------------|--------|
| `task-done.wav`   | `RUN_COMPLETE` — short ascending two-note ding | ~500 ms |
| `task-failed.wav` | `TASK_FAILED` — subdued descending two-note tone | ~580 ms |

## Source / licence

Both sounds were generated from scratch by the Watchfire project as
combinations of pure sine tones with simple attack/release envelopes — no
samples, recordings, or third-party assets were used. They are released
under the same licence as the rest of the Watchfire repository (see the
top-level `LICENSE` file). You are free to ship them with downstream
forks.

The generator is a small Python script using only the standard library
(`wave`, `struct`, `math`); it is intentionally not committed because the
WAVs themselves are the source of truth — they are bundled into the
Electron renderer build via `import url from '...?url'` (see
`gui/src/renderer/src/stores/notifications-store.ts`).

To regenerate (e.g. to retune the frequencies), the script lives in the
v5.0 Pulse task notes and can be re-emitted with no external dependencies.
