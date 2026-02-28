package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/watchfire-io/watchfire/internal/config"
	pb "github.com/watchfire-io/watchfire/proto"
)

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

	printStartupMessage(mode, status, taskNumber)

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
	oldState, err := setupRawTerminal()
	if err != nil {
		return err
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	// Handle SIGWINCH (window resize)
	go watchWindowResize(ctx, client, project.ProjectID)

	// userStopped tracks whether the user explicitly stopped the agent (Ctrl+C / SIGINT)
	// in wildfire/start-all modes, so the CLI can break out of the re-subscribe loop.
	isChaining := mode == "wildfire" || mode == "start-all"
	userStopped := make(chan struct{})
	var userStoppedOnce sync.Once

	// Handle SIGINT
	go handleSIGINT(ctx, client, project.ProjectID, isChaining, userStopped, &userStoppedOnce)

	// Input goroutine: read from stdin and send to agent.
	go streamInput(ctx, client, project.ProjectID, isChaining, userStopped, &userStoppedOnce)

	// Main loop: receive raw output and write to stdout
	return streamOutput(ctx, stream, client, project.ProjectID, mode, isChaining, userStopped, oldState)
}

// printStartupMessage prints the agent startup message based on mode.
func printStartupMessage(mode string, status *pb.AgentStatus, taskNumber int32) {
	switch mode {
	case "start-all":
		fmt.Printf("Agent started for %s (start-all mode — starting with task #%04d)\n", status.ProjectName, status.TaskNumber)
	case "wildfire":
		if status.WildfirePhase != "" {
			fmt.Printf("Agent started for %s (wildfire mode — %s phase", status.ProjectName, status.WildfirePhase)
			if status.TaskNumber > 0 {
				fmt.Printf(", task #%04d", status.TaskNumber)
			}
			fmt.Printf(")\n")
		} else {
			fmt.Printf("Agent started for %s (wildfire mode — starting with task #%04d)\n", status.ProjectName, status.TaskNumber)
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
}

// setupRawTerminal puts the terminal into raw mode and returns the previous state.
func setupRawTerminal() (*term.State, error) {
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return nil, fmt.Errorf("failed to set raw terminal: %w", err)
	}
	return oldState, nil
}

// watchWindowResize handles SIGWINCH signals and sends resize requests.
func watchWindowResize(ctx context.Context, client pb.AgentServiceClient, projectID string) {
	sigwinchCh := make(chan os.Signal, 1)
	signal.Notify(sigwinchCh, syscall.SIGWINCH)
	for range sigwinchCh {
		cols, rows, err := term.GetSize(int(os.Stdin.Fd()))
		if err == nil {
			_, _ = client.Resize(ctx, &pb.ResizeRequest{
				ProjectId: projectID,
				Rows:      int32(rows),
				Cols:      int32(cols),
			})
		}
	}
}

// handleSIGINT handles SIGINT in chaining and non-chaining modes.
func handleSIGINT(ctx context.Context, client pb.AgentServiceClient, projectID string, isChaining bool, userStopped chan struct{}, userStoppedOnce *sync.Once) {
	sigintCh := make(chan os.Signal, 1)
	signal.Notify(sigintCh, syscall.SIGINT)
	<-sigintCh
	if isChaining {
		userStoppedOnce.Do(func() { close(userStopped) })
		_, _ = client.StopAgent(ctx, &pb.ProjectId{ProjectId: projectID})
	} else {
		_, _ = client.SendInput(ctx, &pb.SendInputRequest{
			ProjectId: projectID,
			Data:      []byte{3}, // Ctrl+C
		})
	}
}

// streamInput reads from stdin and sends to the agent.
func streamInput(ctx context.Context, client pb.AgentServiceClient, projectID string, isChaining bool, userStopped chan struct{}, userStoppedOnce *sync.Once) {
	buf := make([]byte, 1024)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])

			// In chaining modes, intercept lone Ctrl+C byte
			if isChaining && n == 1 && data[0] == 3 {
				userStoppedOnce.Do(func() { close(userStopped) })
				_, _ = client.StopAgent(ctx, &pb.ProjectId{ProjectId: projectID})
				return
			}

			_, sendErr := client.SendInput(ctx, &pb.SendInputRequest{
				ProjectId: projectID,
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
}

// streamOutput reads raw output from the stream and writes to stdout.
// Handles chaining (re-subscription) for wildfire/start-all modes.
func streamOutput(ctx context.Context, stream pb.AgentService_SubscribeRawOutputClient, client pb.AgentServiceClient, projectID, mode string, isChaining bool, userStopped chan struct{}, oldState *term.State) error {
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
			if !isChaining {
				break
			}

			cmd, newStream := handleChaining(ctx, client, projectID, mode, userStopped, oldState)
			if cmd == "break" {
				break
			}
			if cmd == "continue" {
				stream = newStream
				continue
			}
			break
		}
		os.Stdout.Write(chunk.Data)
	}

	return nil
}

