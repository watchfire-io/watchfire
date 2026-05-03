package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/agent/backend"
	"github.com/watchfire-io/watchfire/internal/models"
	pb "github.com/watchfire-io/watchfire/proto"
)

// validateExecutablePath mirrors the daemon-side check
// (`server/settings_service.go:validateExecutablePath`) so the TUI can give
// the user immediate feedback before sending an UpdateSettings RPC. The
// daemon repeats the check on save — this client-side copy is a UX
// optimisation, not the source of truth.
func validateExecutablePath(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("path must be absolute")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("is a directory")
	}
	if info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("not executable")
	}
	return nil
}

// askPerProjectValue is the sentinel stored in Settings.Defaults.DefaultAgent
// to mean "prompt at init time". It must be empty so it round-trips
// through the proto string field cleanly.
const askPerProjectValue = ""

// notifyRow identifies a single editable notification preference row.
// The row layout is fixed (no list), so we use a small enum.
type notifyRow int

const (
	notifyRowEnabled notifyRow = iota
	notifyRowEventTaskFailed
	notifyRowEventRunComplete
	notifyRowEventWeeklyDigest // v6.0 Ember
	notifyRowDigestSchedule    // v6.0 Ember — cycle through preset cadences
	notifyRowSoundsEnabled
	notifyRowSoundTaskFailed
	notifyRowSoundRunComplete
	notifyRowVolume
	notifyRowQuietEnabled
	notifyRowQuietStart
	notifyRowQuietEnd
	notifyRowCount // sentinel = number of rows
)

// notifyState mirrors models.NotificationsConfig in a flat editable shape.
// Kept in the form so the View() can render and the dispatch layer can roll
// the whole thing up into a NotificationsConfig proto on save.
type notifyState struct {
	Enabled           bool
	EventTaskFailed   bool
	EventRunComplete  bool
	EventWeeklyDigest bool   // v6.0 Ember
	DigestSchedule    string // v6.0 Ember — cron-ish "MON 09:00" / "DAILY 17:00"
	SoundsEnabled     bool
	SoundTaskFailed   bool
	SoundRunComplete  bool
	Volume            int // 0..100, displayed as N% and persisted as float32 N/100
	QuietEnabled      bool
	QuietStart        string
	QuietEnd          string
}

// digestSchedulePresets are the cron-ish strings the TUI cycles through on
// the digest-schedule row. Mirrors the GUI's SCHEDULE_PRESETS.
var digestSchedulePresets = []string{"MON 09:00", "MON 18:00", "FRI 17:00", "DAILY 09:00"}

// nextDigestSchedule cycles to the next preset after the current value.
// Unknown values reset to the first preset.
func nextDigestSchedule(current string) string {
	for i, p := range digestSchedulePresets {
		if p == current {
			return digestSchedulePresets[(i+1)%len(digestSchedulePresets)]
		}
	}
	return digestSchedulePresets[0]
}

// settingsCategoryID enumerates the eight macOS-style categories the global
// settings overlay surfaces in its left pane. Mirrors the GUI's
// SETTINGS_CATEGORIES list — keep them in sync.
type settingsCategoryID int

const (
	catAppearance settingsCategoryID = iota
	catDefaults
	catAgentPaths
	catNotifications
	catIntegrations
	catInbound
	catUpdates
	catAbout
	catCount
)

type settingsCategoryDef struct {
	ID    settingsCategoryID
	Slug  string // matches the GUI hash slug (#defaults / #agent-paths / ...)
	Label string
	// Stub is shown in the right pane when this category has no
	// TUI-editable rows (Appearance / Integrations / Inbound / Updates /
	// About). Empty for categories that own real rows.
	Stub string
}

// settingsCategories is the canonical, ordered list. The order also drives
// the cursor in the left pane.
var settingsCategories = []settingsCategoryDef{
	{catAppearance, "appearance", "Appearance", "Theme is configured in the GUI."},
	{catDefaults, "defaults", "Defaults", ""},
	{catAgentPaths, "agent-paths", "Agent Paths", ""},
	{catNotifications, "notifications", "Notifications", ""},
	{catIntegrations, "integrations", "Integrations", "Press Ctrl+I to manage outbound integrations."},
	{catInbound, "inbound", "Inbound", "Inbound (Echo) settings are configured in the GUI."},
	{catUpdates, "updates", "Updates", "Update preferences are configured in the GUI."},
	{catAbout, "about", "About", "Watchfire global settings — see the dashboard for version."},
}

// settingsPane identifies which side of the two-pane layout has focus.
type settingsPane int

const (
	paneCategories settingsPane = iota // left
	paneFields                         // right
)

// settingsSearchEntry is a single hit in the search index. Mirrors the GUI's
// SettingsSearchEntry. We build the index dynamically from the live row list
// so adding a row only requires touching one place (ToggleNotify et al).
type settingsSearchEntry struct {
	Category settingsCategoryID
	FieldID  string
	Label    string
	Keywords []string
	// rowIndex points back into the global row list; -1 for entries whose
	// category has no editable rows (the search jumps to the category but
	// can't position a field cursor).
	rowIndex int
}

