// Package tui — Project Settings tab.
//
// v6 (#0090 + #0091) replaced the flat 7-row form with a macOS-style
// sidebar + content-pane layout. The sidebar lists eight sections (General,
// Automation, Notifications, Integrations, MCP, Metadata, Secrets, Danger
// zone); the right pane renders the focused section's rows. Tab/Shift+Tab
// moves the section cursor; ↑↓ moves the row cursor inside the active
// section.
//
// v9.0 Firestorm added MCP: a thin onboarding surface over
// SettingsService.GetMcpClientStatus / InstallMcpClient. It holds no config
// state of its own — every badge, path and instruction block is what the
// daemon reported.
//
// `/` opens the same search overlay shape used by the Flare global settings
// (filter-as-you-type, Enter jumps to the matching row).
//
// The form is RPC-driven on every mutation: toggles fire updateProjectCmd
// immediately, integration bindings fire setProjectIntegrationBindingsCmd,
// danger-zone actions wait for a y/N confirmation captured by
// `keyhandler.handleConfirmKey`.
package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/agent/backend"
	pb "github.com/watchfire-io/watchfire/proto"
)

// FieldType defines the type of a settings row.
type FieldType int

const (
	fieldText FieldType = iota
	fieldToggle
	fieldCycle
	fieldReadOnly
	fieldAction
)

// CycleOption is a single option in a cycling field.
type CycleOption struct {
	Value   string
	Display string
}

// settingsSectionID enumerates the sections of the per-project Settings tab.
// Order matches the sidebar. MCP (v9.0 Firestorm) sits next to Integrations
// because it is the same kind of concern: wiring Watchfire up to something
// outside it.
type settingsSectionID int

const (
	sectionGeneral settingsSectionID = iota
	sectionAutomation
	sectionNotifications
	sectionIntegrations
	sectionMcp
	sectionMetadata
	sectionSecrets
	sectionDanger
	sectionCount
)

type settingsSectionDef struct {
	ID    settingsSectionID
	Label string
}

var settingsSections = []settingsSectionDef{
	{sectionGeneral, "General"},
	{sectionAutomation, "Automation"},
	{sectionNotifications, "Notifications"},
	{sectionIntegrations, "Integrations"},
	{sectionMcp, "MCP"},
	{sectionMetadata, "Metadata"},
	{sectionSecrets, "Secrets"},
	{sectionDanger, "Danger zone"},
}

// settingsRowKind classifies a row so the View / key handler can branch
// without stringly-typing on `Key`.
type settingsRowKind int

const (
	rowKindGeneric settingsRowKind = iota
	// Notifications rows.
	rowKindNotifMute
	rowKindNotifOverride
	rowKindNotifEvent
	rowKindNotifQuietToggle
	rowKindNotifQuietStart
	rowKindNotifQuietEnd
	// Integrations rows.
	rowKindIntegGitHub
	rowKindIntegSlackChannel
	rowKindIntegDiscordGuild
	// MCP onboarding rows (v9.0 Firestorm): one per known harness, plus a
	// trailing Custom row that reveals the generic snippet.
	rowKindMcpClient
	rowKindMcpCustom
	// Metadata copy-target rows are rowKindGeneric + readonly.
	// Secrets editor row.
	rowKindSecretsEdit
	// Danger-zone action rows.
	rowKindDangerArchive
	rowKindDangerRegenID
	rowKindDangerResetNumbering
	rowKindDangerPruneBranches
	rowKindDangerUnregister
)

// SettingsField is a single row.
type SettingsField struct {
	Label        string
	Key          string
	Value        string
	BoolValue    bool
	Type         FieldType
	CycleOptions []CycleOption
	CycleIndex   int
	Section      settingsSectionID
	Kind         settingsRowKind
	// EventKey identifies which notification event a rowKindNotifEvent row
	// targets ("task_failed" / "run_complete" / "weekly_digest"). Empty
	// for non-event rows.
	EventKey string
	// Disabled marks a row that renders dimmed and ignores input. Used by
	// the Notifications per-event rows when override mode is off so the
	// user sees the inherited values without being able to edit them.
	Disabled bool
	// CopyValue overrides the displayed value when copying to the
	// clipboard (Metadata `y` action). Empty falls back to Value.
	CopyValue string
	// McpClient is the stable client key ("claude-code", "codex", ...) a
	// rowKindMcpClient row targets. Empty for every other row.
	McpClient string
}

// settingsPaneFocus tracks which side of the layout owns navigation.
type settingsPaneFocus int

const (
	settingsPaneSidebar settingsPaneFocus = iota
	settingsPaneFields
)

// settingsSearchHit is one entry in the search index.
type settingsSearchHit struct {
	Section  settingsSectionID
	Label    string
	Keywords []string
	RowIndex int // index into the global rows slice
}

// SettingsForm manages the per-project settings tab.
type SettingsForm struct {
	rows   []SettingsField
	cursor int // index into rows; always in the active section's range when paneFields focused

	// Sidebar nav.
	pane          settingsPaneFocus
	activeSec     settingsSectionID
	sidebarCursor int

	editing bool
	input   textinput.Model
	width   int
	height  int

	// Search overlay.
	searchOpen   bool
	searchInput  textinput.Model
	searchCursor int

	// ── MCP onboarding (v9.0 Firestorm) ──────────────────────────
	// Every value here comes from SettingsService.GetMcpClientStatus /
	// InstallMcpClient. The TUI is a pure thin client: it never reads or
	// writes a harness config itself, so an empty mcpClients slice means
	// "not fetched yet", never "no clients".
	mcpClients []*pb.McpClientStatus
	// mcpCustomSnippet is the generic MCP block for any other client,
	// rendered verbatim as the daemon returned it.
	mcpCustomSnippet string
	mcpFetched       bool
	mcpLoading       bool
	mcpErr           string
	// mcpInstalling is the client key with an InstallMcpClient RPC in
	// flight (empty when idle) — drives the inline spinner.
	mcpInstalling string
	// mcpDetail holds the per-client message revealed by Enter: the install
	// outcome, or the manual instructions when the harness is absent.
	mcpDetail map[string]string
	// mcpShowCustom expands the Custom row's copyable snippet block.
	mcpShowCustom   bool
	mcpSpinnerFrame int

	// Loaded project snapshot (used for read-only / compute fields).
	project *pb.Project
	// projectPath caches the project's filesystem path (read from
	// `project.Path` at load time) so Secrets + Metadata can resolve their
	// derived values without round-tripping the proto on every render.
	projectPath string
}

