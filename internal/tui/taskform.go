package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)

// TaskForm is the add/edit task overlay form.
type TaskForm struct {
	mode       string // "add" or "edit"
	taskNumber int32  // For edit mode

	titleInput   textinput.Model
	promptArea   textarea.Model
	criteriaArea textarea.Model
	status       string // "draft" or "ready"

	focusIndex int // 0=title, 1=prompt, 2=criteria, 3=status
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

	// Focus title first
	tf.titleInput.Focus()

	return tf
}

// PreFill fills the form with existing task data for editing.
func (tf *TaskForm) PreFill(taskNumber int32, title, prompt, criteria, status string) {
	tf.taskNumber = taskNumber
	tf.titleInput.SetValue(title)
	tf.promptArea.SetValue(prompt)
	tf.criteriaArea.SetValue(criteria)
	if status == "draft" || status == "ready" {
		tf.status = status
	}
}

// FocusNext moves to the next field.
func (tf *TaskForm) FocusNext() {
	tf.blurAll()
	tf.focusIndex = (tf.focusIndex + 1) % 4
	tf.focusCurrent()
}

// FocusPrev moves to the previous field.
func (tf *TaskForm) FocusPrev() {
	tf.blurAll()
	tf.focusIndex--
	if tf.focusIndex < 0 {
		tf.focusIndex = 3
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
	case 0:
		tf.titleInput.Focus()
	case 1:
		tf.promptArea.Focus()
	case 2:
		tf.criteriaArea.Focus()
	case 3:
		// Status field — no input to focus
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

	parts := make([]string, 0, 16)
	parts = append(parts, overlayTitleStyle.Render(title))

	// Title field
	label := lipgloss.NewStyle().Bold(true).Render("Title:")
	parts = append(parts, label, tf.titleInput.View(), "")

	// Prompt field
	label = lipgloss.NewStyle().Bold(true).Render("Prompt:")
	parts = append(parts, label, tf.promptArea.View(), "")

	// Criteria field
	label = lipgloss.NewStyle().Bold(true).Render("Acceptance Criteria:")
	parts = append(parts, label, tf.criteriaArea.View(), "")

	// Status
	label = lipgloss.NewStyle().Bold(true).Render("Status:")
	var statusDisplay string
	if tf.status == "draft" {
		statusDisplay = taskDraftStyle.Render("Draft")
		if tf.focusIndex == 3 {
			statusDisplay += lipgloss.NewStyle().Foreground(colorDim).Render("  (Space/Enter to toggle)")
		}
	} else {
		statusDisplay = taskReadyStyle.Render("Ready")
		if tf.focusIndex == 3 {
			statusDisplay += lipgloss.NewStyle().Foreground(colorDim).Render("  (Space/Enter to toggle)")
		}
	}
	parts = append(parts, label+" "+statusDisplay, "")

	// Footer
	footer := lipgloss.NewStyle().Foreground(colorDim).Render("Ctrl+s save  |  Tab next field  |  Esc cancel")
	parts = append(parts, footer)

	content := strings.Join(parts, "\n")
	return overlayStyle.Width(formWidth).Render(content)
}