// GlobalSettingsForm is the overlay used to edit ~/.watchfire/settings.yaml.
// It now uses a two-pane layout (categories | fields) plus a search overlay
// (`/`) that mirrors macOS System Settings. The actual editable state
// (agentRows, notify, ...) is unchanged — only navigation is reorganised.
type GlobalSettingsForm struct {
	// agentRows has one entry per backend in backend.List() order.
	agentRows []agentPathRow
	// defaultOptions is [Ask per project, backend1, backend2, ...].
	defaultOptions []CycleOption
	defaultIndex   int

	// terminalShell is the absolute path to the shell binary the GUI's
	// in-app terminal should spawn (issue #32). Empty = use $SHELL with
	// login-shell autodetection. Editable via the form, persisted in
	// settings.yaml under defaults.terminal_shell.
	terminalShell string

	notify notifyState

	// cursor is the row index in the global row table (the same indexing
	// used by Toggle / StartEdit / FinishEdit). Always points at a row in
	// the currently selected category.
	cursor  int
	editing bool
	input   textinput.Model
	width   int

	loaded bool

	// macOS-style two-pane navigation.
	pane             settingsPane
	selectedCategory settingsCategoryID
	categoryCursor   int // index into the *visible* (non-empty) category list

	// Search overlay state. searchOpen=true takes precedence over pane
	// navigation: typing edits the query and Enter jumps to the selected
	// entry's category + row.
	searchOpen   bool
	searchInput  textinput.Model
	searchCursor int
}

type agentPathRow struct {
	Name        string // backend name (e.g. "claude-code")
	DisplayName string
	Path        string // empty = "auto"
	Available   bool   // binary resolves on this host; display-time hint only
}

// NewGlobalSettingsForm builds the form with rows derived from the
// backend registry. Settings values are loaded separately via Load.
func NewGlobalSettingsForm() *GlobalSettingsForm {
	ti := textinput.New()
	ti.CharLimit = 500

	si := textinput.New()
	si.CharLimit = 80
	si.Placeholder = "Search settings"

	backends := backend.List()
	settings, _ := config.LoadSettings()
	rows := make([]agentPathRow, 0, len(backends))
	for _, b := range backends {
		_, err := b.ResolveExecutable(settings)
		rows = append(rows, agentPathRow{
			Name:        b.Name(),
			DisplayName: b.DisplayName(),
			Available:   err == nil,
		})
	}

	opts := make([]CycleOption, 0, len(backends)+1)
	opts = append(opts, CycleOption{Value: askPerProjectValue, Display: "Ask per project"})
	for _, b := range backends {
		display := b.DisplayName()
		if _, err := b.ResolveExecutable(settings); err != nil {
			display = display + " (not installed)"
		}
		opts = append(opts, CycleOption{Value: b.Name(), Display: display})
	}

	g := &GlobalSettingsForm{
		agentRows:        rows,
		defaultOptions:   opts,
		defaultIndex:     0,
		input:            ti,
		searchInput:      si,
		selectedCategory: catDefaults,
	}
	g.categoryCursor = g.categoryListCursor(g.selectedCategory)
	// Land the cursor on the first row of the initial category so the
	// right pane reads coherently before any user input.
	g.alignCursorToCategory()
	return g
}

// Load populates the form from a fetched Settings message. Agents not
// registered as backends are ignored (the server rejects them on save).
func (g *GlobalSettingsForm) Load(s *pb.Settings) {
	g.loaded = true
	for i := range g.agentRows {
		g.agentRows[i].Path = ""
	}
	if s != nil {
		for name, cfg := range s.Agents {
			for i := range g.agentRows {
				if g.agentRows[i].Name == name {
					if cfg != nil {
						g.agentRows[i].Path = cfg.Path
					}
					break
				}
			}
		}
	}
	g.defaultIndex = 0
	g.terminalShell = ""
	if s != nil && s.Defaults != nil {
		for i, o := range g.defaultOptions {
			if o.Value == s.Defaults.DefaultAgent {
				g.defaultIndex = i
				break
			}
		}
		g.terminalShell = s.Defaults.TerminalShell
	}

	// Notifications — fall back to defaults when the daemon hasn't sent the
	// block (older settings.yaml that pre-dates this section).
	g.notify = defaultNotifyState()
	if s != nil && s.Defaults != nil && s.Defaults.Notifications != nil {
		n := s.Defaults.Notifications
		g.notify.Enabled = n.Enabled
		if n.Events != nil {
			g.notify.EventTaskFailed = n.Events.TaskFailed
			g.notify.EventRunComplete = n.Events.RunComplete
			g.notify.EventWeeklyDigest = n.Events.WeeklyDigest
		}
		if n.DigestSchedule != "" {
			g.notify.DigestSchedule = n.DigestSchedule
		}
		if n.Sounds != nil {
			g.notify.SoundsEnabled = n.Sounds.Enabled
			g.notify.SoundTaskFailed = n.Sounds.TaskFailed
			g.notify.SoundRunComplete = n.Sounds.RunComplete
			g.notify.Volume = volumeToPercent(n.Sounds.Volume)
		}
		if n.QuietHours != nil {
			g.notify.QuietEnabled = n.QuietHours.Enabled
			if n.QuietHours.Start != "" {
				g.notify.QuietStart = n.QuietHours.Start
			}
			if n.QuietHours.End != "" {
				g.notify.QuietEnd = n.QuietHours.End
			}
		}
	}
}

func defaultNotifyState() notifyState {
	return notifyState{
		Enabled:           true,
		EventTaskFailed:   true,
		EventRunComplete:  true,
		EventWeeklyDigest: false,
		DigestSchedule:    "MON 09:00",
		SoundsEnabled:     true,
		SoundTaskFailed:   true,
		SoundRunComplete:  true,
		Volume:            60,
		QuietEnabled:      false,
		QuietStart:        "22:00",
		QuietEnd:          "08:00",
	}
}

func volumeToPercent(v float64) int {
	pct := int(v*100 + 0.5)
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
}