// NewSettingsForm builds an empty form. Loaded via LoadFromProject.
func NewSettingsForm() *SettingsForm {
	ti := textinput.New()
	ti.CharLimit = 200

	si := textinput.New()
	si.CharLimit = 80
	si.Placeholder = "Search settings"

	return &SettingsForm{
		input:         ti,
		searchInput:   si,
		pane:          settingsPaneSidebar,
		activeSec:     sectionGeneral,
		sidebarCursor: 0,
		mcpDetail:     map[string]string{},
	}
}

// LoadFromProject populates rows from project data.
func (s *SettingsForm) LoadFromProject(project *pb.Project) {
	s.project = project
	s.projectPath = project.Path
	s.rebuildRows()
	// Snap cursor to first row of active section if out-of-range.
	s.snapCursor()
}

// rebuildRows recomputes the row list from the current project snapshot.
// Called by LoadFromProject + after a mutation (e.g. toggling override
// shows / hides per-event rows).
func (s *SettingsForm) rebuildRows() {
	p := s.project
	if p == nil {
		s.rows = nil
		return
	}
	rows := make([]SettingsField, 0, 32)

	// ── General ──────────────────────────────────────────────────
	agentOptions := buildAgentCycleOptions()
	agentIdx := agentCycleIndex(agentOptions, p.DefaultAgent)
	rows = append(rows,
		SettingsField{Section: sectionGeneral, Label: "Name", Key: "name", Value: p.Name, Type: fieldText},
		SettingsField{Section: sectionGeneral, Label: "Color", Key: "color", Value: p.Color, Type: fieldText},
		SettingsField{Section: sectionGeneral, Label: "Agent", Key: "default_agent", Type: fieldCycle, CycleOptions: agentOptions, CycleIndex: agentIdx},
		SettingsField{Section: sectionGeneral, Label: "Sandbox", Key: "sandbox", Type: fieldCycle, CycleOptions: sandboxCycleOptions(), CycleIndex: sandboxCycleIndex(p.Sandbox)},
		SettingsField{Section: sectionGeneral, Label: "Status", Key: "status", Type: fieldCycle, CycleOptions: statusCycleOptions(), CycleIndex: statusCycleIndex(p.Status)},
	)

	// ── Automation ───────────────────────────────────────────────
	rows = append(rows,
		SettingsField{Section: sectionAutomation, Label: "Auto-merge", Key: "auto_merge", BoolValue: p.AutoMerge, Type: fieldToggle},
		SettingsField{Section: sectionAutomation, Label: "Auto-delete branch", Key: "auto_delete_branch", BoolValue: p.AutoDeleteBranch, Type: fieldToggle},
		SettingsField{Section: sectionAutomation, Label: "Auto-start tasks", Key: "auto_start_tasks", BoolValue: p.AutoStartTasks, Type: fieldToggle},
	)

	// ── Notifications ────────────────────────────────────────────
	muted := false
	override := false
	var quietOverride *pb.QuietHoursConfig
	eventPrefs := map[string]*pb.ProjectEventPref{}
	if p.Notifications != nil {
		muted = p.Notifications.Muted
		override = p.Notifications.OverrideEvents
		quietOverride = p.Notifications.QuietHoursOverride
		for k, v := range p.Notifications.Events {
			eventPrefs[k] = v
		}
	}
	rows = append(rows,
		SettingsField{Section: sectionNotifications, Label: "Mute notifications", Key: "notifications_muted", BoolValue: muted, Type: fieldToggle, Kind: rowKindNotifMute},
		SettingsField{Section: sectionNotifications, Label: "Override per-event preferences", Key: "notifications_override", BoolValue: override, Type: fieldToggle, Kind: rowKindNotifOverride},
	)
	for _, ev := range []struct {
		key, label string
	}{
		{"task_failed", "Notify on task failure"},
		{"run_complete", "Notify on run complete"},
		{"weekly_digest", "Send weekly digest"},
	} {
		enabled := false
		if pref, ok := eventPrefs[ev.key]; ok && pref != nil {
			enabled = pref.Enabled
		}
		rows = append(rows, SettingsField{
			Section:   sectionNotifications,
			Label:     ev.label,
			Key:       "notifications_event_" + ev.key,
			BoolValue: enabled,
			Type:      fieldToggle,
			Kind:      rowKindNotifEvent,
			EventKey:  ev.key,
			Disabled:  !override,
		})
	}
	quietEnabled := false
	quietStart := "22:00"
	quietEnd := "08:00"
	if quietOverride != nil {
		quietEnabled = quietOverride.Enabled
		if quietOverride.Start != "" {
			quietStart = quietOverride.Start
		}
		if quietOverride.End != "" {
			quietEnd = quietOverride.End
		}
	}
	rows = append(rows,
		SettingsField{Section: sectionNotifications, Label: "Quiet hours override", Key: "notifications_quiet_toggle", BoolValue: quietEnabled, Type: fieldToggle, Kind: rowKindNotifQuietToggle},
		SettingsField{Section: sectionNotifications, Label: "Quiet hours start", Key: "notifications_quiet_start", Value: quietStart, Type: fieldText, Kind: rowKindNotifQuietStart, Disabled: !quietEnabled},
		SettingsField{Section: sectionNotifications, Label: "Quiet hours end", Key: "notifications_quiet_end", Value: quietEnd, Type: fieldText, Kind: rowKindNotifQuietEnd, Disabled: !quietEnabled},
	)

	// ── Integrations ─────────────────────────────────────────────
	autoPR := false
	slackChannel := ""
	discordGuild := ""
	if p.Integrations != nil {
		autoPR = p.Integrations.GithubAutoPr
		slackChannel = p.Integrations.SlackChannel
		discordGuild = p.Integrations.DiscordGuildId
	}
	rows = append(rows,
		SettingsField{Section: sectionIntegrations, Label: "GitHub auto-PR for this project", Key: "integ_github_auto_pr", BoolValue: autoPR, Type: fieldToggle, Kind: rowKindIntegGitHub},
		SettingsField{Section: sectionIntegrations, Label: "Slack channel", Key: "integ_slack_channel", Value: slackChannel, Type: fieldText, Kind: rowKindIntegSlackChannel},
		SettingsField{Section: sectionIntegrations, Label: "Discord guild ID", Key: "integ_discord_guild", Value: discordGuild, Type: fieldText, Kind: rowKindIntegDiscordGuild},
	)

	// ── MCP (v9.0 Firestorm) ─────────────────────────────────────
	// One row per harness the daemon reported, then a Custom row. The
	// Custom row exists before the first fetch lands so the section is
	// always navigable.
	for _, c := range s.mcpClients {
		if c == nil {
			continue
		}
		rows = append(rows, SettingsField{
			Section:   sectionMcp,
			Label:     c.DisplayName,
			Key:       "mcp_" + c.Client,
			Type:      fieldAction,
			Kind:      rowKindMcpClient,
			McpClient: c.Client,
		})
	}
	rows = append(rows, SettingsField{
		Section: sectionMcp,
		Label:   "Custom",
		Key:     "mcp_custom",
		Type:    fieldAction,
		Kind:    rowKindMcpCustom,
	})

	// ── Metadata ─────────────────────────────────────────────────
	branch := resolveDefaultBranch(p.Path)
	rows = append(rows,
		SettingsField{Section: sectionMetadata, Label: "Project ID", Key: "meta_id", Value: p.ProjectId, CopyValue: p.ProjectId, Type: fieldReadOnly},
		SettingsField{Section: sectionMetadata, Label: "Path", Key: "meta_path", Value: p.Path, CopyValue: p.Path, Type: fieldReadOnly},
		SettingsField{Section: sectionMetadata, Label: "Default branch", Key: "meta_branch", Value: branch, CopyValue: branch, Type: fieldReadOnly},
		SettingsField{Section: sectionMetadata, Label: "Created", Key: "meta_created", Value: formatTime(p.CreatedAt.AsTime()), CopyValue: p.CreatedAt.AsTime().Format(time.RFC3339), Type: fieldReadOnly},
		SettingsField{Section: sectionMetadata, Label: "Updated", Key: "meta_updated", Value: formatTime(p.UpdatedAt.AsTime()), CopyValue: p.UpdatedAt.AsTime().Format(time.RFC3339), Type: fieldReadOnly},
		SettingsField{Section: sectionMetadata, Label: "Next task #", Key: "meta_next_task", Value: fmt.Sprintf("%d", p.NextTaskNumber), CopyValue: fmt.Sprintf("%d", p.NextTaskNumber), Type: fieldReadOnly},
		SettingsField{Section: sectionMetadata, Label: "Status", Key: "meta_status", Value: p.Status, CopyValue: p.Status, Type: fieldReadOnly},
	)

	// ── Secrets ──────────────────────────────────────────────────
	secretsPath := filepath.Join(p.Path, ".watchfire", "secrets", "instructions.md")
	rows = append(rows, SettingsField{
		Section: sectionSecrets,
		Label:   "instructions.md",
		Key:     "secrets_instructions",
		Value:   secretsFileSummary(secretsPath),
		Type:    fieldAction,
		Kind:    rowKindSecretsEdit,
	})

	// ── Danger zone ──────────────────────────────────────────────
	archiveLabel := "Archive project"
	if p.Status == "archived" {
		archiveLabel = "Unarchive project"
	}
	rows = append(rows,
		SettingsField{Section: sectionDanger, Label: archiveLabel, Key: "danger_archive", Type: fieldAction, Kind: rowKindDangerArchive},
		SettingsField{Section: sectionDanger, Label: "Regenerate project ID", Key: "danger_regen_id", Type: fieldAction, Kind: rowKindDangerRegenID},
		SettingsField{Section: sectionDanger, Label: "Reset task numbering", Key: "danger_reset_numbering", Type: fieldAction, Kind: rowKindDangerResetNumbering},
		SettingsField{Section: sectionDanger, Label: "Prune merged branches", Key: "danger_prune_branches", Type: fieldAction, Kind: rowKindDangerPruneBranches},
		SettingsField{Section: sectionDanger, Label: "Unregister project", Key: "danger_unregister", Type: fieldAction, Kind: rowKindDangerUnregister},
	)

	s.rows = rows
}

