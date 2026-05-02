package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/watchfire-io/watchfire/internal/config"
	pb "github.com/watchfire-io/watchfire/proto"
)

func connectDaemonCmd() tea.Cmd {
	return func() tea.Msg {
		info, err := config.LoadDaemonInfo()
		if err != nil || info == nil {
			return ErrorMsg{Err: fmt.Errorf("daemon not running")}
		}

		addr := fmt.Sprintf("%s:%d", info.Host, info.Port)
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return ErrorMsg{Err: fmt.Errorf("failed to connect to daemon: %w", err)}
		}

		return DaemonConnectedMsg{Conn: conn}
	}
}

func loadProjectCmd(conn *grpc.ClientConn, projectID string) tea.Cmd {
	return func() tea.Msg {
		client := pb.NewProjectServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		project, err := client.GetProject(ctx, &pb.ProjectId{ProjectId: projectID})
		if err != nil {
			return ErrorMsg{Err: fmt.Errorf("failed to load project: %w", err)}
		}
		return ProjectLoadedMsg{Project: project}
	}
}

func loadTasksCmd(conn *grpc.ClientConn, projectID string) tea.Cmd {
	return func() tea.Msg {
		client := pb.NewTaskServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		resp, err := client.ListTasks(ctx, &pb.ListTasksRequest{
			ProjectId: projectID,
		})
		if err != nil {
			return ErrorMsg{Err: fmt.Errorf("failed to load tasks: %w", err)}
		}
		return TasksLoadedMsg{Tasks: resp.Tasks}
	}
}

func getAgentStatusCmd(conn *grpc.ClientConn, projectID string) tea.Cmd {
	return func() tea.Msg {
		client := pb.NewAgentServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		resp, err := client.GetAgentStatus(ctx, &pb.ProjectId{ProjectId: projectID})
		if err != nil {
			if isConnectionLost(err) {
				return DaemonDisconnectedMsg{}
			}
			return AgentStatusMsg{Status: nil}
		}
		return AgentStatusMsg{Status: resp}
	}
}

func startAgentCmd(conn *grpc.ClientConn, projectID, mode string, taskNumber int32, rows, cols int) tea.Cmd {
	return func() tea.Msg {
		client := pb.NewAgentServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()

		status, err := client.StartAgent(ctx, &pb.StartAgentRequest{
			ProjectId:  projectID,
			Mode:       mode,
			TaskNumber: taskNumber,
			Rows:       int32(rows),
			Cols:       int32(cols),
		})
		if err != nil {
			return ErrorMsg{Err: fmt.Errorf("failed to start agent: %w", err)}
		}
		return AgentStartedMsg{Status: status}
	}
}

func resumeAgentCmd(conn *grpc.ClientConn, projectID string) tea.Cmd {
	return func() tea.Msg {
		client := pb.NewAgentServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		status, err := client.ResumeAgent(ctx, &pb.ProjectId{ProjectId: projectID})
		if err != nil {
			return ErrorMsg{Err: fmt.Errorf("failed to resume agent: %w", err)}
		}
		return AgentStatusMsg{Status: status}
	}
}

func subscribeScreenCmd(ctx context.Context, conn *grpc.ClientConn, projectID string, rows, cols int, program *programRef) tea.Cmd {
	return func() tea.Msg {
		client := pb.NewAgentServiceClient(conn)

		// Resize daemon terminal to match TUI viewport before subscribing
		// so the initial snapshot arrives at the correct size.
		// After resize, sleep briefly to let the agent re-render in
		// response to SIGWINCH before we take the snapshot.
		if rows > 0 && cols > 0 {
			resizeCtx, resizeCancel := context.WithTimeout(ctx, 2*time.Second)
			_, _ = client.Resize(resizeCtx, &pb.ResizeRequest{
				ProjectId: projectID,
				Rows:      int32(rows),
				Cols:      int32(cols),
			})
			resizeCancel()
			time.Sleep(150 * time.Millisecond)
		}

		stream, err := client.SubscribeScreen(ctx, &pb.SubscribeScreenRequest{
			ProjectId: projectID,
		})
		if err != nil {
			return ErrorMsg{Err: fmt.Errorf("failed to subscribe to screen: %w", err)}
		}

		go func() {
			for {
				buf, err := stream.Recv()
				if err != nil {
					if isConnectionLost(err) {
						program.Send(DaemonDisconnectedMsg{})
					} else {
						program.Send(ScreenEndedMsg{})
					}
					return
				}
				program.Send(ScreenUpdateMsg{AnsiContent: buf.AnsiContent})
			}
		}()

		return nil
	}
}

func subscribeAgentIssuesCmd(ctx context.Context, conn *grpc.ClientConn, projectID string, program *programRef) tea.Cmd {
	return func() tea.Msg {
		client := pb.NewAgentServiceClient(conn)
		stream, err := client.SubscribeAgentIssues(ctx, &pb.SubscribeAgentIssuesRequest{
			ProjectId: projectID,
		})
		if err != nil {
			return nil // Non-critical
		}

		go func() {
			for {
				issue, err := stream.Recv()
				if err != nil {
					return
				}
				program.Send(AgentIssueMsg{Issue: issue})
			}
		}()

		return nil
	}
}

