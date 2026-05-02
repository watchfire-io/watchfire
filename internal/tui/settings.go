package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/agent/backend"
	pb "github.com/watchfire-io/watchfire/proto"
)

// FieldType defines the type of a settings field.
type FieldType int

const (
	fieldText FieldType = iota
	fieldToggle
	fieldCycle
)

// CycleOption is a single option in a cycling field.
type CycleOption struct {
	Value   string // Value persisted (e.g. backend Name())
	Display string // Label shown to the user (e.g. backend DisplayName())
}

// SettingsField is a single field in the settings form.
type SettingsField struct {
	Label        string
	Key          string // Maps to project field
	Value        string
	BoolValue    bool
	Type         FieldType
	CycleOptions []CycleOption
	CycleIndex   int
}

// SettingsForm manages the settings tab.
type SettingsForm struct {
	fields  []SettingsField
	cursor  int
	editing bool
	input   textinput.Model
	width   int
	height  int
}

// NewSettingsForm creates a new settings form.
func NewSettingsForm() *SettingsForm {
	ti := textinput.New()
	ti.CharLimit = 100
	return &SettingsForm{
		input: ti,
	}
}

// LoadFromProject populates fields from project data.
func (s *SettingsForm) LoadFromProject(project *pb.Project) {
	agentOptions := buildAgentCycleOptions()
	agentIdx := agentCycleIndex(agentOptions, project.DefaultAgent)

	muted := false
	if project.Notifications != nil {
		muted = project.Notifications.Muted
	}

	s.fields = []SettingsField{
		{Label: "Name", Key: "name", Value: project.Name, Type: fieldText},
		{Label: "Color", Key: "color", Value: project.Color, Type: fieldText},
		{Label: "Agent", Key: "default_agent", Type: fieldCycle, CycleOptions: agentOptions, CycleIndex: agentIdx},
		{Label: "Auto-merge", Key: "auto_merge", BoolValue: project.AutoMerge, Type: fieldToggle},
		{Label: "Auto-delete Branch", Key: "auto_delete_branch", BoolValue: project.AutoDeleteBranch, Type: fieldToggle},
		{Label: "Auto-start Tasks", Key: "auto_start_tasks", BoolValue: project.AutoStartTasks, Type: fieldToggle},
		{Label: "Mute Notifications", Key: "notifications_muted", BoolValue: muted, Type: fieldToggle},
	}
}

// buildAgentCycleOptions returns the ordered cycle options for the Agent
// field, derived from the backend registry. When no backend is registered
// (tests, stripped builds) it falls back to a single "claude-code" option so
// the UI remains stable.
//
// Every registered backend appears — agents whose binary does not resolve on
// the host are still listed with a "(not installed)" suffix rather than being
// hidden. Hiding them was the root cause of issue #29, where a freshly
// installed Codex CLI didn't appear until the picker was re-enumerated on the
// host that happened to have the right fallback path.
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
// found (e.g. existing project with unset or unknown agent), it returns the
// index of "claude-code" when present, else 0.
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

// SetSize updates dimensions.
func (s *SettingsForm) SetSize(width, height int) {
	s.width = width
	s.height = height
	s.input.Width = width - 24
}

// MoveUp moves cursor up.
func (s *SettingsForm) MoveUp() {
	if !s.editing && s.cursor > 0 {
		s.cursor--
	}
}

// MoveDown moves cursor down.
func (s *SettingsForm) MoveDown() {
	if !s.editing && s.cursor < len(s.fields)-1 {
		s.cursor++
	}
}

// Toggle toggles a boolean field, cycles a cycle field, or returns no-op for
// text fields.
func (s *SettingsForm) Toggle() (changed bool, key string, value interface{}) {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return false, "", nil
	}
	f := &s.fields[s.cursor]
	switch f.Type {
	case fieldToggle:
		f.BoolValue = !f.BoolValue
		return true, f.Key, f.BoolValue
	case fieldCycle:
		if len(f.CycleOptions) == 0 {
			return false, "", nil
		}
		f.CycleIndex = (f.CycleIndex + 1) % len(f.CycleOptions)
		return true, f.Key, f.CycleOptions[f.CycleIndex].Value
	}
	return false, "", nil
}

// StartEdit begins inline editing of the current text field.
func (s *SettingsForm) StartEdit() bool {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return false
	}
	f := s.fields[s.cursor]
	if f.Type != fieldText {
		// For toggle, just toggle
		return false
	}
	s.editing = true
	s.input.SetValue(f.Value)
	s.input.Focus()
	return true
}

// FinishEdit confirms the current edit.
func (s *SettingsForm) FinishEdit() (changed bool, key string, value interface{}) {
	if !s.editing {
		return false, "", nil
	}
	s.editing = false
	s.input.Blur()

	f := &s.fields[s.cursor]
	newVal := s.input.Value()

	// Validate color field
	if f.Key == "color" && newVal != "" && !isValidColor(newVal) {
		return false, "", nil
	}

	if newVal != f.Value {
		f.Value = newVal
		return true, f.Key, newVal
	}
	return false, "", nil
}

// CancelEdit cancels the current edit.
func (s *SettingsForm) CancelEdit() {
	s.editing = false
	s.input.Blur()
}

// IsEditing returns whether a field is being edited.
func (s *SettingsForm) IsEditing() bool {
	return s.editing
}

// UpdateInput forwards a key message to the text input.
func (s *SettingsForm) UpdateInput(msg interface{}) {
	if ti, ok := msg.(textinput.Model); ok {
		s.input = ti
	}
}

// InputModel returns the text input model for Update forwarding.
func (s *SettingsForm) InputModel() *textinput.Model {
	return &s.input
}

// View renders the settings form.
func (s *SettingsForm) View() string {
	if len(s.fields) == 0 {
		return lipgloss.NewStyle().Foreground(colorDim).Render("Loading settings...")
	}

	// Compute max label width dynamically
	maxLabelLen := 0
	for _, f := range s.fields {
		if l := len(f.Label) + 1; l > maxLabelLen { // +1 for ":"
			maxLabelLen = l
		}
	}
	labelStyle := settingsLabelStyle.Width(maxLabelLen + 1) // +1 for padding

	lines := make([]string, 0, len(s.fields))
	for i, f := range s.fields {
		var line string
		label := labelStyle.Render(f.Label + ":")

		if f.Type == fieldToggle {
			var val string
			if f.BoolValue {
				val = settingsToggleOn.Render("[ON]")
			} else {
				val = settingsToggleOff.Render("[OFF]")
			}
			line = label + " " + val
		} else if f.Type == fieldCycle {
			display := ""
			if len(f.CycleOptions) > 0 && f.CycleIndex >= 0 && f.CycleIndex < len(f.CycleOptions) {
				display = f.CycleOptions[f.CycleIndex].Display
			}
			line = label + " " + settingsValueStyle.Render(display)
		} else {
			if s.editing && i == s.cursor {
				line = label + " " + s.input.View()
			} else {
				val := f.Value
				if val == "" {
					val = lipgloss.NewStyle().Foreground(colorDim).Render("(empty)")
				} else {
					val = settingsValueStyle.Render(val)
				}
				line = label + " " + val
			}
		}

		if i == s.cursor {
			line = settingsCursorStyle.Width(s.width).Render(line)
		}
		lines = append(lines, line)
	}

	return strings.Join(lines, "\n")
}

func isValidColor(color string) bool {
	match, _ := regexp.MatchString(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`, color)
	return match
}