// rowsInSection returns the indices of rows belonging to a section. Empty
// when the section has no rows (shouldn't happen with the current layout,
// but defensive).
func (s *SettingsForm) rowsInSection(sec settingsSectionID) []int {
	out := []int{}
	for i, r := range s.rows {
		if r.Section == sec {
			out = append(out, i)
		}
	}
	return out
}

// snapCursor moves the cursor onto the first row of the active section
// when it's not already in-bounds for that section.
func (s *SettingsForm) snapCursor() {
	rows := s.rowsInSection(s.activeSec)
	if len(rows) == 0 {
		return
	}
	for _, i := range rows {
		if i == s.cursor {
			return
		}
	}
	s.cursor = rows[0]
}

// SetSize updates dimensions. Sidebar takes ~18 cols, content fills rest.
func (s *SettingsForm) SetSize(width, height int) {
	s.width = width
	s.height = height
	contentWidth := width - 22
	if contentWidth < 20 {
		contentWidth = 20
	}
	s.input.Width = contentWidth - 12
	if s.input.Width < 8 {
		s.input.Width = 8
	}
	s.searchInput.Width = width - 8
	if s.searchInput.Width < 10 {
		s.searchInput.Width = 10
	}
}

// MoveUp / MoveDown — semantics depend on focused pane.
func (s *SettingsForm) MoveUp() {
	if s.editing {
		return
	}
	if s.searchOpen {
		s.moveSearchUp()
		return
	}
	if s.pane == settingsPaneSidebar {
		if s.sidebarCursor > 0 {
			s.sidebarCursor--
			s.activeSec = settingsSections[s.sidebarCursor].ID
			s.snapCursor()
		}
		return
	}
	rows := s.rowsInSection(s.activeSec)
	for i, r := range rows {
		if r == s.cursor && i > 0 {
			s.cursor = rows[i-1]
			return
		}
	}
}

func (s *SettingsForm) MoveDown() {
	if s.editing {
		return
	}
	if s.searchOpen {
		s.moveSearchDown()
		return
	}
	if s.pane == settingsPaneSidebar {
		if s.sidebarCursor < len(settingsSections)-1 {
			s.sidebarCursor++
			s.activeSec = settingsSections[s.sidebarCursor].ID
			s.snapCursor()
		}
		return
	}
	rows := s.rowsInSection(s.activeSec)
	for i, r := range rows {
		if r == s.cursor && i < len(rows)-1 {
			s.cursor = rows[i+1]
			return
		}
	}
}

