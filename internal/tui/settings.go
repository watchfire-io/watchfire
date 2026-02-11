package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"

	pb "github.com/watchfire-io/watchfire/proto"
)

// FieldType defines the type of a settings field.
type FieldType int

const (
	fieldText FieldType = iota
	fieldToggle
)

// SettingsField is a single field in the settings form.
type SettingsField struct {
	Label     string
	Key       string // Maps to project field
	Value     string
	BoolValue bool
	Type      FieldType
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
	s.fields = []SettingsField{
		{Label: "Name", Key: "name", Value: project.Name, Type: fieldText},
		{Label: "Color", Key: "color", Value: project.Color, Type: fieldText},
		{Label: "Default Branch", Key: "default_branch", Value: project.DefaultBranch, Type: fieldText},
		{Label: "Auto-merge", Key: "auto_merge", BoolValue: project.AutoMerge, Type: fieldToggle},
		{Label: "Auto-delete Branch", Key: "auto_delete_branch", BoolValue: project.AutoDeleteBranch, Type: fieldToggle},
		{Label: "Auto-start Tasks", Key: "auto_start_tasks", BoolValue: project.AutoStartTasks, Type: fieldToggle},
	}
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

// Toggle toggles a boolean field or starts editing a text field.
func (s *SettingsForm) Toggle() (changed bool, key string, value interface{}) {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return false, "", nil
	}
	f := &s.fields[s.cursor]
	if f.Type == fieldToggle {
		f.BoolValue = !f.BoolValue
		return true, f.Key, f.BoolValue
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

	var lines []string
	for i, f := range s.fields {
		var line string
		label := settingsLabelStyle.Render(f.Label + ":")

		if f.Type == fieldToggle {
			var val string
			if f.BoolValue {
				val = settingsToggleOn.Render("[ON]")
			} else {
				val = settingsToggleOff.Render("[OFF]")
			}
			line = label + " " + val
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
