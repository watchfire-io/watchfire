package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/task"
	pb "github.com/watchfire-io/watchfire/proto"
)

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Manage coding agents",
	Long:  `Manage coding agent sessions for the current project.`,
}

var agentStartCmd = &cobra.Command{
	Use:   "start [task-number|all]",
	Short: "Start an agent session",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runAgentStart,
}

var agentGenerateCmd = &cobra.Command{
	Use:     "generate",
	Aliases: []string{"gen"},
	Short:   "Generate project artifacts",
	Long:    `Generate project definition or tasks using an agent.`,
}

var agentGenerateDefCmd = &cobra.Command{
	Use:     "definition",
	Aliases: []string{"def"},
	Short:   "Generate project definition",
	RunE:    runAgentGenerateDef,
}

var agentGenerateTasksCmd = &cobra.Command{
	Use:   "tasks",
	Short: "Generate tasks from project definition",
	RunE:  runAgentGenerateTasks,
}

var agentWildfireCmd = &cobra.Command{
	Use:   "wildfire",
	Short: "🔥 Run all ready tasks in sequence",
	RunE:  runAgentWildfire,
}

func init() {
	agentGenerateCmd.AddCommand(agentGenerateDefCmd)
	agentGenerateCmd.AddCommand(agentGenerateTasksCmd)

	agentCmd.AddCommand(agentGenerateCmd)
	agentCmd.AddCommand(agentStartCmd)
	agentCmd.AddCommand(agentWildfireCmd)
}

func runAgentStart(cmd *cobra.Command, args []string) error {
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
			fmt.Printf("Task #%04d validated.\n", n)
		}
	}

	return runAgentAttach(projectPath, mode, taskNumber)
}

func runAgentWildfire(cmd *cobra.Command, args []string) error {
	projectPath, err := getProjectPath()
	if err != nil {
		return err
	}

	return runAgentAttach(projectPath, "wildfire", 0)
}