// SetWidth sets the overlay's usable width so the text input can size.
func (g *GlobalSettingsForm) SetWidth(w int) {
	g.width = w
	g.input.Width = w - 24
	if g.input.Width < 10 {
		g.input.Width = 10
	}
	g.searchInput.Width = w - 16
	if g.searchInput.Width < 10 {
		g.searchInput.Width = 10
	}
}

// Reset returns the form to its pre-open state: not editing, cursor
// at top, on the categories pane, search closed. Called on close so the
// next open starts clean.
func (g *GlobalSettingsForm) Reset() {
	g.editing = false
	g.input.Blur()
	g.searchOpen = false
	g.searchInput.Blur()
	g.searchInput.SetValue("")
	g.searchCursor = 0
	g.pane = paneCategories
	g.selectedCategory = catDefaults
	g.categoryCursor = g.categoryListCursor(g.selectedCategory)
	g.alignCursorToCategory()
}

// rowsForCategory returns the global row indices that belong to a category,
// in display order. Empty for stub categories (Appearance / Integrations /
// Inbound / Updates / About).
func (g *GlobalSettingsForm) rowsForCategory(c settingsCategoryID) []int {
	switch c {
	case catAgentPaths:
		out := make([]int, len(g.agentRows))
		for i := range out {
			out[i] = i
		}
		return out
	case catDefaults:
		return []int{g.defaultCursor(), g.terminalCursor()}
	case catNotifications:
		out := make([]int, int(notifyRowCount))
		base := g.notifyCursorBase()
		for i := 0; i < int(notifyRowCount); i++ {
			out[i] = base + i
		}
		return out
	}
	return nil
}

// categoryForRow returns the category that owns a given global row index.
// Falls back to catDefaults for out-of-range values rather than panicking;
// the caller should never see that path under normal navigation.
func (g *GlobalSettingsForm) categoryForRow(row int) settingsCategoryID {
	if row >= 0 && row < len(g.agentRows) {
		return catAgentPaths
	}
	if row == g.defaultCursor() || row == g.terminalCursor() {
		return catDefaults
	}
	if row >= g.notifyCursorBase() && row < g.notifyCursorBase()+int(notifyRowCount) {
		return catNotifications
	}
	return catDefaults
}

// alignCursorToCategory moves g.cursor onto the first row of the currently
// selected category. No-op if the cursor is already in-bounds for that
// category.
func (g *GlobalSettingsForm) alignCursorToCategory() {
	rows := g.rowsForCategory(g.selectedCategory)
	if len(rows) == 0 {
		return
	}
	for _, r := range rows {
		if r == g.cursor {
			return
		}
	}
	g.cursor = rows[0]
}

// categoryListCursor returns the index of `c` in the visible category list
// (which is just the canonical settingsCategories list — every category is
// surfaced, including stubs).
func (g *GlobalSettingsForm) categoryListCursor(c settingsCategoryID) int {
	for i, def := range settingsCategories {
		if def.ID == c {
			return i
		}
	}
	return 0
}

func (g *GlobalSettingsForm) rowCount() int {
	return len(g.agentRows) + 2 + int(notifyRowCount)
}

// defaultCursor is the index of the global-default row.
func (g *GlobalSettingsForm) defaultCursor() int { return len(g.agentRows) }

// terminalCursor is the index of the terminal-shell row.
func (g *GlobalSettingsForm) terminalCursor() int { return len(g.agentRows) + 1 }

// notifyCursorBase is the index of the first notification row.
func (g *GlobalSettingsForm) notifyCursorBase() int { return len(g.agentRows) + 2 }

// notifyRowAtCursor returns which notification row the cursor is on, or -1
// if the cursor is not on a notification row.
func (g *GlobalSettingsForm) notifyRowAtCursor() notifyRow {
	idx := g.cursor - g.notifyCursorBase()
	if idx < 0 || idx >= int(notifyRowCount) {
		return -1
	}
	return notifyRow(idx)
}

// IsSearching reports whether the search overlay has focus. Used by the key
// handler to decide whether typing edits the query or drives field nav.
func (g *GlobalSettingsForm) IsSearching() bool { return g.searchOpen }

// ActivePane reports which pane has the navigation cursor.
func (g *GlobalSettingsForm) ActivePane() settingsPane { return g.pane }

// SelectedCategory exposes the active category for tests and the key handler.
func (g *GlobalSettingsForm) SelectedCategory() settingsCategoryID { return g.selectedCategory }

// Cursor exposes the active row cursor for tests.
func (g *GlobalSettingsForm) Cursor() int { return g.cursor }

// SwitchPane toggles between the categories list and the fields list. No-op
// while editing or while the search overlay is open.
func (g *GlobalSettingsForm) SwitchPane() {
	if g.editing || g.searchOpen {
		return
	}
	if g.pane == paneCategories {
		// Only switch to fields if the category has any.
		if len(g.rowsForCategory(g.selectedCategory)) == 0 {
			return
		}
		g.pane = paneFields
		g.alignCursorToCategory()
	} else {
		g.pane = paneCategories
	}
}

// MoveUp/MoveDown move the selection cursor while not editing. The
// behaviour depends on the active pane: in the categories pane we move
// through the category list; in the fields pane we move through the
// current category's rows. Search-overlay navigation is handled separately
// (MoveSearchUp / MoveSearchDown).
func (g *GlobalSettingsForm) MoveUp() {
	if g.editing {
		return
	}
	if g.searchOpen {
		g.MoveSearchUp()
		return
	}
	switch g.pane {
	case paneCategories:
		if g.categoryCursor > 0 {
			g.categoryCursor--
			g.selectedCategory = settingsCategories[g.categoryCursor].ID
			g.alignCursorToCategory()
		}
	case paneFields:
		rows := g.rowsForCategory(g.selectedCategory)
		for i, r := range rows {
			if r == g.cursor && i > 0 {
				g.cursor = rows[i-1]
				return
			}
		}
	}
}

