// v10 Torch (task 0148) — project-window IA revisit.
//
// Run with: node --test gui/src/renderer/src/views/ProjectView/__tests__/RefTabRouting.test.mjs
//
// Three behaviors are locked in here:
//  1. Focus-request routing in ProjectView: 'tasks'/'task' targets still land
//     on the Tasks tab (notification / needs-attention deep links), and the
//     new 'settings' target lands on the Settings tab — both must also clear
//     chat-focus so the collapsed reference region is revealed.
//  2. Cmd+, / app-menu Settings routing in App.tsx: a project window opens
//     PROJECT settings via a focus request; the home window keeps its
//     historical global-settings behavior.
//  3. Structure: the six reference surfaces stay reachable (three primary
//     tabs + three utility-cluster tabs), the persistent header gear exists
//     outside the collapsible region, and Wildfire leads the mode cluster in
//     the chat toolbar instead of sitting apart in the ProjectView header.
//
// The logic mirrors must be kept in sync with the effects they mirror — any
// change to those code paths must be reflected here.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { dirname, resolve } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const src = (rel) => readFile(resolve(here, rel), 'utf8')

// --- Logic mirror: ProjectView focusRequest effect --------------------------
// Mirrors the useEffect in ProjectView.tsx that consumes app-store focus
// requests ("Honour tray-driven focus requests ...").

function routeFocusRequest(state, focusRequest, projectId) {
  if (!focusRequest || focusRequest.projectId !== projectId) return state
  if (focusRequest.target === 'tasks' || focusRequest.target === 'task') {
    return { refTab: 'tasks', chatFocus: false }
  }
  if (focusRequest.target === 'settings') {
    return { refTab: 'settings', chatFocus: false }
  }
  return state
}

test('tasks/task deep links still land on Tasks and reveal the region', () => {
  for (const target of ['tasks', 'task']) {
    const next = routeFocusRequest(
      { refTab: 'definition', chatFocus: true },
      { id: 1, projectId: 'p1', target, taskNumber: 42 },
      'p1'
    )
    assert.deepEqual(next, { refTab: 'tasks', chatFocus: false })
  }
})

test('settings target lands on Settings and reveals the region from chat-focus', () => {
  const next = routeFocusRequest(
    { refTab: 'tasks', chatFocus: true },
    { id: 2, projectId: 'p1', target: 'settings' },
    'p1'
  )
  assert.deepEqual(next, { refTab: 'settings', chatFocus: false })
})

test('requests for another project or main target leave state untouched', () => {
  const state = { refTab: 'insights', chatFocus: true }
  assert.equal(
    routeFocusRequest(state, { id: 3, projectId: 'OTHER', target: 'settings' }, 'p1'),
    state
  )
  assert.equal(
    routeFocusRequest(state, { id: 4, projectId: 'p1', target: 'main' }, 'p1'),
    state
  )
  assert.equal(routeFocusRequest(state, null, 'p1'), state)
})

// --- Logic mirror: App.tsx openSettings ------------------------------------
// Mirrors the openSettings callback: project windows route a 'settings' focus
// request (so ProjectView expands the region), other windows open the global
// settings view.

function openSettingsAction(windowScope) {
  if (windowScope.kind === 'project') {
    return { kind: 'requestFocus', projectId: windowScope.projectId, target: 'settings' }
  }
  return { kind: 'setView', view: 'settings' }
}

test('Cmd+, in a project window opens project settings via focus request', () => {
  assert.deepEqual(openSettingsAction({ kind: 'project', projectId: 'p1' }), {
    kind: 'requestFocus',
    projectId: 'p1',
    target: 'settings'
  })
})

test('Cmd+, in the home window keeps opening global settings', () => {
  assert.deepEqual(openSettingsAction({ kind: 'home' }), {
    kind: 'setView',
    view: 'settings'
  })
})

// --- Structural assertions ---------------------------------------------------

test('ProjectView keeps all six surfaces reachable: 3 primary + 3 utility tabs', async () => {
  const s = await src('../ProjectView.tsx')
  // Primary set at full weight.
  assert.match(s, /PRIMARY_TABS[\s\S]*?'tasks'[\s\S]*?'definition'[\s\S]*?'insights'/)
  // Admin set grouped as the utility cluster.
  assert.match(s, /UTILITY_TABS[\s\S]*?'secrets'[\s\S]*?'trash'[\s\S]*?'settings'/)
  // Both groups actually render in the tab bar.
  assert.match(s, /PRIMARY_TABS\.map/)
  assert.match(s, /UTILITY_TABS\.map/)
  // Every surface still has its content mount.
  for (const tab of ['tasks', 'definition', 'insights', 'secrets', 'trash', 'settings']) {
    assert.match(s, new RegExp(`refTab === '${tab}' && <`), `content mount for ${tab}`)
  }
})

