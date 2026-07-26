package mcpserver

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/watchfire-io/watchfire/proto"
)

var projectTools = []toolDef{
	newTool(toolSpec{
		Group: groupProject, Name: "list_projects", Title: "List projects",
		ReadOnly: true, Idempotent: true,
		Description: "List every Watchfire project registered on this machine, each enriched with live agent status: whether an agent is running, in which mode (chat, task, start-all, wildfire), the task it is working on, and the wildfire phase if any. Start here: this is how you discover the ids and names accepted by the \"project\" argument of every other tool.",
	}, handleListProjects),
	newTool(toolSpec{
		Group: groupProject, Name: "get_project", Title: "Get project",
		ReadOnly: true, Idempotent: true,
		Description: "Get full detail for one Watchfire project: definition (the project spec agents work from), default agent and sandbox/merge settings, task counts by status, git state (current branch, dirty files, ahead/behind), and live agent status. \"project\" is a project id or name; omit it to use the project of the directory the server was started in.",
	}, handleGetProject),
}

type listProjectsArgs struct{}

type getProjectArgs struct {
	Project string `json:"project,omitempty" jsonschema:"Project id or name (see list_projects). Optional when the server runs inside a registered project directory."`
}

// agentSummary is the live AgentService.GetAgentStatus view of a project.
type agentSummary struct {
	Running       bool   `json:"running"`
	Mode          string `json:"mode,omitempty"`
	TaskNumber    int32  `json:"task_number,omitempty"`
	TaskTitle     string `json:"task_title,omitempty"`
	WildfirePhase string `json:"wildfire_phase,omitempty"`
	StartedAt     string `json:"started_at,omitempty"`
	IssueType     string `json:"issue_type,omitempty"`
	IssueMessage  string `json:"issue_message,omitempty"`
}

type projectSummary struct {
	ProjectID      string        `json:"project_id"`
	Name           string        `json:"name"`
	Path           string        `json:"path"`
	Status         string        `json:"status"`
	DefaultAgent   string        `json:"default_agent"`
	AutoStartTasks bool          `json:"auto_start_tasks"`
	Agent          *agentSummary `json:"agent,omitempty"`
	AgentError     string        `json:"agent_status_error,omitempty"`
}