func (g *GlobalSettingsForm) MoveDown() {
	if g.editing {
		return
	}
	if g.searchOpen {
		g.MoveSearchDown()
		return
	}
	switch g.pane {
	case paneCategories:
		if g.categoryCursor < len(settingsCategories)-1 {
			g.categoryCursor++
			g.selectedCategory = settingsCategories[g.categoryCursor].ID
			g.alignCursorToCategory()
		}
	case paneFields:
		rows := g.rowsForCategory(g.selectedCategory)
		for i, r := range rows {
			if r == g.cursor && i < len(rows)-1 {
				g.cursor = rows[i+1]
				return
			}
		}
	}
}

// IsEditing reports whether the path text input has focus.
func (g *GlobalSettingsForm) IsEditing() bool { return g.editing }

// InputModel exposes the text input for Update forwarding. When the search
// overlay is open the search input is returned instead, so the same key
// dispatch loop drives both inputs without branching.
func (g *GlobalSettingsForm) InputModel() *textinput.Model {
	if g.searchOpen {
		return &g.searchInput
	}
	return &g.input
}

// StartEdit enters edit mode on the currently selected agent path row OR
// on a notification text-editable row (volume / quiet-hours start/end).
// Returns false when the cursor is on a non-editable row (toggles, the
// default selector) or when there are no backends registered. Only acts
// when the fields pane has focus.
func (g *GlobalSettingsForm) StartEdit() bool {
	if g.searchOpen || g.pane != paneFields {
		return false
	}
	if g.cursor >= 0 && g.cursor < len(g.agentRows) {
		g.editing = true
		g.input.SetValue(g.agentRows[g.cursor].Path)
		g.input.Focus()
		return true
	}
	if g.cursor == g.terminalCursor() {
		g.editing = true
		g.input.SetValue(g.terminalShell)
		g.input.Focus()
		return true
	}
	switch g.notifyRowAtCursor() {
	case notifyRowVolume:
		g.editing = true
		g.input.SetValue(strconv.Itoa(g.notify.Volume))
		g.input.Focus()
		return true
	case notifyRowQuietStart:
		g.editing = true
		g.input.SetValue(g.notify.QuietStart)
		g.input.Focus()
		return true
	case notifyRowQuietEnd:
		g.editing = true
		g.input.SetValue(g.notify.QuietEnd)
		g.input.Focus()
		return true
	}
	return false
}

// CancelEdit exits edit mode without applying changes.
func (g *GlobalSettingsForm) CancelEdit() {
	g.editing = false
	g.input.Blur()
}

// EditResult is what FinishEdit returns. Callers branch on Kind to know which
// RPC to fire. AgentName/Path apply only to EditAgentPath; NotifyChanged
// applies to any notify-row edit and signals the caller to push the whole
// notifications block; TerminalShell carries the new shell path on
// EditTerminalShell.
type EditResult struct {
	Kind          EditKind
	AgentName     string
	Path          string
	NotifyChanged bool
	TerminalShell string
	Err           string // non-empty when the edit was rejected (e.g. malformed HH:MM)
}

// EditKind identifies which part of the form an edit changed.
type EditKind int

const (
	EditNone EditKind = iota
	EditAgentPath
	EditNotify
	EditTerminalShell
)

// FinishEdit applies the edit. The returned EditResult tells the caller what
// (if anything) needs to be pushed up to the daemon. Malformed quiet-hours
// values are rejected with EditNone + Err so the caller can flash a status.
func (g *GlobalSettingsForm) FinishEdit() EditResult {
	if !g.editing {
		return EditResult{}
	}
	g.editing = false
	g.input.Blur()

	if g.cursor >= 0 && g.cursor < len(g.agentRows) {
		row := &g.agentRows[g.cursor]
		newPath := strings.TrimSpace(g.input.Value())
		if newPath == row.Path {
			return EditResult{}
		}
		row.Path = newPath
		return EditResult{Kind: EditAgentPath, AgentName: row.Name, Path: newPath}
	}

	if g.cursor == g.terminalCursor() {
		newShell := strings.TrimSpace(g.input.Value())
		if newShell == g.terminalShell {
			return EditResult{}
		}
		// Validate locally before sending — a bad path produces a clear
		// status-bar message instead of a daemon-side error round-trip. We
		// still let the daemon do the authoritative X_OK check on save so
		// the contract matches the GUI.
		if newShell != "" {
			if err := validateExecutablePath(newShell); err != nil {
				return EditResult{Err: fmt.Sprintf("invalid terminal shell %q: %v", newShell, err)}
			}
		}
		g.terminalShell = newShell
		return EditResult{Kind: EditTerminalShell, TerminalShell: newShell}
	}

	switch g.notifyRowAtCursor() {
	case notifyRowVolume:
		raw := strings.TrimSpace(g.input.Value())
		raw = strings.TrimSuffix(raw, "%")
		v, err := strconv.Atoi(raw)
		if err != nil {
			return EditResult{Err: fmt.Sprintf("invalid volume %q (expected 0-100)", raw)}
		}
		if v < 0 {
			v = 0
		}
		if v > 100 {
			v = 100
		}
		if v == g.notify.Volume {
			return EditResult{}
		}
		g.notify.Volume = v
		return EditResult{Kind: EditNotify, NotifyChanged: true}
	case notifyRowQuietStart:
		newVal := strings.TrimSpace(g.input.Value())
		if !models.IsValidTimeOfDay(newVal) {
			return EditResult{Err: fmt.Sprintf("invalid time %q (expected HH:MM)", newVal)}
		}
		if newVal == g.notify.QuietStart {
			return EditResult{}
		}
		g.notify.QuietStart = newVal
		return EditResult{Kind: EditNotify, NotifyChanged: true}
	case notifyRowQuietEnd:
		newVal := strings.TrimSpace(g.input.Value())
		if !models.IsValidTimeOfDay(newVal) {
			return EditResult{Err: fmt.Sprintf("invalid time %q (expected HH:MM)", newVal)}
		}
		if newVal == g.notify.QuietEnd {
			return EditResult{}
		}
		g.notify.QuietEnd = newVal
		return EditResult{Kind: EditNotify, NotifyChanged: true}
	}
	return EditResult{}
}

