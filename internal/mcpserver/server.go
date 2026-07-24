// Package mcpserver implements the Watchfire MCP server (`watchfire mcp
// serve`): a stateless stdio facade over the local daemon. Every tool call
// translates to an existing daemon gRPC RPC — no orchestration logic lives
// here. The package imports only the generated proto client,
// internal/config, and exported internal/cli helpers.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	pb "github.com/watchfire-io/watchfire/proto"
)

// Tool groups. --read-only serving (task 0126) filters registration by group.
const (
	groupProject = "project"
	groupTask    = "task"
	groupRun     = "run"
	groupInspect = "inspect"
)

// Options configures a Serve run.
type Options struct {
	// ReadOnly registers only observation tools. Reserved: the filtering
	// plumbing lands with the inspect tools (task 0126).
	ReadOnly bool
	// Version is the client version reported in the MCP handshake.
	Version string
}

// server holds the daemon connection and per-run state shared by all tool
// handlers. It is stateless beyond the connection and the default project:
// concurrent tool calls are safe because the daemon serializes per-project
// agent operations.
type server struct {
	projects pb.ProjectServiceClient
	tasks    pb.TaskServiceClient
	agents   pb.AgentServiceClient
	settings pb.SettingsServiceClient

	// defaultProjectID is the project of the directory the server was
	// started in, or "" when started outside a registered project.
	defaultProjectID string

	// pollInterval is the wait_for_task polling cadence; 0 means the
	// 2-second default (tests shorten it).
	pollInterval time.Duration

	readOnly bool
}

// toolDef is one row of the tool registry: name, description, and input
// schema are carried by the closed-over *mcp.Tool; group drives --read-only
// filtering. Later tasks add tools by appending rows in allTools.
type toolDef struct {
	group    string
	name     string
	register func(m *mcp.Server, s *server)
}

// newTool builds a registry row from a typed handler. The input schema is
// inferred from In (jsonschema struct tags become property descriptions) and
// arguments are validated by the SDK before the handler runs. The handler's
// return value is rendered as JSON text content; an error becomes a tool
// error (IsError result), not a protocol error.
//
// An optional customize function post-processes the inferred schema for
// constraints struct tags cannot express (enums, defaults).
func newTool[In any](group, name, description string, handler func(context.Context, *server, In) (any, error), customize ...func(*jsonschema.Schema)) toolDef {
	return toolDef{
		group: group,
		name:  name,
		register: func(m *mcp.Server, s *server) {
			tool := &mcp.Tool{Name: name, Description: description}
			if len(customize) > 0 {
				schema, err := jsonschema.For[In](nil)
				if err != nil {
					panic(fmt.Sprintf("tool %q: infer input schema: %v", name, err))
				}
				for _, c := range customize {
					c(schema)
				}
				tool.InputSchema = schema
			}
			mcp.AddTool(m, tool,
				func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
					out, err := handler(ctx, s, in)
					if err != nil {
						return nil, nil, err
					}
					return jsonResult(out)
				})
		},
	}
}

// allTools is the full registry. Task 0126 only appends its group here.
func allTools() []toolDef {
	var defs []toolDef
	defs = append(defs, projectTools...)
	defs = append(defs, taskTools...)
	defs = append(defs, runTools...)
	return defs
}

// jsonResult renders a handler's return value as pretty-printed JSON text
// content.
func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(data)}},
	}, nil, nil
}

const serverInstructions = `Watchfire is a local orchestrator that runs coding agents (Claude Code, Codex, Gemini CLI, opencode, Copilot CLI) on tasks inside sandboxed, git-worktree-isolated sessions, then merges the results into the project's default branch. This server is a thin facade over the local Watchfire daemon.

The canonical factory loop: create_task (status "ready") -> run_task -> wait_for_task -> get_task (check success/failure_reason) -> get_task_diff (review the merged change) -> iterate with follow-up tasks. You plan and review; Watchfire manufactures the code.

Most tools take an optional "project" argument (project id or name; see list_projects). When the server was started inside a registered project directory, that project is the default and "project" may be omitted.`

// Serve runs the MCP server over stdio until the client closes the pipe.
// It auto-starts the daemon if needed (same path as the CLI). stdout is the
// MCP transport: all logging goes to stderr.
func Serve(ctx context.Context, opts Options) error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	conn, err := ensureDaemonConn()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	s := &server{
		projects: pb.NewProjectServiceClient(conn),
		tasks:    pb.NewTaskServiceClient(conn),
		agents:   pb.NewAgentServiceClient(conn),
		settings: pb.NewSettingsServiceClient(conn),
		readOnly: opts.ReadOnly,
	}

	defaultID, err := detectDefaultProject()
	if err != nil {
		logger.Warn("failed to resolve default project from working directory", "error", err)
	}
	s.defaultProjectID = defaultID

	m := mcp.NewServer(
		&mcp.Implementation{Name: "watchfire", Title: "Watchfire", Version: opts.Version},
		&mcp.ServerOptions{Instructions: serverInstructions, Logger: logger},
	)
	for _, td := range allTools() {
		td.register(m, s)
	}

	logger.Info("watchfire mcp server serving on stdio",
		"default_project_id", s.defaultProjectID, "read_only", s.readOnly)
	return m.Run(ctx, &mcp.StdioTransport{})
}
