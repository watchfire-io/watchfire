package tui

import (
	"testing"

	pb "github.com/watchfire-io/watchfire/proto"
)

// minimalProject returns a Project proto with the minimum fields the
// SettingsForm needs to render. Fields can be overridden by callers via
// struct literal embedding.
func minimalProject() *pb.Project {
	return &pb.Project{
		Name:         "demo",
		ProjectId:    "test-id",
		Path:         "/tmp/demo",
		DefaultAgent: "claude-code",
		Sandbox:      "auto",
		Status:       "active",
	}
}

// rowAt returns the row at index i, or nil if out of range. Helper for
// terse table tests.
func rowAt(s *SettingsForm, i int) *SettingsField {
	if i < 0 || i >= len(s.rows) {
		return nil
	}
	return &s.rows[i]
}

// findRow returns the first row matching key, or nil. Order of rows is a
// rebuildRows implementation detail; tests should look up by key.
func findRow(s *SettingsForm, key string) *SettingsField {
	for i := range s.rows {
		if s.rows[i].Key == key {
			return &s.rows[i]
		}
	}
	return nil
}

func TestSettingsCycleAgent(t *testing.T) {
	f := NewSettingsForm()
	f.LoadFromProject(minimalProject())

	row := findRow(f, "default_agent")
	if row == nil {
		t.Fatalf("default_agent row missing")
	}
	if len(row.CycleOptions) == 0 {
		t.Fatalf("expected agent cycle options")
	}
	if row.CycleOptions[row.CycleIndex].Value != "claude-code" {
		t.Fatalf("expected starting value claude-code, got %q", row.CycleOptions[row.CycleIndex].Value)
	}

	// Land cursor on agent row + switch to fields pane.
	for i := range f.rows {
		if f.rows[i].Key == "default_agent" {
			f.cursor = i
			break
		}
	}
	f.pane = settingsPaneFields

	changed, key, _, _ := f.Toggle()
	if !changed {
		t.Fatalf("expected toggle on cycle field to report change")
	}
	if key != "default_agent" {
		t.Fatalf("expected key default_agent, got %q", key)
	}
}

func TestSettingsCycleAgentFallsBackForUnknown(t *testing.T) {
	p := minimalProject()
	p.DefaultAgent = ""
	f := NewSettingsForm()
	f.LoadFromProject(p)

	row := findRow(f, "default_agent")
	if row == nil || len(row.CycleOptions) == 0 {
		t.Fatalf("default_agent row / options missing")
	}
	if row.CycleOptions[row.CycleIndex].Value != "claude-code" {
		t.Fatalf("expected fallback to claude-code, got %q", row.CycleOptions[row.CycleIndex].Value)
	}
}

// TestSidebarHasAllSections verifies every advertised section is wired
// into the layout.
func TestSidebarHasAllSections(t *testing.T) {
	f := NewSettingsForm()
	f.LoadFromProject(minimalProject())

	want := map[settingsSectionID]bool{
		sectionGeneral:       false,
		sectionAutomation:    false,
		sectionNotifications: false,
		sectionIntegrations:  false,
		sectionMetadata:      false,
		sectionSecrets:       false,
		sectionDanger:        false,
	}
	for _, r := range f.rows {
		want[r.Section] = true
	}
	for sec, ok := range want {
		if !ok {
			t.Errorf("section %d had no rows after LoadFromProject", sec)
		}
	}
}

// TestSwitchPaneToggles ensures Tab moves between sidebar and fields
// when not editing / searching.
func TestSwitchPaneToggles(t *testing.T) {
	f := NewSettingsForm()
	f.LoadFromProject(minimalProject())
	if f.ActivePane() != settingsPaneSidebar {
		t.Fatalf("initial pane should be sidebar")
	}
	f.SwitchPane()
	if f.ActivePane() != settingsPaneFields {
		t.Fatalf("after SwitchPane, pane should be fields")
	}
	f.SwitchPane()
	if f.ActivePane() != settingsPaneSidebar {
		t.Fatalf("second SwitchPane should return to sidebar")
	}
}

