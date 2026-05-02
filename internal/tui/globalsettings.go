package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/agent/backend"
	"github.com/watchfire-io/watchfire/internal/models"
	pb "github.com/watchfire-io/watchfire/proto"
)

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
	Enabled          bool
	EventTaskFailed  bool
	EventRunComplete bool
	SoundsEnabled    bool
	SoundTaskFailed  bool
	SoundRunComplete bool
	Volume           int // 0..100, displayed as N% and persisted as float32 N/100
	QuietEnabled     bool
	QuietStart       string
	QuietEnd         string
}

// GlobalSettingsForm is the overlay used to edit ~/.watchfire/settings.yaml.
// It shows one editable path row per registered backend, a cycling
// "Global default agent" selector that includes an "Ask per project" option,
// and a Notifications section with master / per-event / sound / volume /
// quiet-hours editors.
type GlobalSettingsForm struct {
	// agentRows has one entry per backend in backend.List() order.
	agentRows []agentPathRow
	// defaultOptions is [Ask per project, backend1, backend2, ...].
	defaultOptions []CycleOption
	defaultIndex   int

	notify notifyState

	cursor  int // 0..len(agentRows)-1 = agent paths, then default, then notify rows
	editing bool
	input   textinput.Model
	width   int

	loaded bool
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

	return &GlobalSettingsForm{
		agentRows:      rows,
		defaultOptions: opts,
		defaultIndex:   0,
		input:          ti,
	}
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
	if s != nil && s.Defaults != nil {
		for i, o := range g.defaultOptions {
			if o.Value == s.Defaults.DefaultAgent {
				g.defaultIndex = i
				break
			}
		}
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
		Enabled:          true,
		EventTaskFailed:  true,
		EventRunComplete: true,
		SoundsEnabled:    true,
		SoundTaskFailed:  true,
		SoundRunComplete: true,
		Volume:           60,
		QuietEnabled:     false,
		QuietStart:       "22:00",
		QuietEnd:         "08:00",
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
}

// Reset returns the form to its pre-open state: not editing, cursor
// at top. Called on close so the next open starts clean.
func (g *GlobalSettingsForm) Reset() {
	g.editing = false
	g.input.Blur()
	g.cursor = 0
}

func (g *GlobalSettingsForm) rowCount() int {
	return len(g.agentRows) + 1 + int(notifyRowCount)
}

// defaultCursor is the index of the global-default row.
func (g *GlobalSettingsForm) defaultCursor() int { return len(g.agentRows) }

// notifyCursorBase is the index of the first notification row.
func (g *GlobalSettingsForm) notifyCursorBase() int { return len(g.agentRows) + 1 }

// notifyRowAtCursor returns which notification row the cursor is on, or -1
// if the cursor is not on a notification row.
func (g *GlobalSettingsForm) notifyRowAtCursor() notifyRow {
	idx := g.cursor - g.notifyCursorBase()
	if idx < 0 || idx >= int(notifyRowCount) {
		return -1
	}
	return notifyRow(idx)
}

// MoveUp/MoveDown move the selection cursor while not editing.
func (g *GlobalSettingsForm) MoveUp() {
	if g.editing {
		return
	}
	if g.cursor > 0 {
		g.cursor--
	}
}

func (g *GlobalSettingsForm) MoveDown() {
	if g.editing {
		return
	}
	if g.cursor < g.rowCount()-1 {
		g.cursor++
	}
}

// IsEditing reports whether the path text input has focus.
func (g *GlobalSettingsForm) IsEditing() bool { return g.editing }

// InputModel exposes the text input for Update forwarding.
func (g *GlobalSettingsForm) InputModel() *textinput.Model { return &g.input }

// StartEdit enters edit mode on the currently selected agent path row OR
// on a notification text-editable row (volume / quiet-hours start/end).
// Returns false when the cursor is on a non-editable row (toggles, the
// default selector) or when there are no backends registered.
func (g *GlobalSettingsForm) StartEdit() bool {
	if g.cursor >= 0 && g.cursor < len(g.agentRows) {
		g.editing = true
		g.input.SetValue(g.agentRows[g.cursor].Path)
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
// notifications block.
type EditResult struct {
	Kind          EditKind
	AgentName     string
	Path          string
	NotifyChanged bool
	Err           string // non-empty when the edit was rejected (e.g. malformed HH:MM)
}

// EditKind identifies which part of the form an edit changed.
type EditKind int

const (
	EditNone EditKind = iota
	EditAgentPath
	EditNotify
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
// backend name. Only effective when the cursor is on the default row.
func (g *GlobalSettingsForm) CycleDefault() (changed bool, newValue string) {
	if g.editing || g.cursor != g.defaultCursor() {
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
	if g.editing {
		return false
	}
	switch g.notifyRowAtCursor() {
	case notifyRowEnabled:
		g.notify.Enabled = !g.notify.Enabled
	case notifyRowEventTaskFailed:
		g.notify.EventTaskFailed = !g.notify.EventTaskFailed
	case notifyRowEventRunComplete:
		g.notify.EventRunComplete = !g.notify.EventRunComplete
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

// NotificationsProto returns a fully-populated NotificationsConfig proto
// that the caller can stuff into an UpdateSettingsRequest.Defaults block.
func (g *GlobalSettingsForm) NotificationsProto() *pb.NotificationsConfig {
	return &pb.NotificationsConfig{
		Enabled: g.notify.Enabled,
		Events: &pb.NotificationsEvents{
			TaskFailed:  g.notify.EventTaskFailed,
			RunComplete: g.notify.EventRunComplete,
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
	}
}

// View renders the overlay body (without the outer border — caller
// wraps it with overlayStyle).
func (g *GlobalSettingsForm) View() string {
	title := overlayTitleStyle.Render("Global Settings")
	var lines []string
	lines = append(lines, title)

	if !g.loaded {
		lines = append(lines, lipgloss.NewStyle().Foreground(colorDim).Render("Loading..."))
		return strings.Join(lines, "\n")
	}

	// Section: agent binary paths
	lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Render("Agent Binary Paths"))
	if len(g.agentRows) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(colorDim).Render("  (no agents registered)"))
	}

	labelWidth := 0
	for _, r := range g.agentRows {
		if l := len(r.DisplayName) + 1; l > labelWidth {
			labelWidth = l
		}
	}
	if l := len("Global default:"); l > labelWidth {
		labelWidth = l
	}
	labelStyle := settingsLabelStyle.Width(labelWidth + 1)

	for i, r := range g.agentRows {
		label := labelStyle.Render(r.DisplayName + ":")
		var val string
		if g.editing && g.cursor == i {
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
		if i == g.cursor {
			line = settingsCursorStyle.Render(line)
		}
		lines = append(lines, line)
	}

	// Section: global default agent
	lines = append(lines, "")
	lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Render("Global Default Agent"))
	label := labelStyle.Render("Default:")
	display := ""
	if g.defaultIndex >= 0 && g.defaultIndex < len(g.defaultOptions) {
		display = g.defaultOptions[g.defaultIndex].Display
	}
	dline := "  " + label + " " + settingsValueStyle.Render(display)
	if g.cursor == g.defaultCursor() {
		dline = settingsCursorStyle.Render(dline)
	}
	lines = append(lines, dline)

	// Section: notifications
	lines = append(lines, "")
	lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Render("Notifications"))
	lines = append(lines, g.notifyToggleLine("Enable notifications", g.notify.Enabled, notifyRowEnabled))
	lines = append(lines, g.notifyToggleLine("Notify on task failure", g.notify.EventTaskFailed, notifyRowEventTaskFailed))
	lines = append(lines, g.notifyToggleLine("Notify on run complete", g.notify.EventRunComplete, notifyRowEventRunComplete))

	lines = append(lines, "")
	lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Render("Sounds"))
	lines = append(lines, g.notifyToggleLine("Play sounds", g.notify.SoundsEnabled, notifyRowSoundsEnabled))
	lines = append(lines, g.notifyToggleLine("Sound on task failure", g.notify.SoundTaskFailed, notifyRowSoundTaskFailed))
	lines = append(lines, g.notifyToggleLine("Sound on run complete", g.notify.SoundRunComplete, notifyRowSoundRunComplete))
	lines = append(lines, g.notifyValueLine("Volume", g.volumeBar(), notifyRowVolume))

	lines = append(lines, "")
	lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Render("Quiet Hours"))
	lines = append(lines, g.notifyToggleLine("Mute during window", g.notify.QuietEnabled, notifyRowQuietEnabled))
	lines = append(lines, g.notifyValueLine("Start", g.notify.QuietStart, notifyRowQuietStart))
	lines = append(lines, g.notifyValueLine("End", g.notify.QuietEnd, notifyRowQuietEnd))

	// Footer hints
	lines = append(lines, "")
	hint := "j/k  navigate   space  toggle   Enter  edit/cycle   Esc  close"
	if g.editing {
		hint = "Enter  apply   Esc  cancel"
	}
	lines = append(lines, lipgloss.NewStyle().Foreground(colorDim).Render(hint))

	return strings.Join(lines, "\n")
}

func (g *GlobalSettingsForm) notifyToggleLine(label string, on bool, row notifyRow) string {
	mark := "[ ]"
	if on {
		mark = "[x]"
	}
	line := "  " + mark + " " + label
	if g.cursor == g.notifyCursorBase()+int(row) {
		line = settingsCursorStyle.Render(line)
	}
	return line
}

func (g *GlobalSettingsForm) notifyValueLine(label, value string, row notifyRow) string {
	idx := g.notifyCursorBase() + int(row)
	prefix := "  " + lipgloss.NewStyle().Foreground(colorDim).Render(label+":") + " "
	if g.editing && g.cursor == idx {
		return prefix + g.input.View()
	}
	line := prefix + settingsValueStyle.Render(value)
	if g.cursor == idx {
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
