package tui

import (
	"errors"
	"strings"
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
		sectionMcp:           false,
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

// ── MCP onboarding section (v9.0 Firestorm) ───────────────────────

// mcpStatusList is a two-harness GetMcpClientStatus response: one detected but
// unconfigured, one absent.
func mcpStatusList() *pb.McpClientStatusList {
	return &pb.McpClientStatusList{
		Clients: []*pb.McpClientStatus{
			{
				Client:      "claude-code",
				DisplayName: "Claude Code",
				Detected:    true,
				Configured:  false,
				ConfigPath:  "/home/u/.claude.json",
				Message:     "Detected. Installing adds the watchfire entry to /home/u/.claude.json.",
			},
			{
				Client:      "codex",
				DisplayName: "OpenAI Codex",
				Detected:    false,
				Configured:  false,
				ConfigPath:  "/home/u/.codex/config.toml",
				Message:     "OpenAI Codex was not found on this machine. Installing returns the snippet to add to /home/u/.codex/config.toml by hand.",
			},
		},
		CustomSnippet: "{\n  \"command\": \"watchfire\",\n  \"args\": [\"mcp\", \"serve\"]\n}",
	}
}

// focusRow lands the cursor on the row with the given key and focuses the
// fields pane. Fails the test when the row is missing.
func focusRow(t *testing.T, f *SettingsForm, key string) {
	t.Helper()
	for i := range f.rows {
		if f.rows[i].Key == key {
			f.cursor = i
			f.pane = settingsPaneFields
			return
		}
	}
	t.Fatalf("row %q not found", key)
}

// TestMcpSectionCustomRowExistsBeforeFetch: the section must be navigable
// before any RPC lands, so the Custom row is always present.
func TestMcpSectionCustomRowExistsBeforeFetch(t *testing.T) {
	f := NewSettingsForm()
	f.LoadFromProject(minimalProject())

	rows := f.rowsInSection(sectionMcp)
	if len(rows) != 1 {
		t.Fatalf("expected exactly the Custom row before fetch, got %d rows", len(rows))
	}
	if got := f.rows[rows[0]].Kind; got != rowKindMcpCustom {
		t.Fatalf("expected rowKindMcpCustom, got %v", got)
	}
}

// TestMcpNeedsFetchOnSectionFocus: focusing the section (and only it) asks for
// exactly one fetch.
func TestMcpNeedsFetchOnSectionFocus(t *testing.T) {
	f := NewSettingsForm()
	f.LoadFromProject(minimalProject())

	if f.NeedsMcpFetch() {
		t.Fatalf("General section should not request an MCP fetch")
	}
	f.activeSec = sectionMcp
	if !f.NeedsMcpFetch() {
		t.Fatalf("MCP section with no status should request a fetch")
	}
	if !f.MarkMcpFetching() {
		t.Fatalf("first MarkMcpFetching should win")
	}
	if f.NeedsMcpFetch() {
		t.Fatalf("an in-flight fetch should suppress further requests")
	}
	if f.MarkMcpFetching() {
		t.Fatalf("MarkMcpFetching should refuse while a fetch is in flight")
	}

	f.SetMcpStatus(mcpStatusList(), nil)
	if f.NeedsMcpFetch() {
		t.Fatalf("a completed fetch should not be repeated")
	}
}

// TestMcpStatusBuildsOneRowPerHarness verifies the daemon's client list drives
// the rows, with the Custom row kept last.
func TestMcpStatusBuildsOneRowPerHarness(t *testing.T) {
	f := NewSettingsForm()
	f.LoadFromProject(minimalProject())
	f.SetMcpStatus(mcpStatusList(), nil)

	rows := f.rowsInSection(sectionMcp)
	if len(rows) != 3 {
		t.Fatalf("expected 2 harness rows + Custom, got %d", len(rows))
	}
	if got := f.rows[rows[0]].McpClient; got != "claude-code" {
		t.Errorf("first row should target claude-code, got %q", got)
	}
	if got := f.rows[rows[1]].McpClient; got != "codex" {
		t.Errorf("second row should target codex, got %q", got)
	}
	if got := f.rows[rows[2]].Kind; got != rowKindMcpCustom {
		t.Errorf("Custom row should be last, got kind %v", got)
	}
	if got := f.McpCustomSnippet(); got != mcpStatusList().CustomSnippet {
		t.Errorf("custom snippet should be stored verbatim, got %q", got)
	}
}

// TestMcpEnterActionPerState covers the Enter contract: install only for a
// detected-but-unconfigured harness, reveal otherwise, toggle for Custom.
func TestMcpEnterActionPerState(t *testing.T) {
	f := NewSettingsForm()
	f.LoadFromProject(minimalProject())
	f.SetMcpStatus(mcpStatusList(), nil)

	cases := []struct {
		rowKey     string
		wantAction mcpEnterAction
		wantClient string
	}{
		{"mcp_claude-code", mcpActionInstall, "claude-code"},
		{"mcp_codex", mcpActionReveal, "codex"},
		{"mcp_custom", mcpActionToggleCustom, ""},
	}
	for _, c := range cases {
		t.Run(c.rowKey, func(t *testing.T) {
			focusRow(t, f, c.rowKey)
			action, client := f.McpEnterAction()
			if action != c.wantAction || client != c.wantClient {
				t.Fatalf("got (%v, %q), want (%v, %q)", action, client, c.wantAction, c.wantClient)
			}
		})
	}

	// An already-configured harness reveals rather than re-installing.
	list := mcpStatusList()
	list.Clients[0].Configured = true
	f.SetMcpStatus(list, nil)
	focusRow(t, f, "mcp_claude-code")
	if action, _ := f.McpEnterAction(); action != mcpActionReveal {
		t.Fatalf("configured harness should reveal, got %v", action)
	}
}

// TestMcpInstallFlipsToConfigured walks the install lifecycle the way the
// message handler does: begin (spinner), then apply the response.
func TestMcpInstallFlipsToConfigured(t *testing.T) {
	f := NewSettingsForm()
	f.LoadFromProject(minimalProject())
	f.SetMcpStatus(mcpStatusList(), nil)

	if !f.BeginMcpInstall("claude-code") {
		t.Fatalf("BeginMcpInstall should start for a fresh install")
	}
	if !f.McpBusy() {
		t.Fatalf("form should report busy while the install is in flight")
	}
	if f.BeginMcpInstall("codex") {
		t.Fatalf("a second concurrent install should be refused")
	}

	f.SetMcpInstalled("claude-code", &pb.McpClientStatus{
		Client:      "claude-code",
		DisplayName: "Claude Code",
		Detected:    true,
		Configured:  true,
		ConfigPath:  "/home/u/.claude.json",
		Message:     "Registered the Watchfire MCP server with Claude Code.",
	}, nil)

	if f.McpBusy() {
		t.Fatalf("form should be idle after the install response")
	}
	st := f.McpStatusFor("claude-code")
	if st == nil || !st.Configured {
		t.Fatalf("claude-code should read configured after install, got %v", st)
	}
	// Enter on the now-configured row must not install again.
	focusRow(t, f, "mcp_claude-code")
	if action, _ := f.McpEnterAction(); action != mcpActionReveal {
		t.Fatalf("configured harness should reveal, got %v", action)
	}
}

// TestMcpInstallFailureRendersManualInstructions: a failed install comes back
// as a normal response with configured=false plus manual instructions, and
// those must show up in the rendered section.
func TestMcpInstallFailureRendersManualInstructions(t *testing.T) {
	f := NewSettingsForm()
	f.SetSize(120, 40)
	f.LoadFromProject(minimalProject())
	f.SetMcpStatus(mcpStatusList(), nil)

	const manual = "Could not register automatically: config unparseable"
	f.BeginMcpInstall("claude-code")
	f.SetMcpInstalled("claude-code", &pb.McpClientStatus{
		Client:      "claude-code",
		DisplayName: "Claude Code",
		Detected:    true,
		Configured:  false,
		Message:     manual,
	}, nil)

	f.activeSec = sectionMcp
	f.sidebarCursor = 0
	for i, def := range settingsSections {
		if def.ID == sectionMcp {
			f.sidebarCursor = i
		}
	}
	view := f.View()
	if !strings.Contains(view, "Could not register automatically") {
		t.Fatalf("manual instructions should render inline; view was:\n%s", view)
	}
}

// TestMcpCustomRowShowsSnippetVerbatim: the Custom row renders the snippet the
// daemon returned, not a locally-authored copy.
func TestMcpCustomRowShowsSnippetVerbatim(t *testing.T) {
	f := NewSettingsForm()
	f.SetSize(120, 40)
	f.LoadFromProject(minimalProject())
	f.SetMcpStatus(mcpStatusList(), nil)
	for i, def := range settingsSections {
		if def.ID == sectionMcp {
			f.sidebarCursor = i
			f.activeSec = def.ID
		}
	}

	view := f.View()
	if strings.Contains(view, "\"mcp\", \"serve\"") {
		t.Fatalf("snippet should stay collapsed until Enter; view was:\n%s", view)
	}

	focusRow(t, f, "mcp_custom")
	f.ToggleMcpCustom()
	view = f.View()
	for _, want := range []string{"\"command\": \"watchfire\"", "\"mcp\", \"serve\"", "--print"} {
		if !strings.Contains(view, want) {
			t.Errorf("expanded Custom row should contain %q; view was:\n%s", want, view)
		}
	}
}

// TestMcpFetchErrorOffersRetry: a failed fetch renders inline and Enter on a
// harness row retries instead of acting on stale state.
func TestMcpFetchErrorOffersRetry(t *testing.T) {
	f := NewSettingsForm()
	f.LoadFromProject(minimalProject())
	f.MarkMcpFetching()
	f.SetMcpStatus(nil, errors.New("daemon unreachable"))

	if !f.McpFetchFailed() {
		t.Fatalf("expected McpFetchFailed after an errored fetch")
	}
	if f.NeedsMcpFetch() {
		t.Fatalf("a failed fetch should not auto-retry on every keypress")
	}
	// Seed a client row so Enter has something to land on, then confirm the
	// error path wins over the per-client action.
	f.mcpClients = mcpStatusList().Clients
	f.rebuildRows()
	focusRow(t, f, "mcp_claude-code")
	if action, _ := f.McpEnterAction(); action != mcpActionRetryFetch {
		t.Fatalf("expected retry action while the fetch error stands, got %v", action)
	}
}

// TestMcpRowEnterIsIgnoredOutsideMcpSection guards the key-handler seam: a
// non-MCP row must fall through to the existing dispatch.
func TestMcpRowEnterIsIgnoredOutsideMcpSection(t *testing.T) {
	m := &Model{settingsForm: NewSettingsForm()}
	m.settingsForm.LoadFromProject(minimalProject())
	focusRow(t, m.settingsForm, "name")

	if cmd, handled := m.handleMcpRowEnter(); handled || cmd != nil {
		t.Fatalf("Enter on a non-MCP row should not be handled by the MCP path")
	}

	focusRow(t, m.settingsForm, "mcp_custom")
	if _, handled := m.handleMcpRowEnter(); !handled {
		t.Fatalf("Enter on the Custom row should be handled by the MCP path")
	}
	if !m.settingsForm.mcpShowCustom {
		t.Fatalf("Enter on the Custom row should expand the snippet")
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
