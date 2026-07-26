//go:build mcpe2e

// Package-level end-to-end test for the Watchfire MCP server.
//
// Everything else in this package tests handlers against fake gRPC clients.
// This file tests the shipped artifact: it spawns the real `watchfire mcp
// serve` binary against a real `watchfired`, speaks MCP to it over stdio as
// an actual client, and walks the factory loop's non-destructive half —
// initialize, tools/list, list_projects, create_task(draft),
// update_task(ready), get_task, delete_task.
//
// It deliberately never starts an agent. run_task / run_all / start_wildfire
// would spawn a real coding agent against a real model API; the run tools are
// exercised only through their refusal paths (unknown task, unknown project),
// which fail before any process is started.
//
// It is behind the `mcpe2e` build tag because it needs a live daemon, and it
// runs from `make test-mcp-e2e` (which builds the binaries first). `make test`
// never compiles it.
//
// Isolation: HOME is redirected to a temp directory for the test process and
// every child, so the daemon this test starts gets its own ~/.watchfire —
// its own project index, singleton lock, dynamic port and logs. It cannot see
// or disturb the developer's real daemon.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/mcpserver/install"
	"github.com/watchfire-io/watchfire/internal/models"
	pb "github.com/watchfire-io/watchfire/proto"
)

const (
	daemonReadyTimeout = 20 * time.Second
	callTimeout        = 30 * time.Second
)

// TestE2EFactoryLoop is the whole test: one daemon, one project, and a
// sequence of MCP sessions against it. Sub-tests share the fixture because
// starting a daemon costs seconds and the assertions are ordered — the task
// created in "factory loop" is the one deleted at its end.
func TestE2EFactoryLoop(t *testing.T) {
	env := setupEnv(t)

	t.Run("factory loop", func(t *testing.T) { testFactoryLoop(t, env) })
	t.Run("tool errors are actionable", func(t *testing.T) { testActionableErrors(t, env) })
	t.Run("read-only serves no write or run tool", func(t *testing.T) { testReadOnlyServer(t, env) })
	t.Run("server opens no listening socket", func(t *testing.T) { testNoListeningSocket(t, env) })
	t.Run("onboarding surfaces agree", func(t *testing.T) { testOnboardingConsistency(t, env) })
}

// --- fixture --------------------------------------------------------------

type e2eEnv struct {
	watchfireBin string
	daemonBin    string
	projectDir   string
	projectID    string
	projectName  string
	daemonPort   int
}

func setupEnv(t *testing.T) *e2eEnv {
	t.Helper()

	// Locate the binaries BEFORE HOME moves: `make test-mcp-e2e` builds
	// them into ./build with the developer's normal toolchain env.
	binDir := os.Getenv("WATCHFIRE_E2E_BIN_DIR")
	if binDir == "" {
		binDir = filepath.Join("..", "..", "build")
	}
	binDir, err := filepath.Abs(binDir)
	if err != nil {
		t.Fatalf("resolve bin dir: %v", err)
	}
	exeSuffix := ""
	if runtime.GOOS == "windows" {
		exeSuffix = ".exe"
	}
	env := &e2eEnv{
		watchfireBin: filepath.Join(binDir, "watchfire"+exeSuffix),
		daemonBin:    filepath.Join(binDir, "watchfired"+exeSuffix),
		projectID:    "e2e-mcp-project",
		projectName:  "mcp-e2e",
	}
	for _, bin := range []string{env.watchfireBin, env.daemonBin} {
		if _, err := os.Stat(bin); err != nil {
			t.Skipf("%s not built — run this test via `make test-mcp-e2e`", bin)
		}
	}

	// Redirect HOME so the daemon, the MCP server and this test process all
	// share one throwaway ~/.watchfire.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows equivalent
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	env.projectDir = registerProject(t, env)
	env.daemonPort = startDaemon(t, env)
	return env
}

// registerProject creates a git repo with a Watchfire project inside it and
// registers it in the (temp) global index, exactly as `watchfire init` would.
func registerProject(t *testing.T, env *e2eEnv) string {
	t.Helper()
	dir := t.TempDir()

	// t.TempDir() can hand back a symlinked path on macOS (/var → /private/var);
	// the daemon stores and compares real paths, so resolve it once here.
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolve project dir: %v", err)
	}
	dir = resolved

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# e2e\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.email", "e2e@watchfire.test"},
		{"config", "user.name", "Watchfire E2E"},
		{"add", "-A"},
		{"commit", "-m", "initial commit"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	project := models.NewProject(env.projectID, env.projectName, dir)
	project.Definition = "End-to-end fixture project for the MCP server test."
	if err := config.EnsureGlobalDir(); err != nil {
		t.Fatalf("ensure global dir: %v", err)
	}
	if err := config.EnsureProjectDir(dir); err != nil {
		t.Fatalf("ensure project dir: %v", err)
	}
	if err := config.SaveProject(dir, project); err != nil {
		t.Fatalf("save project: %v", err)
	}
	if err := config.EnsureProjectRegistered(dir); err != nil {
		t.Fatalf("register project: %v", err)
	}
	return dir
}