// SwitchPane Tab/Shift+Tab toggle between sidebar and fields.
func (s *SettingsForm) SwitchPane() {
	if s.editing || s.searchOpen {
		return
	}
	if s.pane == settingsPaneSidebar {
		s.pane = settingsPaneFields
		s.snapCursor()
	} else {
		s.pane = settingsPaneSidebar
	}
}

// ActivePane reports which side has focus.
func (s *SettingsForm) ActivePane() settingsPaneFocus { return s.pane }

// ActiveSection exposes the focused section for tests.
func (s *SettingsForm) ActiveSection() settingsSectionID { return s.activeSec }

// CurrentRow returns the row under the field cursor, or nil.
func (s *SettingsForm) CurrentRow() *SettingsField {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return nil
	}
	return &s.rows[s.cursor]
}

// Toggle flips a boolean / cycles a cycle field. Returns (changed, key,
// value, kind) — the caller branches on Kind to decide which RPC to fire.
func (s *SettingsForm) Toggle() (changed bool, key string, value interface{}, kind settingsRowKind) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return false, "", nil, rowKindGeneric
	}
	f := &s.rows[s.cursor]
	if f.Disabled {
		return false, "", nil, f.Kind
	}
	switch f.Type {
	case fieldToggle:
		f.BoolValue = !f.BoolValue
		s.applyToggleSideEffects(f)
		return true, f.Key, f.BoolValue, f.Kind
	case fieldCycle:
		if len(f.CycleOptions) == 0 {
			return false, "", nil, f.Kind
		}
		f.CycleIndex = (f.CycleIndex + 1) % len(f.CycleOptions)
		val := f.CycleOptions[f.CycleIndex].Value
		// Mirror status cycles back to the Metadata read-only mirror.
		if f.Key == "status" {
			s.mirrorMetadataStatus(val)
		}
		return true, f.Key, val, f.Kind
	}
	return false, "", nil, f.Kind
}

// applyToggleSideEffects keeps dependent rows in sync when a parent
// toggle flips. The override / quiet-toggle rows gate the visible /
// editable state of their child rows.
func (s *SettingsForm) applyToggleSideEffects(f *SettingsField) {
	switch f.Kind {
	case rowKindNotifOverride:
		for i := range s.rows {
			if s.rows[i].Kind == rowKindNotifEvent {
				s.rows[i].Disabled = !f.BoolValue
			}
		}
	case rowKindNotifQuietToggle:
		for i := range s.rows {
			if s.rows[i].Kind == rowKindNotifQuietStart || s.rows[i].Kind == rowKindNotifQuietEnd {
				s.rows[i].Disabled = !f.BoolValue
			}
		}
	}
}

// mirrorMetadataStatus updates the read-only Metadata status row when the
// General/Status cycle changes — the user shouldn't have to click into
// Metadata to see the new value.
func (s *SettingsForm) mirrorMetadataStatus(status string) {
	for i := range s.rows {
		if s.rows[i].Key == "meta_status" {
			s.rows[i].Value = status
			s.rows[i].CopyValue = status
		}
	}
}

// StartEdit enters text-input mode on the current row (if editable).
// Returns true when an edit was started, false otherwise.
func (s *SettingsForm) StartEdit() bool {
	f := s.CurrentRow()
	if f == nil || f.Disabled || f.Type != fieldText {
		return false
	}
	s.editing = true
	s.input.SetValue(f.Value)
	s.input.Focus()
	return true
}

// FinishEdit confirms the current edit. Returns the change descriptor;
// caller fires the appropriate RPC based on Kind.
type EditOutcome struct {
	Changed bool
	Key     string
	Value   string
	Kind    settingsRowKind
	Err     string
}

// FinishEdit commits the in-progress edit.
func (s *SettingsForm) FinishEdit() EditOutcome {
	if !s.editing {
		return EditOutcome{}
	}
	s.editing = false
	s.input.Blur()

	f := s.CurrentRow()
	if f == nil {
		return EditOutcome{}
	}
	newVal := strings.TrimSpace(s.input.Value())

	switch f.Key {
	case "color":
		if newVal != "" && !isValidColor(newVal) {
			return EditOutcome{Err: "color must be #RGB or #RRGGBB"}
		}
	case "notifications_quiet_start", "notifications_quiet_end":
		if newVal != "" && !timeOfDayRegex.MatchString(newVal) {
			return EditOutcome{Err: "invalid time (expected HH:MM)"}
		}
	}

	if newVal == f.Value {
		return EditOutcome{}
	}
	f.Value = newVal
	return EditOutcome{Changed: true, Key: f.Key, Value: newVal, Kind: f.Kind}
}

// CancelEdit aborts the in-progress edit without applying.
func (s *SettingsForm) CancelEdit() {
	s.editing = false
	s.input.Blur()
}

// IsEditing reports whether a field is being edited.
func (s *SettingsForm) IsEditing() bool { return s.editing }

// IsSearching reports whether the search overlay has focus.
func (s *SettingsForm) IsSearching() bool { return s.searchOpen }

// InputModel returns the active text input (search overlay > inline edit).
func (s *SettingsForm) InputModel() *textinput.Model {
	if s.searchOpen {
		return &s.searchInput
	}
	return &s.input
}

// UpdateInput is a stale shim retained for compatibility — Bubble Tea's
// Update protocol works directly on InputModel(), so we don't need to
// pull a copy back. Kept so older callers don't break.
func (s *SettingsForm) UpdateInput(msg interface{}) {
	if ti, ok := msg.(textinput.Model); ok {
		s.input = ti
	}
}

// OpenSearch enters search mode. Esc / Enter exit.
func (s *SettingsForm) OpenSearch() {
	if s.editing {
		return
	}
	s.searchOpen = true
	s.searchCursor = 0
	s.searchInput.SetValue("")
	s.searchInput.Focus()
}

// CloseSearch exits search mode without jumping.
func (s *SettingsForm) CloseSearch() {
	s.searchOpen = false
	s.searchInput.Blur()
	s.searchInput.SetValue("")
	s.searchCursor = 0
}

