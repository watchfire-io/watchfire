package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/agent/backend"
)

// Task form focus indexes.
const (
	taskFormFocusTitle    = 0
	taskFormFocusPrompt   = 1
	taskFormFocusCriteria = 2
	taskFormFocusAgent    = 3
	taskFormFocusStatus   = 4
	taskFormFieldCount    = 5
)

// TaskForm is the add/edit task overlay form.
type TaskForm struct {
	mode       string // "add" or "edit"
	taskNumber int32  // For edit mode

	titleInput   textinput.Model
	promptArea   textarea.Model
	criteriaArea textarea.Model
	status       string // "draft" or "ready"

	// Agent override: empty string means "use project default".
	agentOptions         []CycleOption // First entry is the project-default sentinel.
	agentIndex           int
	projectDefaultAgent  string // Effective project default (used for display only).
	projectDefaultLabel  string // Pretty label for the project default.

	focusIndex int // 0=title, 1=prompt, 2=criteria, 3=agent, 4=status
	width      int
}

// NewTaskForm creates a new task form.
func NewTaskForm(mode string, width int) *TaskForm {
	ti := textinput.New()
	ti.Placeholder = "Task title"
	ti.CharLimit = 200
	ti.Width = width - 8

	pa := textarea.New()
	pa.Placeholder = "Task prompt / description"
	pa.SetWidth(width - 8)
	pa.SetHeight(4)

	ca := textarea.New()
	ca.Placeholder = "Acceptance criteria (optional)"
	ca.SetWidth(width - 8)
	ca.SetHeight(3)

	tf := &TaskForm{
		mode:         mode,
		titleInput:   ti,
		promptArea:   pa,
		criteriaArea: ca,
		status:       "draft",
		width:        width,
	}
	tf.rebuildAgentOptions("")

	// Focus title first
	tf.titleInput.Focus()

	return tf
}

// SetProjectDefaultAgent informs the form of the project's default agent so
// the "Project default" entry can display the resolved name.
func (tf *TaskForm) SetProjectDefaultAgent(name string) {
	tf.projectDefaultAgent = name
	tf.rebuildAgentOptions(tf.Agent())
}

// rebuildAgentOptions constructs the agent cycle options, placing a
// "Project default (<name>)" entry (Value == "") at index 0, followed by
// every registered backend. It preserves the currently-selected value
// across rebuilds when possible.
func (tf *TaskForm) rebuildAgentOptions(preserve string) {
	label := "Project default"
	if disp := displayNameForBackend(tf.projectDefaultAgent); disp != "" {
		label = "Project default (" + disp + ")"
	}
	tf.projectDefaultLabel = label

	backends := backend.List()
	settings, _ := config.LoadSettings()
	opts := make([]CycleOption, 0, 1+len(backends))
	opts = append(opts, CycleOption{Value: "", Display: label})
	for _, b := range backends {
		display := b.DisplayName()
		if _, err := b.ResolveExecutable(settings); err != nil {
			display = display + " (not installed)"
		}
		opts = append(opts, CycleOption{Value: b.Name(), Display: display})
	}
	tf.agentOptions = opts

	tf.agentIndex = 0
	for i, o := range opts {
		if o.Value == preserve {
			tf.agentIndex = i
			break
		}
	}
}

// displayNameForBackend returns the backend's DisplayName() or falls back
// to the raw name (or "Claude Code" for the built-in default when empty).
func displayNameForBackend(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "claude-code"
	}
	if b, ok := backend.Get(name); ok {
		return b.DisplayName()
	}
	return name
}

// PreFill fills the form with existing task data for editing.
func (tf *TaskForm) PreFill(taskNumber int32, title, prompt, criteria, status, agent string) {
	tf.taskNumber = taskNumber
	tf.titleInput.SetValue(title)
	tf.promptArea.SetValue(prompt)
	tf.criteriaArea.SetValue(criteria)
	if status == "draft" || status == "ready" {
		tf.status = status
	}
	tf.rebuildAgentOptions(strings.TrimSpace(agent))
}

// FocusNext moves to the next field.
func (tf *TaskForm) FocusNext() {
	tf.blurAll()
	tf.focusIndex = (tf.focusIndex + 1) % taskFormFieldCount
	tf.focusCurrent()
}

// FocusPrev moves to the previous field.
func (tf *TaskForm) FocusPrev() {
	tf.blurAll()
	tf.focusIndex--
	if tf.focusIndex < 0 {
		tf.focusIndex = taskFormFieldCount - 1
	}
	tf.focusCurrent()
}

func (tf *TaskForm) blurAll() {
	tf.titleInput.Blur()
	tf.promptArea.Blur()
	tf.criteriaArea.Blur()
}