// startDaemon runs watchfired against the temp HOME and waits for it to
// publish a reachable port. The MCP server would auto-start one itself, but
// owning the process here gives the test a handle to shut down.
func startDaemon(t *testing.T, env *e2eEnv) int {
	t.Helper()

	cmd := exec.Command(env.daemonBin)
	cmd.Env = os.Environ()
	var log strings.Builder
	cmd.Stdout = &log
	cmd.Stderr = &log
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
		if t.Failed() {
			t.Logf("daemon output:\n%s", log.String())
		}
	})

	deadline := time.Now().Add(daemonReadyTimeout)
	for time.Now().Before(deadline) {
		running, info, err := config.IsDaemonRunning()
		if err == nil && running && info != nil {
			return info.Port
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("daemon did not become ready within %s; output:\n%s", daemonReadyTimeout, log.String())
	return 0
}

// mcpSession spawns `watchfire mcp serve` in the project directory and
// returns a connected MCP client session plus the server's PID.
func mcpSession(t *testing.T, env *e2eEnv, args ...string) (*mcp.ClientSession, int) {
	t.Helper()
	ctx := context.Background()

	cmd := exec.Command(env.watchfireBin, append([]string{"mcp", "serve"}, args...)...)
	cmd.Dir = env.projectDir
	cmd.Env = os.Environ()
	var stderr strings.Builder
	cmd.Stderr = &stderr

	client := mcp.NewClient(&mcp.Implementation{Name: "watchfire-e2e", Version: "test"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect to `watchfire mcp serve %s`: %v\nserver stderr:\n%s",
			strings.Join(args, " "), err, stderr.String())
	}
	t.Cleanup(func() {
		_ = session.Close()
		if t.Failed() {
			t.Logf("mcp server stderr:\n%s", stderr.String())
		}
	})

	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	return session, pid
}

// --- helpers --------------------------------------------------------------

// callJSON calls a tool that is expected to succeed and decodes its JSON
// text content.
func callJSON(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res := call(t, session, name, args)
	if res.IsError {
		t.Fatalf("tool %s returned an error: %s", name, resultText(res))
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(resultText(res)), &out); err != nil {
		t.Fatalf("tool %s: content is not a JSON object: %v\n%s", name, err, resultText(res))
	}
	return out
}

// callError calls a tool that is expected to fail and returns the error text.
func callError(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	res := call(t, session, name, args)
	if !res.IsError {
		t.Fatalf("tool %s unexpectedly succeeded: %s", name, resultText(res))
	}
	return resultText(res)
}

func call(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("tools/call %s: %v", name, err)
	}
	return res
}

func resultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if text, ok := c.(*mcp.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String()
}

func wantString(t *testing.T, obj map[string]any, key, want string) {
	t.Helper()
	got, _ := obj[key].(string)
	if got != want {
		t.Errorf("%s = %q, want %q", key, got, want)
	}
}

func wantContains(t *testing.T, what, got string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("%s does not mention %q:\n%s", what, want, got)
		}
	}
}

// --- sub-tests ------------------------------------------------------------

