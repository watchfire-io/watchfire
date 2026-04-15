package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"

	"github.com/watchfire-io/watchfire/internal/daemon/agent/backend"
	pb "github.com/watchfire-io/watchfire/proto"
)

// askPerProjectValue is the sentinel stored in Settings.Defaults.DefaultAgent
// to mean "prompt at init time". It must be empty so it round-trips
// through the proto string field cleanly.
const askPerProjectValue = ""

// GlobalSettingsForm is the overlay used to edit ~/.watchfire/settings.yaml.
// It shows one editable path row per registered backend plus a cycling
// "Global default agent" selector that includes an "Ask per project" option.
type GlobalSettingsForm struct {
	// agentRows has one entry per backend in backend.List() order.
	agentRows []agentPathRow
	// defaultOptions is [Ask per project, backend1, backend2, ...].
	defaultOptions []CycleOption
	defaultIndex   int

	cursor  int // 0..len(agentRows)-1 = agent paths, last row = default selector
	editing bool
	input   textinput.Model
	width   int

	loaded bool
}

type agentPathRow struct {
	Name        string // backend name (e.g. "claude-code")
	DisplayName string
	Path        string // empty = "auto"
}

// NewGlobalSettingsForm builds the form with rows derived from the
// backend registry. Settings values are loaded separately via Load.
func NewGlobalSettingsForm() *GlobalSettingsForm {
	ti := textinput.New()
	ti.CharLimit = 500

	backends := backend.List()
	rows := make([]agentPathRow, 0, len(backends))
	for _, b := range backends {
		rows = append(rows, agentPathRow{
			Name:        b.Name(),
			DisplayName: b.DisplayName(),
		})
	}

	opts := make([]CycleOption, 0, len(backends)+1)
	opts = append(opts, CycleOption{Value: askPerProjectValue, Display: "Ask per project"})
	for _, b := range backends {
		opts = append(opts, CycleOption{Value: b.Name(), Display: b.DisplayName()})
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

func (g *GlobalSettingsForm) rowCount() int { return len(g.agentRows) + 1 }

// defaultCursor is the index of the global-default row.
func (g *GlobalSettingsForm) defaultCursor() int { return len(g.agentRows) }

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

// StartEdit enters edit mode on the currently selected agent path row.
// Returns false when the cursor is on a non-editable row (e.g. the
// default selector) or when there are no backends registered.
func (g *GlobalSettingsForm) StartEdit() bool {
	if g.cursor < 0 || g.cursor >= len(g.agentRows) {
		return false
	}
	g.editing = true
	g.input.SetValue(g.agentRows[g.cursor].Path)
	g.input.Focus()
	return true
}

// CancelEdit exits edit mode without applying changes.
func (g *GlobalSettingsForm) CancelEdit() {
	g.editing = false
	g.input.Blur()
}

// FinishEdit applies the edit. Returns (changed, agentName, newPath)
// so the caller can dispatch an UpdateSettings RPC.
func (g *GlobalSettingsForm) FinishEdit() (changed bool, agentName, path string) {
	if !g.editing {
		return false, "", ""
	}
	g.editing = false
	g.input.Blur()
	if g.cursor < 0 || g.cursor >= len(g.agentRows) {
		return false, "", ""
	}
	row := &g.agentRows[g.cursor]
	newPath := strings.TrimSpace(g.input.Value())
	if newPath == row.Path {
		return false, "", ""
	}
	row.Path = newPath
	return true, row.Name, newPath
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
			val = lipgloss.NewStyle().Foreground(colorDim).Render("(auto)")
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

	// Footer hints
	lines = append(lines, "")
	hint := "j/k  navigate   Enter  edit/cycle   Esc  close"
	if g.editing {
		hint = "Enter  apply   Esc  cancel"
	}
	lines = append(lines, lipgloss.NewStyle().Foreground(colorDim).Render(hint))

	return strings.Join(lines, "\n")
}