func (tf *TaskForm) focusCurrent() {
	switch tf.focusIndex {
	case taskFormFocusTitle:
		tf.titleInput.Focus()
	case taskFormFocusPrompt:
		tf.promptArea.Focus()
	case taskFormFocusCriteria:
		tf.criteriaArea.Focus()
	}
}

// ToggleStatus cycles the status between draft and ready.
func (tf *TaskForm) ToggleStatus() {
	if tf.status == "draft" {
		tf.status = "ready"
	} else {
		tf.status = "draft"
	}
}

// CycleAgentNext advances the agent selector by one option.
func (tf *TaskForm) CycleAgentNext() {
	if len(tf.agentOptions) == 0 {
		return
	}
	tf.agentIndex = (tf.agentIndex + 1) % len(tf.agentOptions)
}

// CycleAgentPrev steps the agent selector back by one option.
func (tf *TaskForm) CycleAgentPrev() {
	if len(tf.agentOptions) == 0 {
		return
	}
	tf.agentIndex--
	if tf.agentIndex < 0 {
		tf.agentIndex = len(tf.agentOptions) - 1
	}
}

// Title returns the current title value.
func (tf *TaskForm) Title() string {
	return tf.titleInput.Value()
}

// Prompt returns the current prompt value.
func (tf *TaskForm) Prompt() string {
	return tf.promptArea.Value()
}

// Criteria returns the current criteria value.
func (tf *TaskForm) Criteria() string {
	return tf.criteriaArea.Value()
}

// Status returns the current status value.
func (tf *TaskForm) Status() string {
	return tf.status
}

// Agent returns the current agent override value (empty = project default).
func (tf *TaskForm) Agent() string {
	if tf.agentIndex < 0 || tf.agentIndex >= len(tf.agentOptions) {
		return ""
	}
	return tf.agentOptions[tf.agentIndex].Value
}

// FocusIndex returns the currently focused field index.
func (tf *TaskForm) FocusIndex() int {
	return tf.focusIndex
}

// TitleInput returns the title input model for update forwarding.
func (tf *TaskForm) TitleInput() *textinput.Model {
	return &tf.titleInput
}

// PromptArea returns the prompt textarea model for update forwarding.
func (tf *TaskForm) PromptArea() *textarea.Model {
	return &tf.promptArea
}

// CriteriaArea returns the criteria textarea model for update forwarding.
func (tf *TaskForm) CriteriaArea() *textarea.Model {
	return &tf.criteriaArea
}

// View renders the task form.
func (tf *TaskForm) View() string {
	title := "Add Task"
	if tf.mode == "edit" {
		title = "Edit Task"
	}

	formWidth := tf.width
	if formWidth > 70 {
		formWidth = 70
	}
	if formWidth < 30 {
		formWidth = 30
	}

	parts := make([]string, 0, 20)
	parts = append(parts, overlayTitleStyle.Render(title))

	// Title field
	label := lipgloss.NewStyle().Bold(true).Render("Title:")
	if tf.titleInput.Value() == "" && tf.focusIndex != taskFormFocusTitle {
		label += lipgloss.NewStyle().Foreground(colorDim).Render(" (required)")
	}
	parts = append(parts, label, tf.titleInput.View(), "")

	// Prompt field
	label = lipgloss.NewStyle().Bold(true).Render("Prompt:")
	parts = append(parts, label, tf.promptArea.View(), "")

	// Criteria field
	label = lipgloss.NewStyle().Bold(true).Render("Acceptance Criteria:")
	parts = append(parts, label, tf.criteriaArea.View(), "")

	// Agent field
	label = lipgloss.NewStyle().Bold(true).Render("Agent:")
	agentDisplay := ""
	if len(tf.agentOptions) > 0 {
		agentDisplay = settingsValueStyle.Render(tf.agentOptions[tf.agentIndex].Display)
	}
	agentLine := label + " " + agentDisplay
	if tf.focusIndex == taskFormFocusAgent {
		agentLine += lipgloss.NewStyle().Foreground(colorDim).Render("  (←/→ or Enter to cycle)")
	}
	parts = append(parts, agentLine, "")

	// Status
	label = lipgloss.NewStyle().Bold(true).Render("Status:")
	var statusDisplay string
	if tf.status == "draft" {
		statusDisplay = taskDraftStyle.Render("Draft")
	} else {
		statusDisplay = taskReadyStyle.Render("Ready")
	}
	if tf.focusIndex == taskFormFocusStatus {
		statusDisplay += lipgloss.NewStyle().Foreground(colorDim).Render("  (Space/Enter to toggle)")
	}
	parts = append(parts, label+" "+statusDisplay, "")

	// Footer
	footer := lipgloss.NewStyle().Foreground(colorDim).Render("Ctrl+s save  |  Tab next field  |  Esc cancel")
	parts = append(parts, footer)

	content := strings.Join(parts, "\n")
	return overlayStyle.Width(formWidth).Render(content)
}