func testFactoryLoop(t *testing.T, env *e2eEnv) {
	session, _ := mcpSession(t, env)
	ctx := context.Background()

	// initialize — the handshake result is what a client caches for the
	// whole session, including the instructions that teach the loop.
	init := session.InitializeResult()
	if init.ServerInfo.Name != "watchfire" {
		t.Errorf("serverInfo.name = %q, want %q", init.ServerInfo.Name, "watchfire")
	}
	wantContains(t, "server instructions", init.Instructions,
		"create_task", "run_task", "wait_for_task", "get_task_diff", "ONE agent per project")

	// tools/list — every tool the catalog promises, with a usable schema.
	listed, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	got := map[string]*mcp.Tool{}
	for _, tool := range listed.Tools {
		got[tool.Name] = tool
	}
	for _, name := range []string{
		"list_projects", "get_project",
		"create_task", "list_tasks", "get_task", "update_task", "delete_task",
		"run_task", "run_all", "start_wildfire", "stop_agent", "get_agent_status", "wait_for_task",
		"get_task_diff", "get_agent_screen", "get_insights", "list_logs", "get_log",
	} {
		tool, ok := got[name]
		if !ok {
			t.Errorf("tools/list is missing %q", name)
			continue
		}
		if tool.Description == "" {
			t.Errorf("tool %q has no description", name)
		}
		schema, _ := tool.InputSchema.(map[string]any)
		if schema["type"] != "object" {
			t.Errorf("tool %q: inputSchema.type = %v, want object", name, schema["type"])
		}
	}
	if len(listed.Tools) != 18 {
		t.Errorf("tools/list returned %d tools, want 18", len(listed.Tools))
	}

	// list_projects — the fixture project, reachable and idle.
	projects := callJSON(t, session, "list_projects", map[string]any{})
	rows, _ := projects["projects"].([]any)
	var found map[string]any
	for _, row := range rows {
		p, _ := row.(map[string]any)
		if p["project_id"] == env.projectID {
			found = p
		}
	}
	if found == nil {
		t.Fatalf("list_projects does not include %q: %v", env.projectID, projects)
	}
	wantString(t, found, "name", env.projectName)
	wantString(t, found, "path", env.projectDir)

	// create_task — draft, so nothing can start.
	created := callJSON(t, session, "create_task", map[string]any{
		"title":               "E2E: no-op task",
		"prompt":              "This task exists only so the MCP end-to-end test can round-trip it. Do not run it.",
		"acceptance_criteria": "Never executed.",
	})
	wantString(t, created, "status", "draft")
	taskNumber, ok := created["task_number"].(float64)
	if !ok || taskNumber < 1 {
		t.Fatalf("create_task returned no usable task_number: %v", created)
	}
	if created["task_id"] == "" {
		t.Error("create_task returned an empty task_id")
	}
	wantString(t, created, "title", "E2E: no-op task")

	// The task is really on disk, written through the daemon's validated
	// path — not just echoed back over the wire.
	taskFile := filepath.Join(env.projectDir, ".watchfire", "tasks", fmt.Sprintf("%04d.yaml", int(taskNumber)))
	if _, err := os.Stat(taskFile); err != nil {
		t.Errorf("create_task did not produce %s: %v", taskFile, err)
	}

	// list_tasks sees it.
	tasks := callJSON(t, session, "list_tasks", map[string]any{})
	if !hasTask(tasks, taskNumber) {
		t.Errorf("list_tasks does not include task %d: %v", int(taskNumber), tasks)
	}

	// update_task — draft → ready. This must NOT start an agent: nothing in
	// the daemon acts on a task becoming ready on its own.
	updated := callJSON(t, session, "update_task", map[string]any{
		"task_number": taskNumber,
		"status":      "ready",
	})
	wantString(t, updated, "status", "ready")

	status := callJSON(t, session, "get_agent_status", map[string]any{})
	if running, _ := status["running"].(bool); running {
		t.Fatalf("an agent started on its own after update_task(ready): %v", status)
	}

	// get_task — the full record, matching what update_task returned.
	fetched := callJSON(t, session, "get_task", map[string]any{"task_number": taskNumber})
	wantString(t, fetched, "status", "ready")
	wantString(t, fetched, "title", "E2E: no-op task")
	wantString(t, fetched, "acceptance_criteria", "Never executed.")
	if fetched["task_id"] != created["task_id"] {
		t.Errorf("get_task task_id = %v, want %v", fetched["task_id"], created["task_id"])
	}

	// delete_task — soft delete, and the result says so.
	deleted := callJSON(t, session, "delete_task", map[string]any{"task_number": taskNumber})
	if ok, _ := deleted["deleted"].(bool); !ok {
		t.Errorf("delete_task did not report deleted: %v", deleted)
	}
	wantContains(t, "delete_task note", fmt.Sprint(deleted["note"]), "trash")

	// ...and it drops out of the default listing but stays retrievable.
	after := callJSON(t, session, "list_tasks", map[string]any{})
	if hasTask(after, taskNumber) {
		t.Errorf("list_tasks still includes deleted task %d: %v", int(taskNumber), after)
	}
	withDeleted := callJSON(t, session, "list_tasks", map[string]any{"include_deleted": true})
	if !hasTask(withDeleted, taskNumber) {
		t.Errorf("list_tasks(include_deleted) omits trashed task %d: %v", int(taskNumber), withDeleted)
	}
}

func hasTask(list map[string]any, number float64) bool {
	rows, _ := list["tasks"].([]any)
	for _, row := range rows {
		task, _ := row.(map[string]any)
		if task["task_number"] == number {
			return true
		}
	}
	return false
}