// ActivateSearch jumps to the highlighted hit and closes the overlay.
// Returns true when a target was activated.
func (s *SettingsForm) ActivateSearch() bool {
	if !s.searchOpen {
		return false
	}
	hits := s.searchResults()
	if len(hits) == 0 || s.searchCursor < 0 || s.searchCursor >= len(hits) {
		return false
	}
	hit := hits[s.searchCursor]
	s.activeSec = hit.Section
	for i, def := range settingsSections {
		if def.ID == hit.Section {
			s.sidebarCursor = i
			break
		}
	}
	s.cursor = hit.RowIndex
	s.pane = settingsPaneFields
	s.CloseSearch()
	return true
}

func (s *SettingsForm) moveSearchUp() {
	if s.searchCursor > 0 {
		s.searchCursor--
	}
}

func (s *SettingsForm) moveSearchDown() {
	if s.searchCursor < len(s.searchResults())-1 {
		s.searchCursor++
	}
}

// searchResults filters the search index by the current query.
func (s *SettingsForm) searchResults() []settingsSearchHit {
	q := strings.TrimSpace(strings.ToLower(s.searchInput.Value()))
	if q == "" {
		return nil
	}
	tokens := strings.Fields(q)
	out := []settingsSearchHit{}
	for i, r := range s.rows {
		hay := strings.ToLower(r.Label + " " + sectionLabel(r.Section) + " " + r.Key)
		ok := true
		for _, t := range tokens {
			if !strings.Contains(hay, t) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, settingsSearchHit{
				Section:  r.Section,
				Label:    r.Label,
				RowIndex: i,
			})
		}
	}
	return out
}

// CopySelectedValue returns the value of the focused row that should be
// copied to the clipboard. Empty when the row is not copyable. Used by
// the `y` keybinding on the Metadata section.
func (s *SettingsForm) CopySelectedValue() (string, bool) {
	f := s.CurrentRow()
	if f == nil || f.Section != sectionMetadata {
		return "", false
	}
	if f.CopyValue != "" {
		return f.CopyValue, true
	}
	return f.Value, f.Value != ""
}

// SecretsPath returns the absolute path to the project's
// secrets/instructions.md file. Used by the `e` keybinding on the
// Secrets section to launch $EDITOR.
func (s *SettingsForm) SecretsPath() string {
	if s.projectPath == "" {
		return ""
	}
	return filepath.Join(s.projectPath, ".watchfire", "secrets", "instructions.md")
}

// CurrentNotificationsConfig returns a fully-populated proto block built
// from the current row state. Caller stuffs this into UpdateProjectRequest
// when any notification row changes.
func (s *SettingsForm) CurrentNotificationsConfig() *pb.ProjectNotifications {
	out := &pb.ProjectNotifications{
		Events: map[string]*pb.ProjectEventPref{},
	}
	for _, r := range s.rows {
		switch r.Kind {
		case rowKindNotifMute:
			out.Muted = r.BoolValue
		case rowKindNotifOverride:
			out.OverrideEvents = r.BoolValue
		case rowKindNotifEvent:
			out.Events[r.EventKey] = &pb.ProjectEventPref{Enabled: r.BoolValue}
		case rowKindNotifQuietToggle:
			if out.QuietHoursOverride == nil {
				out.QuietHoursOverride = &pb.QuietHoursConfig{}
			}
			out.QuietHoursOverride.Enabled = r.BoolValue
		case rowKindNotifQuietStart:
			if out.QuietHoursOverride == nil {
				out.QuietHoursOverride = &pb.QuietHoursConfig{}
			}
			out.QuietHoursOverride.Start = r.Value
		case rowKindNotifQuietEnd:
			if out.QuietHoursOverride == nil {
				out.QuietHoursOverride = &pb.QuietHoursConfig{}
			}
			out.QuietHoursOverride.End = r.Value
		}
	}
	// If no override is in effect AND the quiet-hours toggle is off, drop
	// the QuietHoursOverride block so the project inherits cleanly.
	if out.QuietHoursOverride != nil && !out.QuietHoursOverride.Enabled &&
		out.QuietHoursOverride.Start == "22:00" && out.QuietHoursOverride.End == "08:00" {
		out.QuietHoursOverride = nil
	}
	return out
}

// ── MCP onboarding (v9.0 Firestorm) ──────────────────────────────
//
// The form holds no MCP truth of its own: every flag below is either a
// request-lifecycle marker (loading / installing) or a verbatim copy of what
// SettingsService.GetMcpClientStatus / InstallMcpClient returned.

// NeedsMcpFetch reports whether the MCP section has focus and still has no
// status to render. The key handler checks this after every navigation so
// focusing the section triggers exactly one GetMcpClientStatus RPC.
func (s *SettingsForm) NeedsMcpFetch() bool {
	return s.activeSec == sectionMcp && !s.mcpFetched && !s.mcpLoading
}

// MarkMcpFetching flips the in-flight flag and clears any previous error.
// Returns false when a fetch is already running, so a burst of navigation
// keys can't fan out into duplicate RPCs.
func (s *SettingsForm) MarkMcpFetching() bool {
	if s.mcpLoading {
		return false
	}
	s.mcpLoading = true
	s.mcpErr = ""
	return true
}

// SetMcpStatus applies a GetMcpClientStatus response (or its error). A failed
// fetch still marks the section fetched — the error renders inline with a
// retry hint rather than re-firing the RPC on every keypress.
func (s *SettingsForm) SetMcpStatus(list *pb.McpClientStatusList, err error) {
	s.mcpLoading = false
	s.mcpFetched = true
	if err != nil {
		s.mcpErr = err.Error()
		return
	}
	s.mcpErr = ""
	s.mcpClients = list.GetClients()
	s.mcpCustomSnippet = list.GetCustomSnippet()
	s.rebuildPreservingCursor()
}

// McpFetchFailed reports whether the last GetMcpClientStatus attempt errored.
func (s *SettingsForm) McpFetchFailed() bool { return s.mcpErr != "" }

// BeginMcpInstall marks an InstallMcpClient RPC as in flight for clientKey.
// Returns false when the key is empty or another install is already running —
// the daemon writes user-level config files, so we keep it one at a time.
func (s *SettingsForm) BeginMcpInstall(clientKey string) bool {
	if clientKey == "" || s.mcpInstalling != "" {
		return false
	}
	s.mcpInstalling = clientKey
	s.mcpSpinnerFrame = 0
	delete(s.mcpDetail, clientKey)
	return true
}