// handleChaining handles the re-subscription logic for wildfire/start-all modes.
// Returns "break" to exit, "continue" with a new stream to re-subscribe, or "break" on error.
func handleChaining(ctx context.Context, client pb.AgentServiceClient, projectID, mode string, userStopped chan struct{}, oldState *term.State) (string, pb.AgentService_SubscribeRawOutputClient) {
	// If user explicitly stopped, don't wait for next task
	select {
	case <-userStopped:
		printStoppedMessage(mode)
		return "break", nil
	default:
	}

	// Chaining modes: poll for next task starting, then re-subscribe
	os.Stdout.Write([]byte("\r\n--- Task complete. Starting next task... ---\r\n"))

	nextRunning := false
	for i := 0; i < 25; i++ { // up to 5s at 200ms intervals
		time.Sleep(200 * time.Millisecond)

		// Check if user stopped during polling
		select {
		case <-userStopped:
			printStoppedMessage(mode)
			return "break", nil
		default:
		}

		agentStatus, err := client.GetAgentStatus(ctx, &pb.ProjectId{
			ProjectId: projectID,
		})
		if err != nil {
			break
		}
		if agentStatus.IsRunning {
			printChainingStatus(mode, agentStatus)
			nextRunning = true
			break
		}
	}

	if !nextRunning {
		if mode == "start-all" {
			os.Stdout.Write([]byte("\r\n--- Start-all complete: all ready tasks done ---\r\n"))
		} else {
			os.Stdout.Write([]byte("\r\n--- Wildfire complete: all tasks done ---\r\n"))
		}
		return "break", nil
	}

	// Re-subscribe to the new agent's raw output
	stream, err := client.SubscribeRawOutput(ctx, &pb.SubscribeRawOutputRequest{
		ProjectId: projectID,
	})
	if err != nil {
		term.Restore(int(os.Stdin.Fd()), oldState)
		return "break", nil
	}
	return "continue", stream
}

func printStoppedMessage(mode string) {
	if mode == "wildfire" {
		os.Stdout.Write([]byte("\r\n--- Wildfire stopped by user ---\r\n"))
	} else {
		os.Stdout.Write([]byte("\r\n--- Start-all stopped by user ---\r\n"))
	}
}

func printChainingStatus(mode string, agentStatus *pb.AgentStatus) {
	if mode == "wildfire" {
		switch agentStatus.WildfirePhase {
		case "execute":
			os.Stdout.Write([]byte(fmt.Sprintf("\r\n--- Wildfire Execute: task #%04d ---\r\n", agentStatus.TaskNumber)))
		case "refine":
			os.Stdout.Write([]byte(fmt.Sprintf("\r\n--- Wildfire Refine: task #%04d ---\r\n", agentStatus.TaskNumber)))
		case "generate":
			os.Stdout.Write([]byte("\r\n--- Wildfire Generate: analyzing project... ---\r\n"))
		default:
			os.Stdout.Write([]byte(fmt.Sprintf("\r\n--- Wildfire: task #%04d ---\r\n", agentStatus.TaskNumber)))
		}
		// If daemon transitioned to chat mode, wildfire is complete
		if agentStatus.Mode == "chat" {
			os.Stdout.Write([]byte("\r\n--- Wildfire complete: best version achieved. Entering chat mode. ---\r\n"))
		}
	} else {
		os.Stdout.Write([]byte(fmt.Sprintf("\r\n--- Start-all: task #%04d ---\r\n", agentStatus.TaskNumber)))
	}
}
