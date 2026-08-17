package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/watchfire-io/watchfire/internal/config"
	pb "github.com/watchfire-io/watchfire/proto"
)

// definitionCmd groups definition-scoped verbs. The existing top-level
// `watchfire define` (edit in $EDITOR) predates this group and stays as-is.
var definitionCmd = &cobra.Command{
	Use:     "definition",
	Aliases: []string{"defn"},
	Short:   "Project definition operations",
}

var (
	retrofitArchive bool
	retrofitYes     bool
)

var definitionRetrofitCmd = &cobra.Command{
	Use:   "retrofit",
	Short: "Fold completed tasks back into the project definition",
	Long: `Run a retrofit-definition agent session: the agent reads the done tasks
completed since the last retrofit and updates the project definition to
describe what the product is now. The session is attached to your terminal
and fully interruptible.

With --archive, after the session ends you are offered to archive
(soft-delete, reversible from Trash) the folded done tasks. Archiving
always asks for confirmation unless --yes is given. Archived tasks keep
counting in insights.`,
	RunE: runDefinitionRetrofit,
}

func runDefinitionRetrofit(cmd *cobra.Command, args []string) error {
	projectPath, err := getProjectPath()
	if err != nil {
		return err
	}

	if err := runAgentAttach(projectPath, "retrofit-definition", 0); err != nil {
		return err
	}

	return offerRetrofitArchive(projectPath, retrofitArchive, retrofitYes, os.Stdin, os.Stdout)
}

// offerRetrofitArchive runs the post-session archive step: fetch the folded
// candidates (dry run), and — only when archive was requested AND the user
// confirms (or --yes) — archive them through the daemon's soft-delete path.
func offerRetrofitArchive(projectPath string, archive, yes bool, in io.Reader, out io.Writer) error {
	conn, err := ConnectDaemon()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	project, err := config.LoadProject(projectPath)
	if err != nil {
		return fmt.Errorf("failed to load project config: %w", err)
	}

	client := pb.NewTaskServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	candidates, err := client.ArchiveRetrofitTasks(ctx, &pb.ArchiveRetrofitRequest{
		ProjectId: project.ProjectID,
		DryRun:    true,
	})
	if err != nil {
		return fmt.Errorf("failed to list folded tasks: %w", err)
	}

	n := len(candidates.Tasks)
	if n == 0 {
		fmt.Fprintln(out, "No folded done tasks to archive.")
		return nil
	}

	if !archive {
		fmt.Fprintf(out, "%d folded done task(s) can be archived — rerun with --archive, or archive from the GUI/TUI.\n", n)
		return nil
	}

	if !yes && !confirmPrompt(in, out, fmt.Sprintf("Archive %d folded task(s)? They move to Trash (reversible) and keep counting in insights. [y/N] ", n)) {
		fmt.Fprintln(out, "Archive cancelled — folded tasks left in place.")
		return nil
	}

	archived, err := client.ArchiveRetrofitTasks(ctx, &pb.ArchiveRetrofitRequest{
		ProjectId: project.ProjectID,
		DryRun:    false,
	})
	if err != nil {
		return fmt.Errorf("failed to archive folded tasks: %w", err)
	}
	fmt.Fprintf(out, "Archived %d folded task(s) to Trash.\n", len(archived.Tasks))
	return nil
}

// confirmPrompt asks a yes/no question and returns true only on an explicit
// yes ("y"/"yes", case-insensitive). Any other input — including EOF —
// counts as no, so a non-interactive pipe can never accidentally confirm.
func confirmPrompt(in io.Reader, out io.Writer, question string) bool {
	fmt.Fprint(out, question)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}
