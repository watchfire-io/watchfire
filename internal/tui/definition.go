package tui

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// DefinitionView displays the project definition in a read-only viewport.
type DefinitionView struct {
	viewport viewport.Model
	content  string
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
	if d.content == "" {
		return lipgloss.NewStyle().
			Foreground(colorDim).
			Render("No project definition. Press 'e' to edit.")
	}
	return d.viewport.View()
}

// launchEditorCmd returns a tea.Cmd that suspends bubbletea,
// opens the definition in $EDITOR, and returns the edited content.
func launchEditorCmd(content string) tea.Cmd {
	editor := findEditorPath()
	if editor == "" {
		return func() tea.Msg {
			return EditorFinishedMsg{Err: os.ErrNotExist}
		}
	}

	// Create temp file
	tmpFile := filepath.Join(os.TempDir(), "watchfire-definition.md")
	if err := os.WriteFile(tmpFile, []byte(content), 0o600); err != nil {
		return func() tea.Msg {
			return EditorFinishedMsg{Err: err}
		}
	}

	c := exec.Command(editor, tmpFile) //nolint:noctx // tea.ExecProcess requires *exec.Cmd, not CommandContext
	return tea.ExecProcess(c, func(err error) tea.Msg {
		if err != nil {
			return EditorFinishedMsg{Err: err}
		}
		edited, readErr := os.ReadFile(tmpFile)
		_ = os.Remove(tmpFile)
		if readErr != nil {
			return EditorFinishedMsg{Err: readErr}
		}
		return EditorFinishedMsg{Content: string(edited)}
	})
}

// findEditorPath locates the user's preferred editor.
func findEditorPath() string {
	if editor := os.Getenv("EDITOR"); editor != "" {
		return editor
	}
	if editor := os.Getenv("VISUAL"); editor != "" {
		return editor
	}
	for _, name := range []string{"vim", "vi", "nano"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}