// runAgentAttach connects to the daemon, starts an agent, and attaches
// the terminal to the agent's PTY stream. In wildfire mode, it re-subscribes
// when a task finishes and the daemon chains to the next ready task.
func runAgentAttach(projectPath, mode string, taskNumber int32) error {
	// Ensure daemon is running
	if err := EnsureDaemon(); err != nil {
		return err
	}

	// Connect to daemon via gRPC
	conn, err := connectDaemon()
	if err != nil {
		return err
	}
	defer conn.Close()

	// Load project config to get project ID
	project, err := config.LoadProject(projectPath)
	if err != nil {
		return fmt.Errorf("failed to load project config: %w", err)
	}

	client := pb.NewAgentServiceClient(conn)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start agent
	status, err := client.StartAgent(ctx, &pb.StartAgentRequest{
		ProjectId:  project.ProjectID,
		Mode:       mode,
		TaskNumber: taskNumber,
	})
	if err != nil {
		return fmt.Errorf("failed to start agent: %w", err)
	}

	// Print startup message
	switch mode {
	case "start-all":
		fmt.Printf("Agent started for %s (start-all mode — starting with task #%04d)\n", status.ProjectName, status.TaskNumber)
	case "wildfire":
		if status.WildfirePhase != "" {
			fmt.Printf("Agent started for %s (🔥 wildfire mode — %s phase", status.ProjectName, status.WildfirePhase)
			if status.TaskNumber > 0 {
				fmt.Printf(", task #%04d", status.TaskNumber)
			}
			fmt.Printf(")\n")
		} else {
			fmt.Printf("Agent started for %s (🔥 wildfire mode — starting with task #%04d)\n", status.ProjectName, status.TaskNumber)
		}
	case "task":
		fmt.Printf("Agent started for %s (task #%04d)\n", status.ProjectName, taskNumber)
	case "generate-definition":
		fmt.Printf("Agent started for %s (generating project definition)\n", status.ProjectName)
	case "generate-tasks":
		fmt.Printf("Agent started for %s (generating tasks)\n", status.ProjectName)
	default:
		fmt.Printf("Agent started for %s (chat mode)\n", status.ProjectName)
	}

	// Get terminal size and send resize
	cols, rows, err := term.GetSize(int(os.Stdin.Fd()))
	if err == nil {
		_, _ = client.Resize(ctx, &pb.ResizeRequest{
			ProjectId: project.ProjectID,
			Rows:      int32(rows),
			Cols:      int32(cols),
		})
	}

	// Subscribe to raw output stream
	stream, err := client.SubscribeRawOutput(ctx, &pb.SubscribeRawOutputRequest{
		ProjectId: project.ProjectID,
	})
	if err != nil {
		return fmt.Errorf("failed to subscribe to output: %w", err)
	}

	// Put terminal into raw mode
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("failed to set raw terminal: %w", err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	// Handle SIGWINCH (window resize)
	sigwinchCh := make(chan os.Signal, 1)
	signal.Notify(sigwinchCh, syscall.SIGWINCH)
	go func() {
		for range sigwinchCh {
			cols, rows, err := term.GetSize(int(os.Stdin.Fd()))
			if err == nil {
				_, _ = client.Resize(ctx, &pb.ResizeRequest{
					ProjectId: project.ProjectID,
					Rows:      int32(rows),
					Cols:      int32(cols),
				})
			}
		}
	}()

	// Handle SIGINT — forward Ctrl+C to agent process
	sigintCh := make(chan os.Signal, 1)
	signal.Notify(sigintCh, syscall.SIGINT)
	go func() {
		<-sigintCh
		_, _ = client.SendInput(ctx, &pb.SendInputRequest{
			ProjectId: project.ProjectID,
			Data:      []byte{3}, // Ctrl+C
		})
	}()

	// Input goroutine: read from stdin and send to agent
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				_, sendErr := client.SendInput(ctx, &pb.SendInputRequest{
					ProjectId: project.ProjectID,
					Data:      data,
				})
				if sendErr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Main loop: receive raw output and write to stdout
	for {
		chunk, err := stream.Recv()
		if err != nil {
			if err == io.EOF || ctx.Err() != nil {
				// Stream ended
			} else {
				term.Restore(int(os.Stdin.Fd()), oldState)
				return fmt.Errorf("stream error: %w", err)
			}

			// Non-chaining modes: done
			if mode != "wildfire" && mode != "start-all" {
				break
			}

			// Chaining modes: poll for next task starting, then re-subscribe
			os.Stdout.Write([]byte("\r\n--- Task complete. Starting next task... ---\r\n"))

			nextRunning := false
			for i := 0; i < 25; i++ { // up to 5s at 200ms intervals
				time.Sleep(200 * time.Millisecond)
				agentStatus, err := client.GetAgentStatus(ctx, &pb.ProjectId{
					ProjectId: project.ProjectID,
				})
				if err != nil {
					break
				}
				if agentStatus.IsRunning {
					if mode == "wildfire" {
						switch agentStatus.WildfirePhase {
						case "execute":
							os.Stdout.Write([]byte(fmt.Sprintf("\r\n--- 🔥 Wildfire Execute: task #%04d ---\r\n", agentStatus.TaskNumber)))
						case "refine":
							os.Stdout.Write([]byte(fmt.Sprintf("\r\n--- 🔥 Wildfire Refine: task #%04d ---\r\n", agentStatus.TaskNumber)))
						case "generate":
							os.Stdout.Write([]byte("\r\n--- 🔥 Wildfire Generate: analyzing project... ---\r\n"))
						default:
							os.Stdout.Write([]byte(fmt.Sprintf("\r\n--- 🔥 Wildfire: task #%04d ---\r\n", agentStatus.TaskNumber)))
						}
						// If daemon transitioned to chat mode, wildfire is complete
						if agentStatus.Mode == "chat" {
							os.Stdout.Write([]byte("\r\n--- 🔥 Wildfire complete: best version achieved. Entering chat mode. ---\r\n"))
						}
					} else {
						os.Stdout.Write([]byte(fmt.Sprintf("\r\n--- Start-all: task #%04d ---\r\n", agentStatus.TaskNumber)))
					}
					nextRunning = true
					break
				}
			}

			if !nextRunning {
				if mode == "start-all" {
					os.Stdout.Write([]byte("\r\n--- Start-all complete: all ready tasks done ---\r\n"))
				} else {
					os.Stdout.Write([]byte("\r\n--- 🔥 Wildfire complete: all tasks done ---\r\n"))
				}
				break
			}

			// Re-subscribe to the new agent's raw output
			stream, err = client.SubscribeRawOutput(ctx, &pb.SubscribeRawOutputRequest{
				ProjectId: project.ProjectID,
			})
			if err != nil {
				term.Restore(int(os.Stdin.Fd()), oldState)
				return fmt.Errorf("failed to re-subscribe to output: %w", err)
			}
			continue
		}
		os.Stdout.Write(chunk.Data)
	}

	return nil
}

func runAgentGenerateDef(cmd *cobra.Command, args []string) error {
	projectPath, err := getProjectPath()
	if err != nil {
		return err
	}

	return runAgentAttach(projectPath, "generate-definition", 0)
}

func runAgentGenerateTasks(cmd *cobra.Command, args []string) error {
	projectPath, err := getProjectPath()
	if err != nil {
		return err
	}

	return runAgentAttach(projectPath, "generate-tasks", 0)
}