// SetMcpInstalled applies an InstallMcpClient response. The response carries
// post-install detected/configured state for that one client, so we splice it
// into the list rather than re-fetching. Its message is revealed inline — that
// is where the manual instructions live when the install could not be done
// automatically.
func (s *SettingsForm) SetMcpInstalled(clientKey string, st *pb.McpClientStatus, err error) {
	if s.mcpInstalling == clientKey {
		s.mcpInstalling = ""
	}
	if s.mcpDetail == nil {
		s.mcpDetail = map[string]string{}
	}
	if err != nil {
		s.mcpDetail[clientKey] = err.Error()
		return
	}
	if st == nil {
		return
	}
	s.mcpDetail[clientKey] = st.GetMessage()
	for i, c := range s.mcpClients {
		if c != nil && c.GetClient() == clientKey {
			s.mcpClients[i] = st
			return
		}
	}
}

// McpBusy reports whether an install is in flight (drives the spinner tick).
func (s *SettingsForm) McpBusy() bool { return s.mcpInstalling != "" }

// TickMcpSpinner advances the install spinner one frame.
func (s *SettingsForm) TickMcpSpinner() {
	s.mcpSpinnerFrame = (s.mcpSpinnerFrame + 1) % len(spinnerFrames)
}

// McpStatusFor returns the cached status for a client key, or nil when the
// daemon didn't report it (or nothing has been fetched yet).
func (s *SettingsForm) McpStatusFor(clientKey string) *pb.McpClientStatus {
	for _, c := range s.mcpClients {
		if c != nil && c.GetClient() == clientKey {
			return c
		}
	}
	return nil
}

// RevealMcpDetail shows a client's status message inline without installing.
// Used for a harness that is absent (its message carries the manual snippet)
// or already configured (nothing to do).
func (s *SettingsForm) RevealMcpDetail(clientKey string) {
	st := s.McpStatusFor(clientKey)
	if st == nil {
		return
	}
	if s.mcpDetail == nil {
		s.mcpDetail = map[string]string{}
	}
	if _, shown := s.mcpDetail[clientKey]; shown {
		delete(s.mcpDetail, clientKey)
		return
	}
	s.mcpDetail[clientKey] = st.GetMessage()
}

// mcpEnterAction is what Enter on the focused MCP row should do. Keeping the
// decision in the form (rather than the key handler) means it is driven purely
// by daemon-reported state and is unit-testable without a gRPC connection.
type mcpEnterAction int

const (
	// mcpActionNone — nothing actionable (no status for this row yet).
	mcpActionNone mcpEnterAction = iota
	mcpActionToggleCustom
	mcpActionRetryFetch
	mcpActionInstall
	mcpActionReveal
)

// McpEnterAction resolves Enter on the focused row. The second return value is
// the client key the action targets (empty for the Custom row).
func (s *SettingsForm) McpEnterAction() (mcpEnterAction, string) {
	row := s.CurrentRow()
	if row == nil {
		return mcpActionNone, ""
	}
	switch row.Kind {
	case rowKindMcpCustom:
		return mcpActionToggleCustom, ""
	case rowKindMcpClient:
		// A failed fetch leaves nothing to act on — Enter retries it.
		if s.mcpErr != "" {
			return mcpActionRetryFetch, ""
		}
		st := s.McpStatusFor(row.McpClient)
		if st == nil {
			return mcpActionNone, row.McpClient
		}
		// Only a detected-but-unconfigured harness installs. An absent one
		// reveals its message instead — that message carries the manual
		// snippet, which is the whole point of not erroring out.
		if st.GetDetected() && !st.GetConfigured() {
			return mcpActionInstall, row.McpClient
		}
		return mcpActionReveal, row.McpClient
	}
	return mcpActionNone, ""
}

// ToggleMcpCustom expands / collapses the Custom row's snippet block.
func (s *SettingsForm) ToggleMcpCustom() { s.mcpShowCustom = !s.mcpShowCustom }

// McpCustomSnippet exposes the generic snippet exactly as the daemon
// returned it.
func (s *SettingsForm) McpCustomSnippet() string { return s.mcpCustomSnippet }

// rebuildPreservingCursor rebuilds the row list while keeping the cursor on
// the same logical row. Needed because the MCP client rows appear only after
// the first fetch lands, which shifts every row index below them.
func (s *SettingsForm) rebuildPreservingCursor() {
	key := ""
	if r := s.CurrentRow(); r != nil {
		key = r.Key
	}
	s.rebuildRows()
	if key != "" {
		for i := range s.rows {
			if s.rows[i].Key == key {
				s.cursor = i
				break
			}
		}
	}
	s.snapCursor()
}

// ── Helpers ──────────────────────────────────────────────────────

func sectionLabel(id settingsSectionID) string {
	for _, def := range settingsSections {
		if def.ID == id {
			return def.Label
		}
	}
	return ""
}

func sandboxCycleOptions() []CycleOption {
	return []CycleOption{
		{Value: "auto", Display: "Auto"},
		{Value: "sandbox-exec", Display: "sandbox-exec (macOS)"},
		{Value: "off", Display: "Off"},
	}
}

func sandboxCycleIndex(v string) int {
	for i, o := range sandboxCycleOptions() {
		if o.Value == v {
			return i
		}
	}
	return 0
}

func statusCycleOptions() []CycleOption {
	return []CycleOption{
		{Value: "active", Display: "● Active"},
		{Value: "archived", Display: "○ Archived"},
	}
}

func statusCycleIndex(v string) int {
	if v == "archived" {
		return 1
	}
	return 0
}

// buildAgentCycleOptions returns the ordered cycle options for the Agent
// field, derived from the backend registry.
func buildAgentCycleOptions() []CycleOption {
	backends := backend.List()
	if len(backends) == 0 {
		return []CycleOption{{Value: "claude-code", Display: "Claude Code"}}
	}
	settings, _ := config.LoadSettings()
	opts := make([]CycleOption, 0, len(backends))
	for _, b := range backends {
		display := b.DisplayName()
		if _, err := b.ResolveExecutable(settings); err != nil {
			display = display + " (not installed)"
		}
		opts = append(opts, CycleOption{Value: b.Name(), Display: display})
	}
	return opts
}

// agentCycleIndex finds the index of the given agent name in opts. If not
// found (e.g. existing project with unset or unknown agent), it returns
// the index of "claude-code" when present, else 0.
func agentCycleIndex(opts []CycleOption, name string) int {
	for i, o := range opts {
		if o.Value == name {
			return i
		}
	}
	for i, o := range opts {
		if o.Value == "claude-code" {
			return i
		}
	}
	return 0
}