// TestSidebarMoveDownChangesSection ensures ↓ in the sidebar pane walks
// through the section list.
func TestSidebarMoveDownChangesSection(t *testing.T) {
	f := NewSettingsForm()
	f.LoadFromProject(minimalProject())

	if f.ActiveSection() != sectionGeneral {
		t.Fatalf("initial section should be General")
	}
	f.MoveDown()
	if f.ActiveSection() != sectionAutomation {
		t.Fatalf("after one MoveDown, expected Automation, got %v", f.ActiveSection())
	}
}

// TestSandboxCycleRoundtrip exercises the new v6 Sandbox cycle row.
func TestSandboxCycleRoundtrip(t *testing.T) {
	f := NewSettingsForm()
	f.LoadFromProject(minimalProject())
	row := findRow(f, "sandbox")
	if row == nil {
		t.Fatalf("sandbox row missing")
	}
	if row.CycleOptions[row.CycleIndex].Value != "auto" {
		t.Fatalf("expected starting value auto, got %q", row.CycleOptions[row.CycleIndex].Value)
	}
	for i := range f.rows {
		if f.rows[i].Key == "sandbox" {
			f.cursor = i
			break
		}
	}
	f.pane = settingsPaneFields
	if changed, _, val, _ := f.Toggle(); !changed || val != "sandbox-exec" {
		t.Fatalf("expected sandbox cycle to advance to sandbox-exec, got changed=%v val=%v", changed, val)
	}
}

// TestStatusCycleMirrorsMetadata ensures flipping Status in General
// updates the read-only Metadata mirror so the user doesn't get a stale
// view.
func TestStatusCycleMirrorsMetadata(t *testing.T) {
	f := NewSettingsForm()
	f.LoadFromProject(minimalProject())
	for i := range f.rows {
		if f.rows[i].Key == "status" {
			f.cursor = i
			break
		}
	}
	f.pane = settingsPaneFields
	f.Toggle() // active → archived
	mirror := findRow(f, "meta_status")
	if mirror == nil || mirror.Value != "archived" {
		t.Fatalf("expected meta_status mirror to read archived, got %v", mirror)
	}
}

// TestNotificationsOverrideGatesEventRows confirms per-event rows render
// disabled when OverrideEvents is off, and become enabled when it flips
// on.
func TestNotificationsOverrideGatesEventRows(t *testing.T) {
	f := NewSettingsForm()
	f.LoadFromProject(minimalProject())

	// Per-event rows should be Disabled by default (override is off).
	taskFailed := findRow(f, "notifications_event_task_failed")
	if taskFailed == nil {
		t.Fatalf("task_failed event row missing")
	}
	if !taskFailed.Disabled {
		t.Fatalf("event row should be disabled when override is off")
	}

	// Flip the override toggle.
	for i := range f.rows {
		if f.rows[i].Kind == rowKindNotifOverride {
			f.cursor = i
			break
		}
	}
	f.pane = settingsPaneFields
	f.Toggle()

	// Event rows should now be enabled.
	taskFailed = findRow(f, "notifications_event_task_failed")
	if taskFailed.Disabled {
		t.Fatalf("event row should be enabled after override toggled on")
	}
}

// TestQuietHoursOverrideGatesTimeRows: same shape as the override test
// above but for quiet-hours start/end.
func TestQuietHoursOverrideGatesTimeRows(t *testing.T) {
	f := NewSettingsForm()
	f.LoadFromProject(minimalProject())

	start := findRow(f, "notifications_quiet_start")
	if start == nil || !start.Disabled {
		t.Fatalf("quiet_start row should be disabled by default; got %v", start)
	}

	for i := range f.rows {
		if f.rows[i].Kind == rowKindNotifQuietToggle {
			f.cursor = i
			break
		}
	}
	f.pane = settingsPaneFields
	f.Toggle()

	start = findRow(f, "notifications_quiet_start")
	if start.Disabled {
		t.Fatalf("quiet_start should be enabled after quiet-hours toggle")
	}
}