// CycleDefault advances the global-default selector. Returns (changed,
// newValue) — newValue is "" for "Ask per project", otherwise the
// backend name. Only effective when the cursor is on the default row and
// the fields pane has focus.
func (g *GlobalSettingsForm) CycleDefault() (changed bool, newValue string) {
	if g.editing || g.searchOpen || g.pane != paneFields || g.cursor != g.defaultCursor() {
		return false, ""
	}
	if len(g.defaultOptions) == 0 {
		return false, ""
	}
	g.defaultIndex = (g.defaultIndex + 1) % len(g.defaultOptions)
	return true, g.defaultOptions[g.defaultIndex].Value
}

// ToggleNotify flips the boolean notify row under the cursor (master toggle,
// per-event, sound master, per-sound-event, quiet-hours-enabled). Returns
// true when the cursor was on a toggle-able row and the state changed.
func (g *GlobalSettingsForm) ToggleNotify() bool {
	if g.editing || g.searchOpen || g.pane != paneFields {
		return false
	}
	switch g.notifyRowAtCursor() {
	case notifyRowEnabled:
		g.notify.Enabled = !g.notify.Enabled
	case notifyRowEventTaskFailed:
		g.notify.EventTaskFailed = !g.notify.EventTaskFailed
	case notifyRowEventRunComplete:
		g.notify.EventRunComplete = !g.notify.EventRunComplete
	case notifyRowEventWeeklyDigest:
		g.notify.EventWeeklyDigest = !g.notify.EventWeeklyDigest
	case notifyRowDigestSchedule:
		// The digest-schedule row cycles through presets rather than a
		// boolean. Toggle == cycle.
		g.notify.DigestSchedule = nextDigestSchedule(g.notify.DigestSchedule)
	case notifyRowSoundsEnabled:
		g.notify.SoundsEnabled = !g.notify.SoundsEnabled
	case notifyRowSoundTaskFailed:
		g.notify.SoundTaskFailed = !g.notify.SoundTaskFailed
	case notifyRowSoundRunComplete:
		g.notify.SoundRunComplete = !g.notify.SoundRunComplete
	case notifyRowQuietEnabled:
		g.notify.QuietEnabled = !g.notify.QuietEnabled
	default:
		return false
	}
	return true
}

// SelectCategoryByCursor sets the selected category from the current
// categoryCursor, syncing the row cursor onto the category's first row.
// Used by Enter on the categories pane to "drill in" without an explicit
// pane switch.
func (g *GlobalSettingsForm) SelectCategoryByCursor() {
	if g.categoryCursor < 0 || g.categoryCursor >= len(settingsCategories) {
		return
	}
	g.selectedCategory = settingsCategories[g.categoryCursor].ID
	g.alignCursorToCategory()
	if len(g.rowsForCategory(g.selectedCategory)) > 0 {
		g.pane = paneFields
	}
}

// OpenSearch enters search mode. Mirrors the GUI's Cmd/Ctrl+F handler.
func (g *GlobalSettingsForm) OpenSearch() {
	if g.editing {
		return
	}
	g.searchOpen = true
	g.searchCursor = 0
	g.searchInput.SetValue("")
	g.searchInput.Focus()
}

// CloseSearch exits search mode without jumping anywhere.
func (g *GlobalSettingsForm) CloseSearch() {
	g.searchOpen = false
	g.searchInput.Blur()
	g.searchInput.SetValue("")
	g.searchCursor = 0
}

// MoveSearchUp / MoveSearchDown move the cursor in the result list while
// the search overlay is open.
func (g *GlobalSettingsForm) MoveSearchUp() {
	if !g.searchOpen {
		return
	}
	if g.searchCursor > 0 {
		g.searchCursor--
	}
}
func (g *GlobalSettingsForm) MoveSearchDown() {
	if !g.searchOpen {
		return
	}
	results := g.searchResults()
	if g.searchCursor < len(results)-1 {
		g.searchCursor++
	}
}

// ActivateSearch jumps to the highlighted entry's category and field, then
// closes the search overlay. Returns true if a target was activated; false
// when there are no results.
func (g *GlobalSettingsForm) ActivateSearch() bool {
	if !g.searchOpen {
		return false
	}
	results := g.searchResults()
	if len(results) == 0 || g.searchCursor < 0 || g.searchCursor >= len(results) {
		return false
	}
	hit := results[g.searchCursor]
	g.selectedCategory = hit.Category
	g.categoryCursor = g.categoryListCursor(hit.Category)
	if hit.rowIndex >= 0 {
		g.cursor = hit.rowIndex
		g.pane = paneFields
	} else {
		g.pane = paneCategories
	}
	g.CloseSearch()
	return true
}

