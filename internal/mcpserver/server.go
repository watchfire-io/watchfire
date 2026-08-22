// Package mcpserver implements the Watchfire MCP server (`watchfire mcp
// serve`): a stateless stdio facade over the local daemon. Every tool call
// translates to an existing daemon gRPC RPC — no orchestration logic lives
// here. The package imports only the generated proto client,
// internal/config, and exported internal/cli helpers.
//
// The server is local-only by construction: its only transport is stdio,
// wired to this process's stdin/stdout by the MCP client that spawned it.
// Nothing here opens a listening socket — local_only_test.go enforces that
// as an invariant over the package source.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	pb "github.com/watchfire-io/watchfire/proto"
)

// Tool groups. --read-only serving filters registration by group.
const (
	groupProject  = "project"
	groupTask     = "task"
	groupRun      = "run"
	groupInspect  = "inspect"
	groupTelegram = "telegram"
)

// Options configures a Serve run.
type Options struct {
	// ReadOnly registers only observation tools: the project and inspect
	// registry groups (which include get_agent_status). Task-factory and
	// agent-control tools are not registered at all.
	ReadOnly bool
	// Version is the client version reported in the MCP handshake.
	Version string
}

// server holds the daemon connection and per-run state shared by all tool
// handlers. It is stateless beyond the connection and the default project:
// concurrent tool calls are safe because the daemon serializes per-project
// agent operations.
type server struct {
	projects     pb.ProjectServiceClient
	tasks        pb.TaskServiceClient
	agents       pb.AgentServiceClient
	settings     pb.SettingsServiceClient
	insights     pb.InsightsServiceClient
	logs         pb.LogServiceClient
	integrations pb.IntegrationsServiceClient

	// defaultProjectID is the project of the directory the server was
	// started in, or "" when started outside a registered project.
	defaultProjectID string

	// pollInterval is the wait_for_task polling cadence; 0 means the
	// 2-second default (tests shorten it).
	pollInterval time.Duration

	readOnly bool
}

// toolSpec is the declarative half of a registry row: everything an MCP
// client sees about a tool before calling it. Keeping name, title,
// description and behaviour hints in one literal is what makes the catalog
// auditable — a reviewer can read what the model reads without reading the
// handlers.
type toolSpec struct {
	// Group drives --read-only filtering (see readOnlyGroups).
	Group string
	// Name is the unprefixed tool name; clients namespace by server name.
	Name string
	// Title is the short human-readable label clients show in UI.
	Title string
	// Description is what the model reads to decide whether to call the
	// tool. It must state consequences, not just capability.
	Description string
	// ReadOnly marks a pure-observation tool (annotations.readOnlyHint).
	// It must agree with Group: everything in a read-only group is
	// read-only and nothing else is — TestToolAnnotationsMatchGroups.
	ReadOnly bool
	// Destructive marks a tool whose effect is not purely additive
	// (annotations.destructiveHint). Meaningful only when ReadOnly is false.
	Destructive bool
	// Idempotent marks a tool that can be repeated with the same arguments
	// without additional effect (annotations.idempotentHint).
	Idempotent bool
}

// toolDef is one row of the tool registry: the spec above plus the closure
// that binds it to the SDK.
type toolDef struct {
	spec     toolSpec
	register func(m *mcp.Server, s *server)
}

