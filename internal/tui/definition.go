package tui

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/watchfire-io/watchfire/internal/editor"
)

// DefinitionView displays the project definition in a read-only viewport.
type DefinitionView struct {
	viewport viewport.Model
	content  string
	loaded   bool
	width    int
	height   int
}

// NewDefinitionView creates a new definition view.
func NewDefinitionView() *DefinitionView {
	vp := viewport.New(80, 24)
	return &DefinitionView{
		viewport: vp,
	}
}

// SetContent updates the definition text.
func (d *DefinitionView) SetContent(content string) {
	d.content = content
	d.loaded = true
	d.viewport.SetContent(content)
}

// SetSize updates dimensions.
func (d *DefinitionView) SetSize(width, height int) {
	d.width = width
	d.height = height
	d.viewport.Width = width
	d.viewport.Height = height
}

// ScrollUp scrolls the viewport up.
func (d *DefinitionView) ScrollUp() {
	d.viewport.ScrollUp(1)
}

// ScrollDown scrolls the viewport down.
func (d *DefinitionView) ScrollDown() {
	d.viewport.ScrollDown(1)
}

// View renders the definition.
func (d *DefinitionView) View() string {
	if !d.loaded && d.content == "" {
		return lipgloss.NewStyle().
			Foreground(colorDim).
			Render("Loading definition...")
	}
	if d.content == "" {
		return lipgloss.NewStyle().
			Foreground(colorDim).
			Render("No project definition. Press 'e' to edit.")
	}
	return d.viewport.View()
}

// launchEditorCmd suspends the Bubble Tea render loop, opens the
// project definition in the user's external editor (precedence
// $VISUAL → $EDITOR → vim → vi via editor.Find), and returns an
// EditorFinishedMsg on exit. The editor inherits the controlling
// terminal via tea.ExecProcess; Bubble Tea reattaches stdin/stdout
// when the child exits and dispatches the message into Update so
// msghandler.go can decide whether to fire UpdateProject.
//
// Concurrent-edit handling: last-write-wins. If the daemon updates
// project.yaml from another client (e.g. `watchfire define` or a
// second TUI) while the editor is open, the in-flight save here
// will clobber that edit. v6.x ships this limitation explicitly;
// preconditioned UpdateProject (UpdatedAt-based) is deferred until
// the proto schema gains a precondition field.
func launchEditorCmd(content string) tea.Cmd {
	bin := editor.Find()
	if bin == "" {
		return func() tea.Msg {
			return EditorFinishedMsg{Err: fmt.Errorf("no editor found — set $VISUAL or $EDITOR")}
		}
	}

	tmpFile, err := editor.WriteTempFile("definition.md", content)
	if err != nil {
		return func() tea.Msg {
			return EditorFinishedMsg{Err: err}
		}
	}

	c := exec.Command(bin, tmpFile) //nolint:noctx // tea.ExecProcess requires *exec.Cmd, not CommandContext
	return tea.ExecProcess(c, func(execErr error) tea.Msg {
		if execErr != nil {
			_ = os.Remove(tmpFile)
			return EditorFinishedMsg{Err: execErr}
		}
		edited, readErr := editor.ReadTempFile(tmpFile)
		if readErr != nil {
			return EditorFinishedMsg{Err: readErr}
		}
		return EditorFinishedMsg{Content: edited}
	})
}