type taskCounts struct {
	Total     int `json:"total"`
	Draft     int `json:"draft"`
	Ready     int `json:"ready"`
	Done      int `json:"done"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
}

type gitSummary struct {
	CurrentBranch    string `json:"current_branch"`
	RemoteURL        string `json:"remote_url,omitempty"`
	IsDirty          bool   `json:"is_dirty"`
	UncommittedCount int32  `json:"uncommitted_count"`
	Ahead            int32  `json:"ahead"`
	Behind           int32  `json:"behind"`
}

type projectDetail struct {
	ProjectID        string        `json:"project_id"`
	Name             string        `json:"name"`
	Path             string        `json:"path"`
	Status           string        `json:"status"`
	DefaultAgent     string        `json:"default_agent"`
	Sandbox          string        `json:"sandbox"`
	AutoMerge        bool          `json:"auto_merge"`
	AutoDeleteBranch bool          `json:"auto_delete_branch"`
	AutoStartTasks   bool          `json:"auto_start_tasks"`
	NextTaskNumber   int32         `json:"next_task_number"`
	Definition       string        `json:"definition"`
	CreatedAt        string        `json:"created_at,omitempty"`
	UpdatedAt        string        `json:"updated_at,omitempty"`
	Tasks            *taskCounts   `json:"tasks,omitempty"`
	TasksError       string        `json:"tasks_error,omitempty"`
	Git              *gitSummary   `json:"git,omitempty"`
	GitError         string        `json:"git_error,omitempty"`
	Agent            *agentSummary `json:"agent,omitempty"`
	AgentError       string        `json:"agent_status_error,omitempty"`
}

func handleListProjects(ctx context.Context, s *server, _ listProjectsArgs) (any, error) {
	list, err := s.projects.ListProjects(ctx, &emptypb.Empty{})
	if err != nil {
		return nil, rpcErr("list projects", err)
	}

	summaries := make([]projectSummary, 0, len(list.Projects))
	for _, p := range list.Projects {
		sum := projectSummary{
			ProjectID:      p.ProjectId,
			Name:           p.Name,
			Path:           p.Path,
			Status:         p.Status,
			DefaultAgent:   p.DefaultAgent,
			AutoStartTasks: p.AutoStartTasks,
		}
		sum.Agent, sum.AgentError = fetchAgentSummary(ctx, s, p.ProjectId)
		summaries = append(summaries, sum)
	}

	return struct {
		Projects []projectSummary `json:"projects"`
	}{summaries}, nil
}

func handleGetProject(ctx context.Context, s *server, args getProjectArgs) (any, error) {
	projectID, err := s.resolveProject(ctx, args.Project)
	if err != nil {
		return nil, err
	}

	project, err := s.projects.GetProject(ctx, &pb.ProjectId{ProjectId: projectID})
	if err != nil {
		return nil, rpcErr(fmt.Sprintf("get project %s", projectID), err)
	}

	detail := projectDetail{
		ProjectID:        project.ProjectId,
		Name:             project.Name,
		Path:             project.Path,
		Status:           project.Status,
		DefaultAgent:     project.DefaultAgent,
		Sandbox:          project.Sandbox,
		AutoMerge:        project.AutoMerge,
		AutoDeleteBranch: project.AutoDeleteBranch,
		AutoStartTasks:   project.AutoStartTasks,
		NextTaskNumber:   project.NextTaskNumber,
		Definition:       project.Definition,
		CreatedAt:        formatTimestamp(project.CreatedAt),
		UpdatedAt:        formatTimestamp(project.UpdatedAt),
	}

	if tasks, err := s.tasks.ListTasks(ctx, &pb.ListTasksRequest{ProjectId: projectID}); err != nil {
		detail.TasksError = err.Error()
	} else {
		detail.Tasks = countTasks(tasks.Tasks)
	}

	if git, err := s.projects.GetGitInfo(ctx, &pb.ProjectId{ProjectId: projectID}); err != nil {
		detail.GitError = err.Error()
	} else {
		detail.Git = &gitSummary{
			CurrentBranch:    git.CurrentBranch,
			RemoteURL:        git.RemoteUrl,
			IsDirty:          git.IsDirty,
			UncommittedCount: git.UncommittedCount,
			Ahead:            git.Ahead,
			Behind:           git.Behind,
		}
	}

	detail.Agent, detail.AgentError = fetchAgentSummary(ctx, s, projectID)

	return detail, nil
}

// fetchAgentSummary loads live agent status for a project; failures are
// reported alongside the rest of the result instead of failing the tool.
func fetchAgentSummary(ctx context.Context, s *server, projectID string) (*agentSummary, string) {
	status, err := s.agents.GetAgentStatus(ctx, &pb.ProjectId{ProjectId: projectID})
	if err != nil {
		return nil, err.Error()
	}
	sum := &agentSummary{
		Running:       status.IsRunning,
		Mode:          status.Mode,
		TaskNumber:    status.TaskNumber,
		TaskTitle:     status.TaskTitle,
		WildfirePhase: status.WildfirePhase,
		StartedAt:     formatTimestamp(status.StartedAt),
	}
	if status.Issue != nil {
		sum.IssueType = status.Issue.IssueType
		sum.IssueMessage = status.Issue.Message
	}
	return sum, ""
}

func countTasks(tasks []*pb.Task) *taskCounts {
	counts := &taskCounts{Total: len(tasks)}
	for _, t := range tasks {
		switch t.Status {
		case "draft":
			counts.Draft++
		case "ready":
			counts.Ready++
		case "done":
			counts.Done++
			if t.Success != nil && *t.Success {
				counts.Succeeded++
			} else {
				counts.Failed++
			}
		}
	}
	return counts
}

func formatTimestamp(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return ""
	}
	return ts.AsTime().Format(time.RFC3339)
}