// NotificationsProto returns a fully-populated NotificationsConfig proto
// that the caller can stuff into an UpdateSettingsRequest.Defaults block.
func (g *GlobalSettingsForm) NotificationsProto() *pb.NotificationsConfig {
	return &pb.NotificationsConfig{
		Enabled: g.notify.Enabled,
		Events: &pb.NotificationsEvents{
			TaskFailed:   g.notify.EventTaskFailed,
			RunComplete:  g.notify.EventRunComplete,
			WeeklyDigest: g.notify.EventWeeklyDigest,
		},
		Sounds: &pb.NotificationsSounds{
			Enabled:     g.notify.SoundsEnabled,
			TaskFailed:  g.notify.SoundTaskFailed,
			RunComplete: g.notify.SoundRunComplete,
			Volume:      float64(g.notify.Volume) / 100.0,
		},
		QuietHours: &pb.QuietHoursConfig{
			Enabled: g.notify.QuietEnabled,
			Start:   g.notify.QuietStart,
			End:     g.notify.QuietEnd,
		},
		DigestSchedule: g.notify.DigestSchedule,
	}
}

// searchIndex builds the full list of entries against the current row layout.
// Mirrors the GUI's SETTINGS_SEARCH_INDEX one-to-one, with rowIndex resolved
// dynamically (agent paths depend on backend.List() at construction time).
func (g *GlobalSettingsForm) searchIndex() []settingsSearchEntry {
	out := make([]settingsSearchEntry, 0, 32)

	// Appearance — category-only stub.
	out = append(out, settingsSearchEntry{
		Category: catAppearance, FieldID: "theme", Label: "Theme",
		Keywords: []string{"light", "dark", "system", "mode"},
		rowIndex: -1,
	})

	// Defaults.
	out = append(out, settingsSearchEntry{
		Category: catDefaults, FieldID: "default-agent", Label: "Default Agent",
		Keywords: []string{"agent", "claude", "codex", "opencode", "gemini", "copilot"},
		rowIndex: g.defaultCursor(),
	})
	out = append(out, settingsSearchEntry{
		Category: catDefaults, FieldID: "terminal-shell", Label: "Terminal shell",
		Keywords: []string{"shell", "bash", "zsh", "fish"},
		rowIndex: g.terminalCursor(),
	})

	// Agent Paths — one entry per backend.
	for i, r := range g.agentRows {
		out = append(out, settingsSearchEntry{
			Category: catAgentPaths,
			FieldID:  "agent-path-" + r.Name,
			Label:    r.DisplayName + " path",
			Keywords: []string{"path", "binary", "executable", r.Name},
			rowIndex: i,
		})
	}

	// Notifications.
	notifEntries := []struct {
		row      notifyRow
		fieldID  string
		label    string
		keywords []string
	}{
		{notifyRowEnabled, "notifications-enabled", "Enable notifications", []string{"notify", "alert", "master"}},
		{notifyRowEventTaskFailed, "notifications-task-failed", "Notify on task failure", []string{"fail", "error", "task"}},
		{notifyRowEventRunComplete, "notifications-run-complete", "Notify on run complete", []string{"done", "finished", "wildfire"}},
		{notifyRowEventWeeklyDigest, "notifications-weekly-digest", "Send weekly digest", []string{"digest", "summary", "weekly"}},
		{notifyRowDigestSchedule, "notifications-digest-schedule", "Digest schedule", []string{"cron", "schedule", "time"}},
		{notifyRowSoundsEnabled, "notifications-sounds", "Play sounds", []string{"sound", "audio"}},
		{notifyRowSoundTaskFailed, "notifications-sound-task-failed", "Sound on task failure", []string{"sound", "fail"}},
		{notifyRowSoundRunComplete, "notifications-sound-run-complete", "Sound on run complete", []string{"sound", "done"}},
		{notifyRowVolume, "notifications-volume", "Volume", []string{"loud", "audio"}},
		{notifyRowQuietEnabled, "notifications-quiet-hours", "Quiet hours", []string{"mute", "do not disturb", "dnd"}},
		{notifyRowQuietStart, "notifications-quiet-start", "Quiet hours start", []string{"start", "time"}},
		{notifyRowQuietEnd, "notifications-quiet-end", "Quiet hours end", []string{"end", "time"}},
	}
	base := g.notifyCursorBase()
	for _, e := range notifEntries {
		out = append(out, settingsSearchEntry{
			Category: catNotifications,
			FieldID:  e.fieldID,
			Label:    e.label,
			Keywords: e.keywords,
			rowIndex: base + int(e.row),
		})
	}

	// Stub categories — searchable so the user can jump there.
	out = append(out, settingsSearchEntry{
		Category: catIntegrations, FieldID: "integrations", Label: "Integrations",
		Keywords: []string{"webhook", "slack", "discord", "github"}, rowIndex: -1,
	})
	out = append(out, settingsSearchEntry{
		Category: catInbound, FieldID: "inbound", Label: "Inbound (Echo)",
		Keywords: []string{"webhook", "echo", "tunnel"}, rowIndex: -1,
	})
	out = append(out, settingsSearchEntry{
		Category: catUpdates, FieldID: "updates", Label: "Updates",
		Keywords: []string{"version", "download", "auto-update"}, rowIndex: -1,
	})
	out = append(out, settingsSearchEntry{
		Category: catAbout, FieldID: "about", Label: "About",
		Keywords: []string{"version", "build"}, rowIndex: -1,
	})
	return out
}