func stopAgentCmd(conn *grpc.ClientConn, projectID string) tea.Cmd {
	return func() tea.Msg {
		client := pb.NewAgentServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		_, err := client.StopAgent(ctx, &pb.ProjectId{ProjectId: projectID})
		if err != nil {
			return ErrorMsg{Err: fmt.Errorf("failed to stop agent: %w", err)}
		}
		return AgentStoppedMsg{}
	}
}

func sendInputCmd(conn *grpc.ClientConn, projectID string, data []byte) tea.Cmd {
	return func() tea.Msg {
		client := pb.NewAgentServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		_, _ = client.SendInput(ctx, &pb.SendInputRequest{
			ProjectId: projectID,
			Data:      data,
		})
		return nil
	}
}

func resizeAgentCmd(conn *grpc.ClientConn, projectID string, rows, cols int) tea.Cmd {
	return func() tea.Msg {
		client := pb.NewAgentServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		_, _ = client.Resize(ctx, &pb.ResizeRequest{
			ProjectId: projectID,
			Rows:      int32(rows),
			Cols:      int32(cols),
		})
		return nil
	}
}

func createTaskCmd(conn *grpc.ClientConn, projectID, title, prompt, criteria, status, agent string) tea.Cmd {
	return func() tea.Msg {
		client := pb.NewTaskServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		req := &pb.CreateTaskRequest{
			ProjectId: projectID,
			Title:     title,
			Prompt:    prompt,
			Status:    status,
		}
		if criteria != "" {
			req.AcceptanceCriteria = &criteria
		}
		if agent != "" {
			req.Agent = &agent
		}

		task, err := client.CreateTask(ctx, req)
		if err != nil {
			return ErrorMsg{Err: fmt.Errorf("failed to create task: %w", err)}
		}
		return TaskSavedMsg{Task: task}
	}
}

func updateTaskCmd(conn *grpc.ClientConn, projectID string, taskNumber int32, updates map[string]interface{}) tea.Cmd {
	return func() tea.Msg {
		client := pb.NewTaskServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		req := &pb.UpdateTaskRequest{
			ProjectId:  projectID,
			TaskNumber: taskNumber,
		}

		if v, ok := updates["title"].(string); ok {
			req.Title = &v
		}
		if v, ok := updates["prompt"].(string); ok {
			req.Prompt = &v
		}
		if v, ok := updates["criteria"].(string); ok {
			req.AcceptanceCriteria = &v
		}
		if v, ok := updates["status"].(string); ok {
			req.Status = &v
		}
		if v, ok := updates["success"].(bool); ok {
			req.Success = &v
		}
		if v, ok := updates["agent"].(string); ok {
			req.Agent = &v
		}

		task, err := client.UpdateTask(ctx, req)
		if err != nil {
			return ErrorMsg{Err: fmt.Errorf("failed to update task: %w", err)}
		}
		return TaskSavedMsg{Task: task}
	}
}

func deleteTaskCmd(conn *grpc.ClientConn, projectID string, taskNumber int32) tea.Cmd {
	return func() tea.Msg {
		client := pb.NewTaskServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := client.DeleteTask(ctx, &pb.TaskId{
			ProjectId:  projectID,
			TaskNumber: taskNumber,
		})
		if err != nil {
			return ErrorMsg{Err: fmt.Errorf("failed to delete task: %w", err)}
		}
		return TaskDeletedMsg{}
	}
}

func updateProjectCmd(conn *grpc.ClientConn, projectID string, updates map[string]interface{}) tea.Cmd {
	return func() tea.Msg {
		client := pb.NewProjectServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		req := &pb.UpdateProjectRequest{
			ProjectId: projectID,
		}

		if v, ok := updates["name"].(string); ok {
			req.Name = &v
		}
		if v, ok := updates["color"].(string); ok {
			req.Color = &v
		}
		if v, ok := updates["default_agent"].(string); ok {
			req.DefaultAgent = &v
		}
		if v, ok := updates["definition"].(string); ok {
			req.Definition = &v
		}
		if v, ok := updates["auto_merge"].(bool); ok {
			req.AutoMerge = &v
		}
		if v, ok := updates["auto_delete_branch"].(bool); ok {
			req.AutoDeleteBranch = &v
		}
		if v, ok := updates["auto_start_tasks"].(bool); ok {
			req.AutoStartTasks = &v
		}
		if v, ok := updates["notifications_muted"].(bool); ok {
			req.NotificationsMuted = &v
		}

		project, err := client.UpdateProject(ctx, req)
		if err != nil {
			return ErrorMsg{Err: fmt.Errorf("failed to update project: %w", err)}
		}
		return ProjectSavedMsg{Project: project}
	}
}

