package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/watchfire-io/watchfire/internal/config"
)

var definitionCmd = &cobra.Command{
	Use:     "definition",
	Aliases: []string{"def"},
	Short:   "Edit project definition in external editor",
	Long: `Edit the project definition using your preferred text editor.

The definition is a markdown document that describes your project.
It provides context to coding agents about the project's purpose,
architecture, and any special instructions.

The editor is selected in order: $EDITOR, $VISUAL, vim, vi, nano.`,
	RunE: runDefinition,
}

func runDefinition(cmd *cobra.Command, args []string) error {
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

	// Edit definition in external editor
	newDefinition, err := editInEditor(project.Definition, "definition.md")
	if err != nil {
		return fmt.Errorf("failed to edit definition: %w", err)
	}

	// Check if changed
	if newDefinition == project.Definition {
		fmt.Println("No changes made.")
		return nil
	}

	// Save updated project
	project.Definition = newDefinition
	project.UpdatedAt = time.Now().UTC()

	if err := config.SaveProject(projectPath, project); err != nil {
		return fmt.Errorf("failed to save project: %w", err)
	}

	fmt.Println("Project definition updated.")
	return nil
}

// editInEditor opens the content in an external editor and returns the edited content.
func editInEditor(content, filename string) (string, error) {
	// Find editor
	editor := findEditor()
	if editor == "" {
		return "", fmt.Errorf("no editor found. Set $EDITOR or $VISUAL environment variable")
	}

	// Create temp file
	tmpDir := os.TempDir()
	tmpFile := filepath.Join(tmpDir, "watchfire-"+filename)

	if err := os.WriteFile(tmpFile, []byte(content), 0600); err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile)

	// Run editor
	cmd := exec.Command(editor, tmpFile)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("editor exited with error: %w", err)
	}

	// Read edited content
	edited, err := os.ReadFile(tmpFile)
	if err != nil {
		return "", fmt.Errorf("failed to read edited content: %w", err)
	}

	return string(edited), nil
}

// findEditor returns the user's preferred editor.
func findEditor() string {
	// Check environment variables
	if editor := os.Getenv("EDITOR"); editor != "" {
		return editor
	}
	if editor := os.Getenv("VISUAL"); editor != "" {
		return editor
	}

	// Try common editors
	editors := []string{"vim", "vi", "nano"}
	for _, editor := range editors {
		if path, err := exec.LookPath(editor); err == nil {
			return path
		}
	}

	return ""
}
