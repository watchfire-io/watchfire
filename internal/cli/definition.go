package cli

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/editor"
)

var defineCmd = &cobra.Command{
	Use:     "define",
	Aliases: []string{"def"},
	Short:   "Edit project definition in external editor",
	Long: `Edit the project definition using your preferred text editor.

The definition is a markdown document that describes your project.
It provides context to coding agents about the project's purpose,
architecture, and any special instructions.

The editor is selected in order: $VISUAL, $EDITOR, vim, vi.`,
	RunE: runDefine,
}

func runDefine(cmd *cobra.Command, args []string) error {
	projectPath, err := getProjectPath()
	if err != nil {
		return err
	}

	project, err := config.LoadProject(projectPath)
	if err != nil {
		return fmt.Errorf("failed to load project: %w", err)
	}
	if project == nil {
		return fmt.Errorf("failed to load project configuration")
	}

	newDefinition, err := editInEditor(project.Definition, "definition.md")
	if err != nil {
		return fmt.Errorf("failed to edit definition: %w", err)
	}

	if !editor.ShouldSave(project.Definition, newDefinition) {
		fmt.Println("No changes made.")
		return nil
	}

	project.Definition = newDefinition
	project.UpdatedAt = time.Now().UTC()

	if err := config.SaveProject(projectPath, project); err != nil {
		return fmt.Errorf("failed to save project: %w", err)
	}

	fmt.Println("Project definition updated.")
	return nil
}

// editInEditor opens content in the user's external editor, blocking
// on the foreground process, and returns the post-editor content.
// Used by `watchfire define` (CLI). The TUI Definition tab consumes
// the same internal/editor helpers but wraps the editor launch in
// tea.ExecProcess so Bubble Tea's render loop suspends cleanly.
func editInEditor(content, filename string) (string, error) {
	bin := editor.Find()
	if bin == "" {
		return "", fmt.Errorf("no editor found. Set $VISUAL or $EDITOR environment variable")
	}

	tmpFile, err := editor.WriteTempFile(filename, content)
	if err != nil {
		return "", err
	}

	cmd := exec.Command(bin, tmpFile)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		_ = os.Remove(tmpFile)
		return "", fmt.Errorf("editor exited with error: %w", err)
	}

	return editor.ReadTempFile(tmpFile)
}