test('ProjectView has the persistent header Settings gear outside the collapsible region', async () => {
  const s = await src('../ProjectView.tsx')
  assert.match(s, /openProjectSettings/)
  assert.match(s, /title="Project settings/)
  // The gear must not live inside the `!chatFocus && (` collapsible block:
  // it appears before that block in the header row.
  const gearIdx = s.indexOf('title="Project settings')
  const collapsibleIdx = s.indexOf('{!chatFocus && (')
  assert.ok(gearIdx > -1 && collapsibleIdx > -1 && gearIdx < collapsibleIdx,
    'header gear must render before (outside) the collapsible reference region')
})

test('ProjectView focus effect handles the settings target', async () => {
  const s = await src('../ProjectView.tsx')
  assert.match(s, /focusRequest\.target === 'settings'/)
})

test('Wildfire moved out of the ProjectView header into the chat mode cluster, first position', async () => {
  const projectView = await src('../ProjectView.tsx')
  assert.ok(!projectView.includes('<WildfireControl'),
    'ProjectView header must no longer mount WildfireControl')

  const chatTab = await src('../RightPanel/ChatTab.tsx')
  const wf = chatTab.indexOf('<WildfireControl')
  const modes = chatTab.indexOf('<ModesControl')
  assert.ok(wf > -1, 'ChatTab must mount WildfireControl')
  assert.ok(modes > -1, 'ChatTab must mount ModesControl')
  assert.ok(wf < modes, 'WildfireControl must lead the mode cluster (before ModesControl)')
})

test('idle Wildfire button carries the fire-orange (primary) emphasis and keeps its confirm gate', async () => {
  const s = await src('../WildfireControl.tsx')
  // The idle start button opens the confirm modal and is styled primary
  // (bg-fire-500 family via Button variant).
  assert.match(s, /variant="primary"[\s\S]{0,200}?setConfirmOpen\(true\)/)
  // Confirm gate survives.
  assert.match(s, /<Modal/)
})

test('live run state renders as the header RunStatusLine, not inside the chat toolbar', async () => {
  // The current task + stop moved to a dedicated header line under the
  // title/git rows; duplicated in the chat toolbar it wrapped into a
  // two-row jumble at typical pane widths. No phase stepper — the header
  // AgentBadge already names the mode and wildfire phase.
  const line = await src('../RunStatusLine.tsx')
  assert.ok(!line.includes('WildfirePhaseBadge'), 'no phase stepper in the run status line')
  assert.match(line, /stopAgent/)

  const projectView = await src('../ProjectView.tsx')
  assert.ok(projectView.includes('<RunStatusLine'),
    'ProjectView header must mount RunStatusLine')

  const wildfire = await src('../WildfireControl.tsx')
  assert.ok(!wildfire.includes('WildfirePhaseBadge'),
    'running-state UI must not remain in WildfireControl')

  const chatTab = await src('../RightPanel/ChatTab.tsx')
  assert.ok(!chatTab.includes('<AgentBadge'),
    'ChatTab toolbar must not duplicate the header AgentBadge')
})

test('app-store exposes the settings focus target', async () => {
  const s = await src('../../../stores/app-store.ts')
  assert.match(s, /FocusRequestTarget = 'main' \| 'tasks' \| 'task' \| 'settings'/)
})

test('App.tsx wires Cmd+, and the menu IPC to openSettings', async () => {
  const s = await src('../../../App.tsx')
  assert.match(s, /target: 'settings'/)
  assert.match(s, /onOpenSettings\(openSettings\)/)
  assert.match(s, /e\.key === ','/)
})

test('main-process menu registers CmdOrCtrl+, and preload exposes onOpenSettings', async () => {
  const menu = await src('../../../../../main/menu.ts')
  assert.match(menu, /accelerator: 'CmdOrCtrl\+,'/)
  assert.match(menu, /open-settings/)

  const preload = await src('../../../../../preload/index.ts')
  assert.match(preload, /onOpenSettings/)
  assert.match(preload, /'open-settings'/)
})
