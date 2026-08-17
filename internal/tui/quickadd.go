package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/lipgloss"

	"github.com/watchfire-io/watchfire/internal/daemon/task"
)

// Quick-add form focus indexes.
const (
	quickAddFocusText   = 0
	quickAddFocusStatus = 1
	quickAddFieldCount  = 2
)

// QuickAddForm is the batch task creation overlay (v10 Torch, issue #19):
// one multiline editor, a draft/ready toggle, zero per-task form friction.
// The daemon splits the text into tasks via TaskService.CreateTasksBatch;
// the live count preview here runs the same shared parser in-process.
type QuickAddForm struct {
	textArea   textarea.Model
	status     string // "draft" or "ready" — default ready
	focusIndex int
	width      int
}

// NewQuickAddForm creates a new quick-add form.
func NewQuickAddForm(width int) *QuickAddForm {
	ta := textarea.New()
	ta.Placeholder = "- One task per top-level bullet\n- Nested lines fold into the task above\n  AC: lines become acceptance criteria"
	ta.SetWidth(width - 8)
	ta.SetHeight(10)
	ta.Focus()

	return &QuickAddForm{
		textArea: ta,
		status:   "ready",
		width:    width,
	}
}

// FocusNext moves between the text area and the status toggle.
func (qf *QuickAddForm) FocusNext() {
	qf.focusIndex = (qf.focusIndex + 1) % quickAddFieldCount
	if qf.focusIndex == quickAddFocusText {
		qf.textArea.Focus()
	} else {
		qf.textArea.Blur()
	}
}

// ToggleStatus cycles the status between ready and draft.
func (qf *QuickAddForm) ToggleStatus() {
	if qf.status == "ready" {
		qf.status = "draft"
	} else {
		qf.status = "ready"
	}
}

// Text returns the current editor content.
func (qf *QuickAddForm) Text() string {
	return qf.textArea.Value()
}

// Status returns the selected status for every created task.
func (qf *QuickAddForm) Status() string {
	return qf.status
}

// FocusIndex returns the currently focused field index.
func (qf *QuickAddForm) FocusIndex() int {
	return qf.focusIndex
}

// TextArea returns the textarea model for update forwarding.
func (qf *QuickAddForm) TextArea() *textarea.Model {
	return &qf.textArea
}

// TaskCount returns how many tasks the current text would create, using
// the same parser the daemon applies on submit.
func (qf *QuickAddForm) TaskCount() int {
	return len(task.ParseQuickAdd(qf.textArea.Value()))
}

// View renders the quick-add overlay.
func (qf *QuickAddForm) View() string {
	formWidth := qf.width
	if formWidth > 70 {
		formWidth = 70
	}
	if formWidth < 30 {
		formWidth = 30
	}

	parts := make([]string, 0, 10)
	parts = append(parts, overlayTitleStyle.Render("Quick Add Tasks"))

	label := lipgloss.NewStyle().Bold(true).Render("Tasks:")
	hint := lipgloss.NewStyle().Foreground(colorDim).Render(" one per top-level bullet (- * 1.)")
	parts = append(parts, label+hint, qf.textArea.View(), "")

	// Status toggle
	label = lipgloss.NewStyle().Bold(true).Render("Create as:")
	var statusDisplay string
	if qf.status == "draft" {
		statusDisplay = taskDraftStyle.Render("Draft")
	} else {
		statusDisplay = taskReadyStyle.Render("Ready")
	}
	if qf.focusIndex == quickAddFocusStatus {
		statusDisplay += lipgloss.NewStyle().Foreground(colorDim).Render("  (Space/Enter to toggle)")
	}
	parts = append(parts, label+" "+statusDisplay, "")

	// Live count preview
	count := qf.TaskCount()
	var preview string
	if count == 0 {
		preview = lipgloss.NewStyle().Foreground(colorDim).Render("Nothing to create yet")
	} else {
		preview = lipgloss.NewStyle().Foreground(colorCyan).Render(
			fmt.Sprintf("Will create %d task%s", count, plural(count)))
	}
	parts = append(parts, preview, "")

	footer := lipgloss.NewStyle().Foreground(colorDim).Render("Ctrl+s create  |  Tab toggle field  |  Esc cancel")
	parts = append(parts, footer)

	content := strings.Join(parts, "\n")
	return overlayStyle.Width(formWidth).Render(content)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