// matchSearchEntries filters the search index to entries whose label or
// keywords contain every whitespace-delimited token from the query
// (case-insensitive). Empty query → empty result, matching the GUI helper.
func matchSearchEntries(query string, index []settingsSearchEntry) []settingsSearchEntry {
	q := strings.TrimSpace(strings.ToLower(query))
	if q == "" {
		return nil
	}
	tokens := strings.Fields(q)
	out := make([]settingsSearchEntry, 0, len(index))
	for _, e := range index {
		hay := strings.ToLower(e.Label + " " + strings.Join(e.Keywords, " "))
		ok := true
		for _, t := range tokens {
			if !strings.Contains(hay, t) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, e)
		}
	}
	return out
}

// searchResults returns the live result set for the current query.
func (g *GlobalSettingsForm) searchResults() []settingsSearchEntry {
	return matchSearchEntries(g.searchInput.Value(), g.searchIndex())
}

// View renders the overlay body (without the outer border — caller
// wraps it with overlayStyle). The layout is two columns: a category
// sidebar on the left, the selected category's fields on the right.
// When the search overlay is open, an additional row above the panes
// shows the input + result list.
func (g *GlobalSettingsForm) View() string {
	title := overlayTitleStyle.Render("Global Settings")
	if !g.loaded {
		return strings.Join([]string{
			title,
			lipgloss.NewStyle().Foreground(colorDim).Render("Loading..."),
		}, "\n")
	}

	var header []string
	header = append(header, title)
	if g.searchOpen {
		header = append(header, g.renderSearchHeader())
	}

	left := g.renderCategoryPane()
	right := g.renderFieldsPane()
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", right)

	header = append(header, body)
	header = append(header, "")
	header = append(header, g.renderHint())
	return strings.Join(header, "\n")
}

func (g *GlobalSettingsForm) renderHint() string {
	switch {
	case g.editing:
		return lipgloss.NewStyle().Foreground(colorDim).Render("Enter  apply   Esc  cancel")
	case g.searchOpen:
		return lipgloss.NewStyle().Foreground(colorDim).Render("type to filter   ↑/↓  navigate   Enter  jump   Esc  close search")
	}
	hint := "j/k  navigate   tab  switch pane   /  search   space  toggle   Enter  edit/cycle   Esc  close"
	return lipgloss.NewStyle().Foreground(colorDim).Render(hint)
}

func (g *GlobalSettingsForm) renderSearchHeader() string {
	in := g.searchInput.View()
	results := g.searchResults()
	lines := []string{
		lipgloss.NewStyle().Foreground(colorCyan).Render("/ ") + in,
	}
	if g.searchInput.Value() == "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(colorDim).
			Render("  type to search settings"))
		return strings.Join(lines, "\n")
	}
	if len(results) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(colorDim).
			Render("  no results"))
		return strings.Join(lines, "\n")
	}
	max := len(results)
	if max > 6 {
		max = 6
	}
	for i := 0; i < max; i++ {
		r := results[i]
		breadcrumb := lipgloss.NewStyle().Foreground(colorDim).
			Render(categoryLabel(r.Category) + " · ")
		label := settingsValueStyle.Render(r.Label)
		row := "  " + breadcrumb + label
		if i == g.searchCursor {
			row = settingsCursorStyle.Render(row)
		}
		lines = append(lines, row)
	}
	if len(results) > max {
		lines = append(lines, lipgloss.NewStyle().Foreground(colorDim).
			Render(fmt.Sprintf("  +%d more", len(results)-max)))
	}
	return strings.Join(lines, "\n")
}

func categoryLabel(c settingsCategoryID) string {
	for _, def := range settingsCategories {
		if def.ID == c {
			return def.Label
		}
	}
	return ""
}

func (g *GlobalSettingsForm) renderCategoryPane() string {
	width := 18
	rows := make([]string, 0, len(settingsCategories)+1)
	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(colorCyan).
		Render("Categories")
	rows = append(rows, header)
	for i, def := range settingsCategories {
		marker := "  "
		if i == g.categoryCursor {
			marker = "▸ "
		}
		label := def.Label
		paneActive := g.pane == paneCategories && !g.searchOpen
		line := marker + label
		switch {
		case i == g.categoryCursor && paneActive:
			line = settingsCursorStyle.Width(width).Render(line)
		case i == g.categoryCursor:
			// Selected but pane not focused — bold without highlight.
			line = lipgloss.NewStyle().Bold(true).Render(line)
		}
		rows = append(rows, lipgloss.NewStyle().Width(width).Render(line))
	}
	return strings.Join(rows, "\n")
}

func (g *GlobalSettingsForm) renderFieldsPane() string {
	def := settingsCategories[g.categoryCursor]
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(colorCyan)
	lines := []string{headerStyle.Render(def.Label)}
	if def.Stub != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(colorDim).Render(def.Stub))
		return strings.Join(lines, "\n")
	}
	switch def.ID {
	case catAgentPaths:
		lines = append(lines, g.renderAgentPathRows()...)
	case catDefaults:
		lines = append(lines, g.renderDefaultsRows()...)
	case catNotifications:
		lines = append(lines, g.renderNotificationsRows()...)
	}
	return strings.Join(lines, "\n")
}

// labelStyleForFields returns a width-aware label style sized to the longest
// label visible in the current category. Re-computing per render keeps the
// layout responsive when the category changes (different label lengths).
func (g *GlobalSettingsForm) labelStyleForFields() lipgloss.Style {
	labelWidth := len("Global default:")
	if l := len("Terminal shell:"); l > labelWidth {
		labelWidth = l
	}
	for _, r := range g.agentRows {
		if l := len(r.DisplayName) + 1; l > labelWidth {
			labelWidth = l
		}
	}
	return settingsLabelStyle.Width(labelWidth + 1)
}

