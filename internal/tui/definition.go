package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

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

// launchEditorOnFileCmd opens an absolute file path directly in $EDITOR
// without round-tripping through a temp buffer. Used by the v6 (#0091)
// Secrets section to edit `.watchfire/secrets/instructions.md` in place
// — there's no daemon round-trip, just a direct write from the user's
// editor. EditorFinishedMsg arrives with empty Content (the caller does
// not need to push the file back through an RPC).
func launchEditorOnFileCmd(path string) tea.Cmd {
	bin := editor.Find()
	if bin == "" {
		return func() tea.Msg {
			return EditorFinishedMsg{Err: fmt.Errorf("no editor found — set $VISUAL or $EDITOR")}
		}
	}
	if path == "" {
		return func() tea.Msg {
			return EditorFinishedMsg{Err: fmt.Errorf("no file path supplied")}
		}
	}

	c := exec.Command(bin, path) //nolint:noctx // tea.ExecProcess requires *exec.Cmd, not CommandContext
	return tea.ExecProcess(c, func(execErr error) tea.Msg {
		if execErr != nil {
			return EditorFinishedMsg{Err: execErr}
		}
		return EditorFinishedMsg{}
	})
}

// copyToClipboardCmd writes a value to the OS clipboard via pbcopy
// (macOS) / xclip (Linux). Returns a StatusMsg on success so the user
// gets a visible confirmation.
func copyToClipboardCmd(value string) tea.Cmd {
	return func() tea.Msg {
		bin, args := clipboardBinary()
		if bin == "" {
			return ErrorMsg{Err: fmt.Errorf("no clipboard tool found (need pbcopy or xclip)")}
		}
		c := exec.Command(bin, args...) //nolint:noctx
		c.Stdin = strings.NewReader(value)
		if err := c.Run(); err != nil {
			return ErrorMsg{Err: fmt.Errorf("clipboard copy failed: %w", err)}
		}
		preview := value
		if len(preview) > 40 {
			preview = preview[:37] + "..."
		}
		return StatusMsg{Text: "Copied: " + preview}
	}
}

// clipboardBinary picks the right clipboard tool for the host platform.
// Empty bin means no tool available.
func clipboardBinary() (string, []string) {
	for _, candidate := range []struct {
		bin  string
		args []string
	}{
		{"pbcopy", nil},                       // macOS
		{"xclip", []string{"-selection", "clipboard"}}, // Linux X11
		{"wl-copy", nil},                      // Linux Wayland
	} {
		if _, err := exec.LookPath(candidate.bin); err == nil {
			return candidate.bin, candidate.args
		}
	}
	return "", nil
}