// newTool builds a registry row from a typed handler. The input schema is
// inferred from In (jsonschema struct tags become property descriptions) and
// arguments are validated by the SDK before the handler runs. The handler's
// return value is rendered as JSON text content; an error becomes a tool
// error (IsError result), not a protocol error.
//
// Optional customize functions post-process the inferred schema for
// constraints struct tags cannot express (enums, defaults, numeric ranges).
func newTool[In any](spec toolSpec, handler func(context.Context, *server, In) (any, error), customize ...func(*jsonschema.Schema)) toolDef {
	return toolDef{
		spec: spec,
		register: func(m *mcp.Server, s *server) {
			tool := &mcp.Tool{
				Name:        spec.Name,
				Description: spec.Description,
				Annotations: &mcp.ToolAnnotations{
					Title:           spec.Title,
					ReadOnlyHint:    spec.ReadOnly,
					DestructiveHint: hintPtr(spec.Destructive),
					IdempotentHint:  spec.Idempotent,
					// Every tool's world is this machine's daemon: a
					// closed, local domain — never the open internet.
					OpenWorldHint: hintPtr(false),
				},
			}
			if len(customize) > 0 {
				schema, err := jsonschema.For[In](nil)
				if err != nil {
					panic(fmt.Sprintf("tool %q: infer input schema: %v", spec.Name, err))
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

func hintPtr(b bool) *bool { return &b }

// allTools is the full registry.
func allTools() []toolDef {
	var defs []toolDef
	defs = append(defs, projectTools...)
	defs = append(defs, taskTools...)
	defs = append(defs, runTools...)
	defs = append(defs, inspectTools...)
	defs = append(defs, telegramTools...)
	return defs
}

// readOnlyGroups are the registry groups still served under --read-only:
// pure-observation tools that cannot create tasks or control agents.
// get_agent_status is included because it carries the inspect registry
// group (see tools_run.go).
var readOnlyGroups = map[string]bool{
	groupProject: true,
	groupInspect: true,
}

// registeredTools is the registry a Serve run actually exposes: everything,
// or only the read-only groups. Filtering happens at registration time, so a
// tool excluded by --read-only is not merely hidden from tools/list — it is
// never added to the SDK server, and calling it by name fails as unknown.
func registeredTools(readOnly bool) []toolDef {
	defs := allTools()
	if !readOnly {
		return defs
	}
	filtered := make([]toolDef, 0, len(defs))
	for _, td := range defs {
		if readOnlyGroups[td.spec.Group] {
			filtered = append(filtered, td)
		}
	}
	return filtered
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

const serverInstructions = `Watchfire is a local orchestrator that runs coding agents (Claude Code, Codex, Gemini CLI, opencode, Copilot CLI) on tasks inside sandboxed, git-worktree-isolated sessions, then merges the results into the project's default branch. This server is a thin facade over the Watchfire daemon on this machine.

THE FACTORY LOOP — you plan and review; Watchfire manufactures the code:
  1. create_task   — file the work with a prompt and acceptance criteria.
  2. run_task      — start a sandboxed agent on it in an isolated worktree.
  3. wait_for_task — block until the run finishes. On timed_out: true, call it again.
  4. get_task      — read the outcome. "done" only means the agent stopped;
                     check the success flag and failure_reason.
  5. get_task_diff — review exactly what was merged, then iterate with follow-up tasks.

Before you start:
- Creating a task never starts it. status "ready" only queues a task — for run_all, and for an already-running run_all/wildfire to chain into. Use run_task to run one now.
- Watchfire runs at most ONE agent per project. run_task, run_all and start_wildfire refuse while an agent is running; they never queue or replace it. Wait with wait_for_task or abort with stop_agent.
- Agent runs take minutes, not seconds. Block with wait_for_task, diagnose an apparently stuck run with get_agent_screen, abort with stop_agent.
- start_wildfire is autonomous: it creates AND executes tasks nobody reviewed. Start it only when the user explicitly asked for autonomous operation.

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
		projects:     pb.NewProjectServiceClient(conn),
		tasks:        pb.NewTaskServiceClient(conn),
		agents:       pb.NewAgentServiceClient(conn),
		settings:     pb.NewSettingsServiceClient(conn),
		insights:     pb.NewInsightsServiceClient(conn),
		logs:         pb.NewLogServiceClient(conn),
		integrations: pb.NewIntegrationsServiceClient(conn),
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
	for _, td := range registeredTools(s.readOnly) {
		td.register(m, s)
	}

	logger.Info("watchfire mcp server serving on stdio",
		"default_project_id", s.defaultProjectID, "read_only", s.readOnly)

	// A local stdio transport, not mcp.StdioTransport, so we can tell a
	// deliberate client disconnect from a fault — see below.
	transport := newStdioTransport()
	runErr := m.Run(ctx, transport)

	// The MCP spec shuts a stdio server down by closing its stdin. The SDK
	// surfaces that as a session error ("server is closing: EOF"), which
	// would make `watchfire mcp serve` exit nonzero on every normal
	// shutdown — clients read that as a crashed server. An input stream
	// that ended in EOF, or a cancelled context (SIGINT/SIGTERM), is a
	// clean exit.
	if transport.sawEOF() || isCleanShutdown(runErr) {
		logger.Info("watchfire mcp server shut down")
		return nil
	}
	return runErr
}

// stdioTransport is mcp.StdioTransport plus a flag recording whether the
// input stream ended in EOF — whether the client closed the pipe on purpose.
// stdout is wrapped in a no-op Close: the SDK closes both ends of the
// connection on teardown, and closing this process's stdout is not ours to
// do (mcp.StdioTransport does the same).
type stdioTransport struct {
	*mcp.IOTransport
	eof *atomic.Bool
}

func newStdioTransport() *stdioTransport {
	eof := &atomic.Bool{}
	return &stdioTransport{
		IOTransport: &mcp.IOTransport{
			Reader: &eofRecordingReader{r: os.Stdin, eof: eof},
			Writer: nopWriteCloser{os.Stdout},
		},
		eof: eof,
	}
}

func (t *stdioTransport) sawEOF() bool { return t.eof.Load() }

// eofRecordingReader records a clean end-of-input so Serve can distinguish
// "the client hung up" from "the session broke".
type eofRecordingReader struct {
	r   io.ReadCloser
	eof *atomic.Bool
}

func (r *eofRecordingReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if errors.Is(err, io.EOF) {
		r.eof.Store(true)
	}
	return n, err
}

func (r *eofRecordingReader) Close() error { return r.r.Close() }

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