// testActionableErrors walks the failure paths an outer agent actually hits.
// Each message has to name the problem AND the way out; a bare "not found"
// leaves the model guessing.
func testActionableErrors(t *testing.T, env *e2eEnv) {
	session, _ := mcpSession(t, env)

	// Unknown project — must list the projects that do exist.
	msg := callError(t, session, "get_project", map[string]any{"project": "no-such-project"})
	wantContains(t, "unknown-project error", msg, "not found", "known projects", env.projectName, env.projectID)

	// Unknown task — must name the task and stay free of gRPC framing.
	msg = callError(t, session, "get_task", map[string]any{"task_number": 4242})
	wantContains(t, "unknown-task error", msg, "4242")
	if strings.Contains(msg, "rpc error") || strings.Contains(msg, "code = ") {
		t.Errorf("unknown-task error leaks the gRPC envelope: %s", msg)
	}

	// Unknown agent backend — must list the valid ones.
	msg = callError(t, session, "create_task", map[string]any{
		"title": "bad agent", "prompt": "never runs", "agent": "not-a-real-agent",
	})
	wantContains(t, "unknown-agent error", msg, "not-a-real-agent", "valid agents", "claude-code")

	// Missing required arguments — the SDK validates before the handler.
	msg = callError(t, session, "create_task", map[string]any{"title": "no prompt"})
	wantContains(t, "missing-argument error", msg, "prompt")

	// run_task against a task that does not exist. This is the only run-tool
	// call in the whole test: it fails while resolving the task, before any
	// agent process is created.
	msg = callError(t, session, "run_task", map[string]any{"task_number": 4242})
	wantContains(t, "run_task error", msg, "4242")

	status := callJSON(t, session, "get_agent_status", map[string]any{})
	if running, _ := status["running"].(bool); running {
		t.Fatalf("run_task on a missing task started an agent: %v", status)
	}

	// An out-of-range argument is rejected by the published schema, not by
	// a surprise at the far end.
	msg = callError(t, session, "wait_for_task", map[string]any{
		"task_number": 1, "timeout_seconds": 99999,
	})
	wantContains(t, "out-of-range error", msg, "timeout_seconds")
}

// testReadOnlyServer is the --read-only guarantee checked from the outside:
// the write and run tools are not merely refused, they are not there.
func testReadOnlyServer(t *testing.T, env *e2eEnv) {
	session, _ := mcpSession(t, env, "--read-only")
	ctx := context.Background()

	listed, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	served := map[string]bool{}
	for _, tool := range listed.Tools {
		served[tool.Name] = true
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("--read-only serves %q without readOnlyHint", tool.Name)
		}
	}

	for _, name := range []string{
		"create_task", "update_task", "delete_task",
		"run_task", "run_all", "start_wildfire", "stop_agent", "wait_for_task",
	} {
		if served[name] {
			t.Errorf("--read-only exposes write/run tool %q", name)
		}
		// Calling it by name must fail as unknown — it was never
		// registered, so there is no handler to reach.
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
		if err == nil && !res.IsError {
			t.Errorf("--read-only server executed %q", name)
		}
	}

	// The observation half still works.
	for _, name := range []string{"list_projects", "list_tasks", "get_agent_status"} {
		if !served[name] {
			t.Errorf("--read-only server is missing observation tool %q", name)
		}
	}
	callJSON(t, session, "list_projects", map[string]any{})
}

// testNoListeningSocket is the runtime half of the local-only claim
// (local_only_test.go is the source half): the live `watchfire mcp serve`
// process holds no listening socket of any kind. Its only transport is the
// pipe its parent handed it.
func testNoListeningSocket(t *testing.T, env *e2eEnv) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("socket inspection needs lsof (%s)", runtime.GOOS)
	}
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("lsof not available")
	}

	session, pid := mcpSession(t, env)
	if pid == 0 {
		t.Fatal("could not determine the mcp serve pid")
	}
	// Make a call first so the server is fully up and has talked to the
	// daemon — an idle process proves less.
	callJSON(t, session, "list_projects", map[string]any{})

	// -a ANDs the filters: sockets of THIS pid that are listening.
	out, err := exec.Command("lsof", "-nP", "-a", "-p", fmt.Sprint(pid), "-i").CombinedOutput()
	// lsof exits 1 when nothing matches, which is exactly the outcome we
	// want; only treat unparseable output as a failure.
	if err != nil && len(out) > 0 && !strings.Contains(string(out), "COMMAND") {
		t.Fatalf("lsof failed: %v\n%s", err, out)
	}

	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "COMMAND") {
			continue
		}
		if strings.Contains(line, "LISTEN") {
			t.Errorf("`watchfire mcp serve` opened a listening socket — it must be stdio-only:\n%s", line)
			continue
		}
		// Its only network sockets should be the outbound gRPC
		// connection to the daemon, on loopback at both ends.
		for _, host := range socketHosts(line) {
			if !isLoopbackHost(host) {
				t.Errorf("`watchfire mcp serve` holds a socket to non-loopback host %q:\n%s", host, line)
			}
		}
	}
}