func listLogsCmd(conn *grpc.ClientConn, projectID string) tea.Cmd {
	return func() tea.Msg {
		client := pb.NewLogServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		resp, err := client.ListLogs(ctx, &pb.ListLogsRequest{
			ProjectId: projectID,
		})
		if err != nil {
			return ErrorMsg{Err: fmt.Errorf("failed to list logs: %w", err)}
		}
		return LogsLoadedMsg{Logs: resp.Logs}
	}
}

func getLogCmd(conn *grpc.ClientConn, projectID, logID string) tea.Cmd {
	return func() tea.Msg {
		client := pb.NewLogServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		resp, err := client.GetLog(ctx, &pb.GetLogRequest{
			ProjectId: projectID,
			LogId:     logID,
		})
		if err != nil {
			return ErrorMsg{Err: fmt.Errorf("failed to get log: %w", err)}
		}
		return LogContentMsg{Entry: resp.Entry, Content: resp.Content}
	}
}

func deleteLogCmd(conn *grpc.ClientConn, projectID, logID string) tea.Cmd {
	return func() tea.Msg {
		client := pb.NewLogServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := client.DeleteLog(ctx, &pb.DeleteLogRequest{
			ProjectId: projectID,
			LogId:     logID,
		})
		return LogDeletedMsg{LogID: logID, Err: err}
	}
}

func spinnerTick() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(_ time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

func pollAgentStatusTick() tea.Cmd {
	return tea.Tick(2*time.Second, func(_ time.Time) tea.Msg {
		return TickMsg{}
	})
}

func clearErrorAfter(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(_ time.Time) tea.Msg {
		return ClearErrorMsg{}
	})
}

func clearSavedAfter(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(_ time.Time) tea.Msg {
		return ClearSavedMsg{}
	})
}

func reconnectTick() tea.Cmd {
	return tea.Tick(3*time.Second, func(_ time.Time) tea.Msg {
		return ReconnectMsg{}
	})
}

func getSettingsCmd(conn *grpc.ClientConn) tea.Cmd {
	return func() tea.Msg {
		client := pb.NewSettingsServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		settings, err := client.GetSettings(ctx, &emptypb.Empty{})
		if err != nil {
			return ErrorMsg{Err: fmt.Errorf("failed to load settings: %w", err)}
		}
		return SettingsLoadedMsg{Settings: settings}
	}
}

// updateGlobalSettingsCmd sends an UpdateSettings RPC. defaultAgent is a
// pointer so callers can omit it; when non-nil (including empty) the defaults
// block is sent. agents carries a merge of agent path edits; nil means no
// change. notifications, when non-nil, replaces the notifications block in
// defaults; the daemon validates it before persisting and rolls back on
// malformed quiet-hours values. Any of these nil → no change for that field.
func updateGlobalSettingsCmd(conn *grpc.ClientConn, defaultAgent *string, agents map[string]string, notifications *pb.NotificationsConfig) tea.Cmd {
	return func() tea.Msg {
		client := pb.NewSettingsServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		req := &pb.UpdateSettingsRequest{}
		if defaultAgent != nil || notifications != nil {
			// Load current defaults so we don't zero out the other fields.
			cur, err := client.GetSettings(ctx, &emptypb.Empty{})
			if err != nil {
				return ErrorMsg{Err: fmt.Errorf("failed to load settings: %w", err)}
			}
			d := cur.Defaults
			if d == nil {
				d = &pb.DefaultsConfig{}
			}
			if defaultAgent != nil {
				d.DefaultAgent = *defaultAgent
			}
			if notifications != nil {
				d.Notifications = notifications
			}
			req.Defaults = d
		}
		if agents != nil {
			req.Agents = make(map[string]*pb.AgentConfig, len(agents))
			for name, path := range agents {
				req.Agents[name] = &pb.AgentConfig{Path: path}
			}
		}

		settings, err := client.UpdateSettings(ctx, req)
		if err != nil {
			return ErrorMsg{Err: fmt.Errorf("failed to save settings: %w", err)}
		}
		return SettingsSavedMsg{Settings: settings}
	}
}

func fetchGitInfoCmd(conn *grpc.ClientConn, projectID string) tea.Cmd {
	return func() tea.Msg {
		client := pb.NewProjectServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		info, err := client.GetGitInfo(ctx, &pb.ProjectId{ProjectId: projectID})
		if err != nil {
			return nil // Non-critical
		}
		return GitInfoMsg{Info: info}
	}
}

func checkDaemonUpdateCmd(conn *grpc.ClientConn) tea.Cmd {
	return func() tea.Msg {
		client := pb.NewDaemonServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		resp, err := client.GetStatus(ctx, &emptypb.Empty{})
		if err != nil {
			return nil
		}
		if resp.UpdateAvailable {
			return UpdateAvailableMsg{Version: resp.UpdateVersion}
		}
		return nil
	}
}

// isConnectionLost checks if a gRPC error indicates the server is gone.
func isConnectionLost(err error) bool {
	code := status.Code(err)
	return code == codes.Unavailable || code == codes.Canceled
}