// TestDangerActionsArmConfirmMode wires the danger-zone actions through
// the keyhandler stub and asserts each lands in the right confirmMode.
// We exercise maybeStartSettingsAction directly so we don't have to spin
// up a full Bubble Tea program.
func TestDangerActionsArmConfirmMode(t *testing.T) {
	cases := []struct {
		key  string
		want int
	}{
		{"danger_archive", confirmSettingsArchive},
		{"danger_regen_id", confirmSettingsRegenID},
		{"danger_reset_numbering", confirmSettingsResetNumbering},
		{"danger_prune_branches", confirmSettingsPruneBranches},
		{"danger_unregister", confirmSettingsUnregister},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			m := &Model{settingsForm: NewSettingsForm()}
			m.settingsForm.LoadFromProject(minimalProject())
			for i := range m.settingsForm.rows {
				if m.settingsForm.rows[i].Key == c.key {
					m.settingsForm.cursor = i
					break
				}
			}
			m.settingsForm.pane = settingsPaneFields
			m.maybeStartSettingsAction()
			if m.confirmMode != c.want {
				t.Errorf("%s: expected confirmMode=%d, got %d", c.key, c.want, m.confirmMode)
			}
		})
	}
}

// TestSearchOverlayMatchAndJump exercises `/` opening the overlay,
// typing a query, and `ActivateSearch` jumping to the matching row.
func TestSearchOverlayMatchAndJump(t *testing.T) {
	f := NewSettingsForm()
	f.LoadFromProject(minimalProject())

	f.OpenSearch()
	if !f.IsSearching() {
		t.Fatalf("expected IsSearching true after OpenSearch")
	}
	f.searchInput.SetValue("sandbox")
	hits := f.searchResults()
	if len(hits) == 0 {
		t.Fatalf("expected at least one hit for 'sandbox'")
	}
	// Activate first hit and confirm cursor jumped onto a row whose Key
	// matches the hit's row in the form.
	if !f.ActivateSearch() {
		t.Fatalf("ActivateSearch should report true with a non-empty result set")
	}
	if f.IsSearching() {
		t.Fatalf("ActivateSearch should close the overlay")
	}
	if f.ActivePane() != settingsPaneFields {
		t.Fatalf("ActivateSearch should focus the fields pane")
	}
	if got := f.CurrentRow(); got == nil || got.Key != "sandbox" {
		t.Fatalf("ActivateSearch should land cursor on sandbox row, got %v", got)
	}
}

// TestCopySelectedValueOnlyFromMetadata: `y` copies on Metadata, no-ops
// elsewhere.
func TestCopySelectedValueOnlyFromMetadata(t *testing.T) {
	f := NewSettingsForm()
	f.LoadFromProject(minimalProject())

	// Cursor on a non-metadata row → no copy.
	for i := range f.rows {
		if f.rows[i].Key == "name" {
			f.cursor = i
			break
		}
	}
	if _, ok := f.CopySelectedValue(); ok {
		t.Errorf("non-metadata row should not be copyable")
	}

	// Cursor on metadata row → copy.
	for i := range f.rows {
		if f.rows[i].Key == "meta_id" {
			f.cursor = i
			break
		}
	}
	val, ok := f.CopySelectedValue()
	if !ok || val != "test-id" {
		t.Errorf("expected copy value 'test-id', got %q ok=%v", val, ok)
	}
}

// TestCurrentNotificationsConfigRoundTrip ensures CurrentNotificationsConfig
// produces a proto that mirrors the form's row state.
func TestCurrentNotificationsConfigRoundTrip(t *testing.T) {
	f := NewSettingsForm()
	f.LoadFromProject(minimalProject())

	// Flip mute on.
	for i := range f.rows {
		if f.rows[i].Kind == rowKindNotifMute {
			f.cursor = i
			break
		}
	}
	f.pane = settingsPaneFields
	f.Toggle()

	cfg := f.CurrentNotificationsConfig()
	if !cfg.Muted {
		t.Errorf("CurrentNotificationsConfig should reflect Muted=true after toggle")
	}
}
