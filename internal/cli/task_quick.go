package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/task"
	pb "github.com/watchfire-io/watchfire/proto"
)

var (
	taskQuickReady bool
	taskQuickDraft bool
	taskQuickStdin bool
)

var taskQuickCmd = &cobra.Command{
	Use:   "quick",
	Short: "Create several tasks from a bullet list",
	Long: `Create several tasks at once from free text (v10 Torch quick add).

Opens $EDITOR with a commented template; each top-level bullet (-, *, or
1. numbered) becomes one task. Nested lines fold into the task above, and
lines starting with "AC:" become acceptance criteria. Text with no bullets
at all becomes a single task. Lines starting with # are ignored.

Everything is created through the daemon's validated TaskService path —
titles with colons and quotes are safe.

With --stdin the text is read from standard input instead of an editor:
  echo "- fix login\n- add pricing page" | watchfire task quick --stdin`,
	RunE: runTaskQuick,
}

const taskQuickTemplate = `# One task per top-level bullet (-, *, or 1. numbered). Lines starting
# with # are ignored. Nested lines fold into the task above; lines
# starting with "AC:" become that task's acceptance criteria.
#
# - Add a /pricing page
#   - three plan cards, FAQ section
#   AC: renders all three plans
# - Fix the login redirect loop
#
# Save and close the editor to create the tasks. An empty file aborts.

`

func runTaskQuick(cmd *cobra.Command, args []string) error {
	projectPath, err := getProjectPath()
	if err != nil {
		return err
	}
	if taskQuickReady && taskQuickDraft {
		return fmt.Errorf("--ready and --draft are mutually exclusive")
	}
	status := "ready"
	if taskQuickDraft {
		status = "draft"
	}

	var text string
	if taskQuickStdin {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("failed to read stdin: %w", err)
		}
		text = string(data)
	} else {
		text, err = editInEditor(taskQuickTemplate, "quick-add.md")
		if err != nil {
			return err
		}
	}
	text = stripCommentLines(text)

	// Preview the parse locally so an empty edit aborts without touching
	// the daemon. The daemon re-parses the same text with the same parser.
	if len(task.ParseQuickAdd(text)) == 0 {
		fmt.Println(styleHint.Render("No tasks found in input. Nothing created."))
		return nil
	}

	project, err := config.LoadProject(projectPath)
	if err != nil || project == nil {
		return fmt.Errorf("failed to load project configuration")
	}

	// Create through the daemon's validated TaskService path.
	if err := EnsureDaemon(); err != nil {
		return err
	}
	conn, err := ConnectDaemon()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	client := pb.NewTaskServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	list, err := client.CreateTasksBatch(ctx, &pb.CreateTasksBatchRequest{
		ProjectId: project.ProjectID,
		Text:      text,
		Status:    status,
	})
	if err != nil {
		return fmt.Errorf("failed to create tasks: %w", err)
	}

	fmt.Println()
	plural := "s"
	if len(list.Tasks) == 1 {
		plural = ""
	}
	fmt.Println(styleSuccess.Render(fmt.Sprintf("Created %d task%s (%s):", len(list.Tasks), plural, status)))
	for _, t := range list.Tasks {
		fmt.Printf("  %s  %s\n", styleHint.Render(fmt.Sprintf("#%04d", t.TaskNumber)), t.Title)
	}
	return nil
}

// stripCommentLines drops lines whose first non-blank character is '#' —
// the editor template's instructions. Only the CLI surface has comments;
// the shared parser never sees them.
func stripCommentLines(text string) string {
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func init() {
	taskQuickCmd.Flags().BoolVar(&taskQuickReady, "ready", false, "Create tasks as ready (default)")
	taskQuickCmd.Flags().BoolVar(&taskQuickDraft, "draft", false, "Create tasks as draft")
	taskQuickCmd.Flags().BoolVar(&taskQuickStdin, "stdin", false, "Read the task list from stdin instead of $EDITOR")
	taskCmd.AddCommand(taskQuickCmd)
}