// socketHosts pulls the endpoint hosts out of one lsof -i row. The NAME
// column is the 9th field and looks like "[::1]:59613->[::1]:59596" or
// "127.0.0.1:60101->127.0.0.1:59596".
func socketHosts(line string) []string {
	fields := strings.Fields(line)
	if len(fields) < 9 {
		return nil
	}
	var hosts []string
	for _, endpoint := range strings.Split(fields[8], "->") {
		// Strip the port: everything after the last colon, unless the
		// address is a bracketed IPv6 literal.
		if i := strings.LastIndex(endpoint, "]:"); i >= 0 {
			hosts = append(hosts, endpoint[:i+1])
			continue
		}
		if i := strings.LastIndex(endpoint, ":"); i >= 0 {
			endpoint = endpoint[:i]
		}
		hosts = append(hosts, endpoint)
	}
	return hosts
}

func isLoopbackHost(host string) bool {
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// testOnboardingConsistency spot-checks that the three onboarding surfaces
// describe this machine identically. The CLI computes status in-process, the
// TUI and GUI both read it from the daemon over gRPC — if those two ever
// diverge, a user is told a harness is configured on one screen and not on
// another.
func testOnboardingConsistency(t *testing.T, env *e2eEnv) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()

	conn, err := grpc.NewClient(fmt.Sprintf("localhost:%d", env.daemonPort),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial daemon: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// The daemon's view — what SettingsService.GetMcpClientStatus feeds the
	// TUI Settings "MCP" section and the GUI Settings "MCP" panel.
	resp, err := pb.NewSettingsServiceClient(conn).GetMcpClientStatus(ctx, &emptypb.Empty{})
	if err != nil {
		t.Fatalf("GetMcpClientStatus: %v", err)
	}
	daemonView := map[string]*pb.McpClientStatus{}
	for _, c := range resp.Clients {
		daemonView[c.Client] = c
	}

	// The CLI's view — install.Clients() evaluated in this process, which
	// shares the same HOME as the daemon.
	clients := install.Clients()
	if len(daemonView) != len(clients) {
		t.Errorf("daemon reports %d MCP clients, install.Clients() has %d", len(daemonView), len(clients))
	}
	for _, c := range clients {
		got, ok := daemonView[c.ID]
		if !ok {
			t.Errorf("daemon does not report MCP client %q", c.ID)
			continue
		}
		want := c.Status()
		if got.Detected != want.Detected || got.Configured != want.Configured {
			t.Errorf("client %q: daemon reports detected=%v configured=%v, CLI computes detected=%v configured=%v",
				c.ID, got.Detected, got.Configured, want.Detected, want.Configured)
		}
		if got.ConfigPath != want.ConfigPath {
			t.Errorf("client %q: daemon config path %q != CLI config path %q", c.ID, got.ConfigPath, want.ConfigPath)
		}
		if got.DisplayName != c.DisplayName {
			t.Errorf("client %q: daemon display name %q != CLI display name %q", c.ID, got.DisplayName, c.DisplayName)
		}
	}

	// And the Custom snippet — the fallback every surface offers — must be
	// byte-identical wherever it is rendered.
	printed, err := exec.Command(env.watchfireBin, "mcp", "install", "--print").CombinedOutput()
	if err != nil {
		t.Fatalf("watchfire mcp install --print: %v\n%s", err, printed)
	}
	if !strings.Contains(string(printed), install.CustomSnippet()) {
		t.Errorf("`watchfire mcp install --print` does not emit the shared snippet:\n%s", printed)
	}
	if resp.CustomSnippet != install.CustomSnippet() {
		t.Errorf("daemon custom snippet != install.CustomSnippet():\n%s", resp.CustomSnippet)
	}
	// The snippet must keep describing a stdio spawn, not a URL.
	wantContains(t, "custom snippet", install.CustomSnippet(), `"command": "watchfire"`, `"mcp", "serve"`)
	if strings.Contains(install.CustomSnippet(), "http") {
		t.Errorf("custom snippet advertises a network transport:\n%s", install.CustomSnippet())
	}
}