// resolveDefaultBranch reads HEAD via git to surface the project's default
// branch in the Metadata section. Returns "(unknown)" when git fails.
func resolveDefaultBranch(projectPath string) string {
	headPath := filepath.Join(projectPath, ".git", "HEAD")
	data, err := os.ReadFile(headPath)
	if err != nil {
		return "(unknown)"
	}
	line := strings.TrimSpace(string(data))
	const prefix = "ref: refs/heads/"
	if strings.HasPrefix(line, prefix) {
		return strings.TrimPrefix(line, prefix)
	}
	if len(line) >= 7 {
		return line[:7] // detached HEAD: short SHA
	}
	return line
}

// secretsFileSummary returns a human-readable "size · mtime" string for
// the secrets file. Falls back to "(missing)" when the file doesn't exist.
func secretsFileSummary(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return "(missing)"
	}
	return fmt.Sprintf("%s · %s", humanBytes(info.Size()), formatTime(info.ModTime()))
}

func humanBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024.0)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024.0*1024.0))
	}
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Local().Format("2006-01-02 15:04")
}

var timeOfDayRegex = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

func isValidColor(color string) bool {
	match, _ := regexp.MatchString(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`, color)
	return match
}

// ── View ──────────────────────────────────────────────────────────

// View renders the sidebar + fields layout, optionally with the search
// overlay header on top.
func (s *SettingsForm) View() string {
	if s.project == nil || len(s.rows) == 0 {
		return lipgloss.NewStyle().Foreground(colorDim).Render("Loading settings...")
	}

	var header []string
	if s.searchOpen {
		header = append(header, s.renderSearchHeader())
	}

	left := s.renderSidebar()
	right := s.renderFieldsPane()
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", right)

	header = append(header, body)
	header = append(header, "")
	header = append(header, s.renderHint())
	return strings.Join(header, "\n")
}

func (s *SettingsForm) renderHint() string {
	switch {
	case s.editing:
		return lipgloss.NewStyle().Foreground(colorDim).Render("Enter  apply   Esc  cancel")
	case s.searchOpen:
		return lipgloss.NewStyle().Foreground(colorDim).Render("type to filter   ↑/↓  navigate   Enter  jump   Esc  close search")
	}
	hint := "j/k  navigate   Tab  switch pane   /  search   Space  toggle   Enter  edit/action"
	switch s.activeSec {
	case sectionMetadata:
		hint += "   y  copy"
	case sectionSecrets:
		hint += "   e  $EDITOR"
	case sectionMcp:
		hint = "j/k  navigate   Tab  switch pane   /  search   Enter  install / show snippet"
	}
	return lipgloss.NewStyle().Foreground(colorDim).Render(hint)
}

func (s *SettingsForm) renderSearchHeader() string {
	in := s.searchInput.View()
	results := s.searchResults()
	lines := []string{lipgloss.NewStyle().Foreground(colorCyan).Render("/ ") + in}
	if s.searchInput.Value() == "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(colorDim).Render("  type to search settings"))
		return strings.Join(lines, "\n")
	}
	if len(results) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(colorDim).Render("  no results"))
		return strings.Join(lines, "\n")
	}
	max := len(results)
	if max > 6 {
		max = 6
	}
	for i := 0; i < max; i++ {
		r := results[i]
		breadcrumb := lipgloss.NewStyle().Foreground(colorDim).Render(sectionLabel(r.Section) + " · ")
		label := settingsValueStyle.Render(r.Label)
		row := "  " + breadcrumb + label
		if i == s.searchCursor {
			row = settingsCursorStyle.Render(row)
		}
		lines = append(lines, row)
	}
	if len(results) > max {
		lines = append(lines, lipgloss.NewStyle().Foreground(colorDim).Render(fmt.Sprintf("  +%d more", len(results)-max)))
	}
	return strings.Join(lines, "\n")
}

func (s *SettingsForm) renderSidebar() string {
	width := 18
	rows := make([]string, 0, len(settingsSections)+1)
	header := lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Render("Sections")
	rows = append(rows, lipgloss.NewStyle().Width(width).Render(header))
	for i, def := range settingsSections {
		marker := "  "
		if i == s.sidebarCursor {
			marker = "▸ "
		}
		line := marker + def.Label
		paneActive := s.pane == settingsPaneSidebar && !s.searchOpen
		switch {
		case i == s.sidebarCursor && paneActive:
			line = settingsCursorStyle.Width(width).Render(line)
		case i == s.sidebarCursor:
			line = lipgloss.NewStyle().Bold(true).Render(line)
		}
		rows = append(rows, lipgloss.NewStyle().Width(width).Render(line))
	}
	return strings.Join(rows, "\n")
}

func (s *SettingsForm) renderFieldsPane() string {
	def := settingsSections[s.sidebarCursor]
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(colorCyan)
	lines := []string{headerStyle.Render(def.Label)}

	// The MCP section renders badges + inline instruction blocks rather than
	// label/value pairs, so it owns its own row renderer.
	if def.ID == sectionMcp {
		lines = append(lines, s.renderMcpRows()...)
		return strings.Join(lines, "\n")
	}

	rows := s.rowsInSection(def.ID)
	if len(rows) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(colorDim).Render("(no fields)"))
		return strings.Join(lines, "\n")
	}

	// Compute label column width for alignment.
	maxLabel := 0
	for _, idx := range rows {
		l := len(s.rows[idx].Label) + 1
		if l > maxLabel {
			maxLabel = l
		}
	}
	labelStyle := settingsLabelStyle.Width(maxLabel + 1)

	for _, idx := range rows {
		f := &s.rows[idx]
		label := labelStyle.Render(f.Label + ":")

		var val string
		switch f.Type {
		case fieldToggle:
			mark := settingsToggleOff.Render("[OFF]")
			if f.BoolValue {
				mark = settingsToggleOn.Render("[ON]")
			}
			val = mark
		case fieldCycle:
			display := ""
			if len(f.CycleOptions) > 0 && f.CycleIndex >= 0 && f.CycleIndex < len(f.CycleOptions) {
				display = f.CycleOptions[f.CycleIndex].Display
			}
			val = settingsValueStyle.Render(display)
		case fieldText:
			if s.editing && idx == s.cursor {
				val = s.input.View()
			} else if f.Value == "" {
				val = lipgloss.NewStyle().Foreground(colorDim).Render("(inherit)")
			} else {
				val = settingsValueStyle.Render(f.Value)
			}
		case fieldReadOnly:
			if f.Value == "" {
				val = lipgloss.NewStyle().Foreground(colorDim).Render("—")
			} else {
				val = settingsValueStyle.Render(f.Value)
			}
		case fieldAction:
			if f.Value != "" {
				val = settingsValueStyle.Render(f.Value)
			} else {
				val = lipgloss.NewStyle().Foreground(colorYellow).Render("⏎ run")
			}
		}

		line := label + " " + val
		if f.Disabled {
			line = lipgloss.NewStyle().Foreground(colorDim).Render(line)
		}
		paneActive := s.pane == settingsPaneFields && !s.searchOpen
		if idx == s.cursor && paneActive {
			line = settingsCursorStyle.Render(line)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// mcpContentWidth is the usable width of the fields pane for MCP copy. The
// sidebar takes 18 columns plus a 2-column gutter; the rest wraps.
func (s *SettingsForm) mcpContentWidth() int {
	w := s.width - 24
	if w < 40 {
		w = 40
	}
	return w
}

// renderMcpRows renders the MCP onboarding section: a one-line explainer, one
// row per known harness with detected / configured badges + config path, and a
// trailing Custom row that reveals the generic snippet. Everything displayed
// here comes from the daemon's GetMcpClientStatus / InstallMcpClient responses.
func (s *SettingsForm) renderMcpRows() []string {
	dim := lipgloss.NewStyle().Foreground(colorDim)
	width := s.mcpContentWidth()

	out := []string{
		dim.Width(width).Render("Expose Watchfire to coding agents as a local MCP server (stdio, host-only)."),
		"",
	}

	switch {
	case s.mcpErr != "":
		out = append(out,
			lipgloss.NewStyle().Foreground(colorRed).Width(width).Render("✗ "+s.mcpErr),
			dim.Render("  Enter retries."),
			"",
		)
	case s.mcpLoading && !s.mcpFetched:
		out = append(out, dim.Render("Loading MCP client status…"), "")
	}

	rows := s.rowsInSection(sectionMcp)
	maxLabel := 0
	for _, idx := range rows {
		if l := len(s.rows[idx].Label); l > maxLabel {
			maxLabel = l
		}
	}
	labelStyle := settingsValueStyle.Width(maxLabel + 2)
	paneActive := s.pane == settingsPaneFields && !s.searchOpen
	// Space left for the config path after the label and badge columns.
	pathWidth := width - (maxLabel + 3) - mcpBadgeWidth

	for _, idx := range rows {
		f := &s.rows[idx]
		line := labelStyle.Render(f.Label) + " " + s.mcpRowValue(f, pathWidth)
		if idx == s.cursor && paneActive {
			line = settingsCursorStyle.Render(line)
		}
		out = append(out, line)
		out = append(out, s.mcpRowDetail(f)...)
	}
	return out
}

// mcpBadgeWidth is the fixed width of the detected / configured badge column,
// so config paths line up across rows.
const mcpBadgeWidth = 15

// mcpRowValue renders the badge column for one MCP row: the install spinner
// while an RPC is in flight, otherwise the harness's detected / configured
// state plus its config path, elided to pathWidth columns.
func (s *SettingsForm) mcpRowValue(f *SettingsField, pathWidth int) string {
	dim := lipgloss.NewStyle().Foreground(colorDim)
	if f.Kind == rowKindMcpCustom {
		if s.mcpShowCustom {
			return dim.Render("⏎ hide snippet")
		}
		return lipgloss.NewStyle().Foreground(colorYellow).Render("⏎ show snippet")
	}

	if s.mcpInstalling == f.McpClient {
		frame := spinnerFrames[s.mcpSpinnerFrame%len(spinnerFrames)]
		return lipgloss.NewStyle().Foreground(colorYellow).Render(frame + " installing…")
	}

	st := s.McpStatusFor(f.McpClient)
	if st == nil {
		return dim.Render("—")
	}

	var badge string
	switch {
	case st.GetConfigured():
		badge = lipgloss.NewStyle().Foreground(colorGreen).Bold(true).Width(mcpBadgeWidth).Render("✓ configured")
	case st.GetDetected():
		badge = lipgloss.NewStyle().Foreground(colorCyan).Width(mcpBadgeWidth).Render("detected")
	default:
		badge = dim.Width(mcpBadgeWidth).Render("not detected")
	}
	if path := st.GetConfigPath(); path != "" {
		return badge + dim.Render(elideLeft(path, pathWidth))
	}
	return badge
}

// elideLeft trims a path from the left, keeping the informative tail, so a long
// config path can't push the MCP rows past the pane width. Returns the path
// unchanged when it already fits (or when there is no sane budget to fit into).
func elideLeft(path string, width int) string {
	if width < 8 || len([]rune(path)) <= width {
		return path
	}
	runes := []rune(path)
	return "…" + string(runes[len(runes)-(width-1):])
}

// mcpRowDetail renders the inline block revealed by Enter: the install outcome
// or manual instructions for a harness row, the generic snippet for the Custom
// row. Returns nil when nothing is revealed.
func (s *SettingsForm) mcpRowDetail(f *SettingsField) []string {
	if f.Kind == rowKindMcpCustom {
		if !s.mcpShowCustom {
			return nil
		}
		out := mcpDetailBlock(s.mcpCustomSnippet, settingsValueStyle, s.mcpContentWidth())
		return append(out, mcpDetailBlock(
			"Add this to any other MCP client. `watchfire mcp install --print` prints it from the CLI.",
			lipgloss.NewStyle().Foreground(colorDim), s.mcpContentWidth())...)
	}
	msg, shown := s.mcpDetail[f.McpClient]
	if !shown || msg == "" {
		return nil
	}
	return mcpDetailBlock(msg, lipgloss.NewStyle().Foreground(colorDim), s.mcpContentWidth())
}

// mcpDetailBlock indents a possibly multi-line daemon message under its row.
// Line structure is preserved as the daemon sent it — for a failed install the
// message is the manual instructions plus the snippet to paste, so the JSON
// keeps its own line breaks. The indent is left padding rather than a literal
// prefix so wrapped continuation lines stay aligned too.
func mcpDetailBlock(text string, style lipgloss.Style, width int) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	indented := style.Width(width).PaddingLeft(4)
	raw := strings.Split(strings.TrimRight(text, "\n"), "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		out = append(out, indented.Render(line))
	}
	return out
}