func (g *GlobalSettingsForm) renderAgentPathRows() []string {
	if len(g.agentRows) == 0 {
		return []string{lipgloss.NewStyle().Foreground(colorDim).Render("  (no agents registered)")}
	}
	labelStyle := g.labelStyleForFields()
	out := make([]string, 0, len(g.agentRows))
	for i, r := range g.agentRows {
		label := labelStyle.Render(r.DisplayName + ":")
		var val string
		if g.editing && g.cursor == i && g.pane == paneFields {
			val = g.input.View()
		} else if r.Path == "" {
			if r.Available {
				val = lipgloss.NewStyle().Foreground(colorDim).Render("(auto)")
			} else {
				val = lipgloss.NewStyle().Foreground(colorYellow).Render("(not installed)")
			}
		} else {
			val = settingsValueStyle.Render(r.Path)
		}
		line := "  " + label + " " + val
		if i == g.cursor && g.pane == paneFields && !g.searchOpen {
			line = settingsCursorStyle.Render(line)
		}
		out = append(out, line)
	}
	return out
}

func (g *GlobalSettingsForm) renderDefaultsRows() []string {
	labelStyle := g.labelStyleForFields()

	// Default agent.
	dlabel := labelStyle.Render("Default:")
	display := ""
	if g.defaultIndex >= 0 && g.defaultIndex < len(g.defaultOptions) {
		display = g.defaultOptions[g.defaultIndex].Display
	}
	dline := "  " + dlabel + " " + settingsValueStyle.Render(display)
	if g.cursor == g.defaultCursor() && g.pane == paneFields && !g.searchOpen {
		dline = settingsCursorStyle.Render(dline)
	}

	// Terminal shell.
	tlabel := labelStyle.Render("Shell:")
	var tval string
	if g.editing && g.cursor == g.terminalCursor() && g.pane == paneFields {
		tval = g.input.View()
	} else if g.terminalShell == "" {
		tval = lipgloss.NewStyle().Foreground(colorDim).Render("($SHELL — login-shell autodetect)")
	} else {
		tval = settingsValueStyle.Render(g.terminalShell)
	}
	tline := "  " + tlabel + " " + tval
	if g.cursor == g.terminalCursor() && g.pane == paneFields && !g.searchOpen {
		tline = settingsCursorStyle.Render(tline)
	}
	return []string{dline, tline}
}

func (g *GlobalSettingsForm) renderNotificationsRows() []string {
	out := make([]string, 0, int(notifyRowCount)+6)
	out = append(out,
		g.notifyToggleLine("Enable notifications", g.notify.Enabled, notifyRowEnabled),
		g.notifyToggleLine("Notify on task failure", g.notify.EventTaskFailed, notifyRowEventTaskFailed),
		g.notifyToggleLine("Notify on run complete", g.notify.EventRunComplete, notifyRowEventRunComplete),
		g.notifyToggleLine("Send weekly digest", g.notify.EventWeeklyDigest, notifyRowEventWeeklyDigest),
		g.notifyValueLine("Digest schedule", g.notify.DigestSchedule, notifyRowDigestSchedule),
	)
	out = append(out, "", lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Render("Sounds"))
	out = append(out,
		g.notifyToggleLine("Play sounds", g.notify.SoundsEnabled, notifyRowSoundsEnabled),
		g.notifyToggleLine("Sound on task failure", g.notify.SoundTaskFailed, notifyRowSoundTaskFailed),
		g.notifyToggleLine("Sound on run complete", g.notify.SoundRunComplete, notifyRowSoundRunComplete),
		g.notifyValueLine("Volume", g.volumeBar(), notifyRowVolume),
	)
	out = append(out, "", lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Render("Quiet Hours"))
	out = append(out,
		g.notifyToggleLine("Mute during window", g.notify.QuietEnabled, notifyRowQuietEnabled),
		g.notifyValueLine("Start", g.notify.QuietStart, notifyRowQuietStart),
		g.notifyValueLine("End", g.notify.QuietEnd, notifyRowQuietEnd),
	)
	return out
}

func (g *GlobalSettingsForm) notifyToggleLine(label string, on bool, row notifyRow) string {
	mark := "[ ]"
	if on {
		mark = "[x]"
	}
	line := "  " + mark + " " + label
	if g.cursor == g.notifyCursorBase()+int(row) && g.pane == paneFields && !g.searchOpen {
		line = settingsCursorStyle.Render(line)
	}
	return line
}

func (g *GlobalSettingsForm) notifyValueLine(label, value string, row notifyRow) string {
	idx := g.notifyCursorBase() + int(row)
	prefix := "  " + lipgloss.NewStyle().Foreground(colorDim).Render(label+":") + " "
	if g.editing && g.cursor == idx && g.pane == paneFields {
		return prefix + g.input.View()
	}
	line := prefix + settingsValueStyle.Render(value)
	if g.cursor == idx && g.pane == paneFields && !g.searchOpen {
		line = settingsCursorStyle.Render(line)
	}
	return line
}

// volumeBar renders a 10-segment bar plus the percentage, mirroring the GUI
// slider treatment.
func (g *GlobalSettingsForm) volumeBar() string {
	const segments = 10
	filled := g.notify.Volume * segments / 100
	if filled < 0 {
		filled = 0
	}
	if filled > segments {
		filled = segments
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", segments-filled)
	return fmt.Sprintf("%s %3d%%", bar, g.notify.Volume)
}
