// Quick-add preview helper (v10 Torch). The daemon owns the real parser
// (internal/daemon/task/quickadd.go — ParseQuickAdd); this mirrors only its
// block-splitting rule so the modal can show "will create N tasks" without a
// round-trip: each TOP-LEVEL bullet (`- `, `* `, `1. `, `1) ` at column 0)
// is one task, and bullet-less non-empty input is a single task.
const BULLET_RE = /^(?:[-*]|\d+[.)])\s+\S/

export function countQuickAddTasks(text: string): number {
  const bullets = text
    .replace(/\r\n/g, '\n')
    .split('\n')
    .filter((line) => BULLET_RE.test(line)).length
  if (bullets > 0) return bullets
  return text.trim() === '' ? 0 : 1
}
