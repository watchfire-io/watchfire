package cli

import (
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/watchfire-io/watchfire/internal/config"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Manage the Watchfire daemon",
	Long:  `Manage the Watchfire daemon process.`,
}

var daemonStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon status",
	RunE:  runDaemonStatus,
}

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the daemon",
	RunE:  runDaemonStart,
}

var daemonStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the daemon",
	RunE:  runDaemonStop,
}

func init() {
	daemonCmd.AddCommand(daemonStartCmd)
	daemonCmd.AddCommand(daemonStatusCmd)
	daemonCmd.AddCommand(daemonStopCmd)
}

func runDaemonStart(cmd *cobra.Command, args []string) error {
	running, info, err := config.IsDaemonRunning()
	if err != nil {
		return fmt.Errorf("failed to check daemon status: %w", err)
	}

	if running && info != nil {
		fmt.Printf("Daemon is already running (PID %d, port %d).\n", info.PID, info.Port)
		return nil
	}

	// Clean up stale daemon info if it exists
	if info != nil {
		_ = config.RemoveDaemonInfo()
	}

	fmt.Print("Starting daemon...")
	if startErr := startDaemon(); startErr != nil {
		fmt.Println()
		return startErr
	}

	// Fetch fresh status to display
	_, freshInfo, err := GetDaemonStatus()
	if err != nil || freshInfo == nil {
		fmt.Println(" started.")
		return nil
	}

	fmt.Printf(" started (PID %d, port %d).\n", freshInfo.PID, freshInfo.Port)
	return nil
}

func runDaemonStatus(cmd *cobra.Command, args []string) error {
	running, info, err := GetDaemonStatus()
	if err != nil {
		return err
	}

	if !running || info == nil {
		fmt.Println("Daemon is not running.")
		return nil
	}

	uptime := time.Since(info.StartedAt).Truncate(time.Second)

	fmt.Println("Daemon is running.")
	fmt.Printf("  Host:       %s\n", info.Host)
	fmt.Printf("  Port:       %d\n", info.Port)
	fmt.Printf("  PID:        %d\n", info.PID)
	fmt.Printf("  Uptime:     %s\n", uptime)

	// Show running agents
	state, err := config.LoadAgentState()
	if err != nil {
		return nil // Non-fatal: just skip agent display
	}

	if len(state.Agents) == 0 {
		fmt.Println("\nNo active agents.")
	} else {
		fmt.Printf("\nActive agents (%d):\n", len(state.Agents))
		for _, a := range state.Agents {
			mode := a.Mode
			detail := ""
			switch a.Mode {
			case "task":
				detail = fmt.Sprintf(" — Task #%04d: %s", a.TaskNumber, a.TaskTitle)
			case "wildfire":
				detail = fmt.Sprintf(" — Task #%04d: %s", a.TaskNumber, a.TaskTitle)
				mode = "wildfire"
			}
			fmt.Printf("  %s [%s]%s\n", a.ProjectName, mode, detail)
			fmt.Printf("    %s\n", a.ProjectPath)
		}
	}

	return nil
}

func runDaemonStop(cmd *cobra.Command, args []string) error {
	running, info, err := config.IsDaemonRunning()
	if err != nil {
		return fmt.Errorf("failed to check daemon status: %w", err)
	}

	if !running || info == nil {
		fmt.Println("Daemon is not running.")
		return nil
	}

	// Send SIGTERM to the daemon process
	process, err := os.FindProcess(info.PID)
	if err != nil {
		return fmt.Errorf("failed to find daemon process: %w", err)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to send stop signal: %w", err)
	}

	// Poll for shutdown (max 5 seconds)
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		stillRunning, _, err := config.IsDaemonRunning()
		if err == nil && !stillRunning {
			fmt.Println("Daemon stopped.")
			return nil
		}
	}

	return fmt.Errorf("daemon did not stop within timeout")
}
