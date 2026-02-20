package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/watchfire-io/watchfire/internal/daemon/task"
)

var runCmd = &cobra.Command{
	Use:   "run [task-number|all]",
	Short: "Start an agent session",
	Long: `Start an agent session in the current project.

Without arguments, starts in chat mode.
With a task number, runs the agent on that specific task.
With "all", runs all ready tasks in sequence.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runRun,
}

func runRun(cmd *cobra.Command, args []string) error {
	projectPath, err := getProjectPath()
	if err != nil {
		return err
	}

	// Determine mode
	mode := "chat"
	var taskNumber int32
	if len(args) > 0 {
		if args[0] == "all" {
			mode = "start-all"
		} else {
			mode = "task"
			n, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid task number: %s", args[0])
			}
			taskNumber = int32(n)

			// Validate task exists
			mgr := task.NewManager()
			_, err = mgr.GetTask(projectPath, n)
			if err != nil {
				return err
			}
			fmt.Println(styleSuccess.Render(fmt.Sprintf("Task #%04d validated.", n)))
		}
	}

	return runAgentAttach(projectPath, mode, taskNumber)
}
