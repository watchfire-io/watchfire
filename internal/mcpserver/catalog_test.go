package mcpserver

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The tool catalog is a prompt: it is the only thing an LLM reads before
// deciding what to call. These tests assert the properties a reviewer would
// otherwise have to re-check by hand on every change — that names are
// consistent, that consequences are stated, and that the behaviour hints an
// MCP client uses to auto-approve a call match the read-only registry.
//
// They exercise the real registration path (mcp.AddTool + schema inference)
// over an in-memory transport, so what they inspect is exactly the tools/list
// payload a client receives. No daemon is involved: tools/list never reaches
// a handler.

// listCatalog registers the tools of a Serve run and returns the tools/list
// result a client would see.
func listCatalog(t *testing.T, readOnly bool) []*mcp.Tool {
	t.Helper()
	ctx := context.Background()

	srv := mcp.NewServer(&mcp.Implementation{Name: "watchfire", Version: "test"},
		&mcp.ServerOptions{Instructions: serverInstructions})
	for _, td := range registeredTools(readOnly) {
		td.register(srv, &server{})
	}

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := srv.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("connect server: %v", err)
	}
	defer func() { _ = serverSession.Close() }()

	client := mcp.NewClient(&mcp.Implementation{Name: "catalog-test", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	defer func() { _ = clientSession.Close() }()

	res, err := clientSession.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	return res.Tools
}

var toolNameRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func TestCatalogNamesAndDescriptions(t *testing.T) {
	seen := map[string]bool{}
	for _, tool := range listCatalog(t, false) {
		if !toolNameRE.MatchString(tool.Name) {
			t.Errorf("tool %q: name is not lower_snake_case", tool.Name)
		}
		if seen[tool.Name] {
			t.Errorf("tool %q: registered twice", tool.Name)
		}
		seen[tool.Name] = true

		// A one-line description cannot state a consequence. Every tool
		// here either changes the machine's state or returns a shape the
		// caller has to interpret; both need a paragraph.
		if len(tool.Description) < 120 {
			t.Errorf("tool %q: description is %d chars, too short to state what it does and what it costs",
				tool.Name, len(tool.Description))
		}
		if tool.Annotations == nil || tool.Annotations.Title == "" {
			t.Errorf("tool %q: missing annotations.title", tool.Name)
		}
	}
}

// TestCatalogArgumentNaming pins the two argument names that appear across
// most of the surface. A tool that called them "project_id" or "task" would
// read as a different concept to the model.
func TestCatalogArgumentNaming(t *testing.T) {
	for _, tool := range listCatalog(t, false) {
		props := schemaProperties(t, tool)
		for name, prop := range props {
			desc, _ := prop["description"].(string)
			switch {
			case strings.Contains(name, "project") && name != "project":
				t.Errorf("tool %q: project argument named %q, want \"project\"", tool.Name, name)
			case strings.Contains(name, "task") && name != "task_number":
				t.Errorf("tool %q: task argument named %q, want \"task_number\"", tool.Name, name)
			}
			if desc == "" {
				t.Errorf("tool %q: argument %q has no description", tool.Name, name)
			}
		}
		// Wherever "project" is accepted it is optional, and says so:
		// the cwd default is the whole point of the argument.
		if prop, ok := props["project"]; ok {
			desc, _ := prop["description"].(string)
			if !strings.Contains(desc, "list_projects") {
				t.Errorf("tool %q: \"project\" description does not point at list_projects: %q", tool.Name, desc)
			}
			if isRequired(t, tool, "project") {
				t.Errorf("tool %q: \"project\" must stay optional (cwd default)", tool.Name)
			}
		}
	}
}

// TestCatalogStatesConsequences guards the specific sentences an outer agent
// needs in order not to misuse the factory: what "ready" does and does not
// do, that a timeout is re-callable, that wildfire is autonomous, and that
// "done" is not "succeeded".
func TestCatalogStatesConsequences(t *testing.T) {
	byName := map[string]*mcp.Tool{}
	for _, tool := range listCatalog(t, false) {
		byName[tool.Name] = tool
	}

	musts := []struct {
		tool     string
		phrases  []string
		whatFor  string
		anyOfSet bool
	}{
		{tool: "create_task", phrases: []string{"never starts it", "run_task"},
			whatFor: "creating a task does not run it"},
		{tool: "update_task", phrases: []string{"does not start an agent", "run_task"},
			whatFor: "flipping to ready does not run it"},
		{tool: "run_task", phrases: []string{"one agent per project", "wait_for_task"},
			whatFor: "single-agent refusal and the follow-up call"},
		{tool: "run_all", phrases: []string{"already running", "chains"},
			whatFor: "single-agent refusal and chaining"},
		{tool: "start_wildfire", phrases: []string{"WARNING", "autonomous", "nobody reviewed", "stop_agent"},
			whatFor: "the autonomy warning"},
		{tool: "wait_for_task", phrases: []string{"timed_out", "call wait_for_task again", "not an error", "success flag"},
			whatFor: "re-call-on-timeout guidance"},
		{tool: "delete_task", phrases: []string{"trash", "reversible"},
			whatFor: "reversibility"},
		{tool: "get_task", phrases: []string{"success flag"},
			whatFor: "done != succeeded"},
	}
	for _, m := range musts {
		tool, ok := byName[m.tool]
		if !ok {
			t.Errorf("tool %q missing from the catalog", m.tool)
			continue
		}
		for _, phrase := range m.phrases {
			if !strings.Contains(tool.Description, phrase) {
				t.Errorf("tool %q: description does not state %s (missing %q)", m.tool, m.whatFor, phrase)
			}
		}
	}
}

// TestCatalogDoesNotClaimAutoStart is a regression guard. The descriptions
// used to promise that status "ready" auto-starts an agent when the project
// has auto_start_tasks enabled. The daemon has no such codepath — nothing
// reads Project.AutoStartTasks — so an outer agent that believed it would
// file a ready task and then wait forever.
func TestCatalogDoesNotClaimAutoStart(t *testing.T) {
	for _, tool := range listCatalog(t, false) {
		if strings.Contains(tool.Description, "auto_start_tasks") ||
			strings.Contains(tool.Description, "auto-start") {
			t.Errorf("tool %q: description claims an auto-start the daemon does not implement", tool.Name)
		}
	}
	if strings.Contains(serverInstructions, "auto_start_tasks") {
		t.Error("server instructions claim an auto-start the daemon does not implement")
	}
}

// TestServerInstructionsDescribeFactoryLoop — the instructions string is read
// once per session and is where an outer agent learns the loop exists at all.
func TestServerInstructionsDescribeFactoryLoop(t *testing.T) {
	for _, want := range []string{
		"create_task", "run_task", "wait_for_task", "get_task", "get_task_diff",
		"ONE agent per project", "start_wildfire is autonomous", "list_projects",
	} {
		if !strings.Contains(serverInstructions, want) {
			t.Errorf("server instructions missing %q", want)
		}
	}
}

// TestToolAnnotationsMatchGroups — annotations.readOnlyHint is what an MCP
// client uses to decide whether a call needs human approval, so it must not
// drift from the registry group that --read-only actually filters on.
func TestToolAnnotationsMatchGroups(t *testing.T) {
	for _, td := range allTools() {
		wantReadOnly := readOnlyGroups[td.spec.Group]
		if td.spec.ReadOnly != wantReadOnly {
			t.Errorf("tool %q: ReadOnly=%v but group %q is served under --read-only=%v",
				td.spec.Name, td.spec.ReadOnly, td.spec.Group, wantReadOnly)
		}
		if td.spec.ReadOnly && td.spec.Destructive {
			t.Errorf("tool %q: marked both read-only and destructive", td.spec.Name)
		}
	}

	// And the same thing end-to-end: every tool a --read-only server
	// serves advertises readOnlyHint to its client.
	for _, tool := range listCatalog(t, true) {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("tool %q is served under --read-only but does not advertise readOnlyHint", tool.Name)
		}
	}
}

// TestReadOnlyCatalogExcludesWriteTools asserts the --read-only guarantee at
// the wire level: excluded tools are absent from tools/list, not merely
// refused when called.
func TestReadOnlyCatalogExcludesWriteTools(t *testing.T) {
	served := map[string]bool{}
	for _, tool := range listCatalog(t, true) {
		served[tool.Name] = true
	}
	for _, name := range []string{
		"create_task", "update_task", "delete_task",
		"run_task", "run_all", "start_wildfire", "stop_agent", "wait_for_task",
	} {
		if served[name] {
			t.Errorf("--read-only server exposes write/run tool %q", name)
		}
	}
	if len(served) == 0 {
		t.Fatal("--read-only server exposes no tools at all")
	}
}

// TestCatalogSchemaConstraints checks the constraints the customize hooks add
// on top of struct-tag inference — the ones a caller would otherwise have to
// discover by getting an error.
func TestCatalogSchemaConstraints(t *testing.T) {
	byName := map[string]*mcp.Tool{}
	for _, tool := range listCatalog(t, false) {
		byName[tool.Name] = tool
	}

	cases := []struct {
		tool, prop  string
		wantEnum    []string
		wantDefault any
		wantMin     float64
		wantMax     float64
	}{
		{tool: "create_task", prop: "status", wantEnum: []string{"draft", "ready"}, wantDefault: "draft"},
		{tool: "update_task", prop: "status", wantEnum: []string{"draft", "ready"}},
		{tool: "get_insights", prop: "scope", wantEnum: []string{"project", "global"}, wantDefault: "project"},
		{tool: "wait_for_task", prop: "timeout_seconds", wantDefault: float64(300), wantMin: 1, wantMax: 600},
		{tool: "get_agent_screen", prop: "lines", wantDefault: float64(100), wantMin: 1, wantMax: 1000},
	}
	for _, tc := range cases {
		tool, ok := byName[tc.tool]
		if !ok {
			t.Errorf("tool %q missing", tc.tool)
			continue
		}
		prop, ok := schemaProperties(t, tool)[tc.prop]
		if !ok {
			t.Errorf("tool %q: no property %q", tc.tool, tc.prop)
			continue
		}
		if tc.wantEnum != nil {
			var got []string
			for _, v := range prop["enum"].([]any) {
				got = append(got, v.(string))
			}
			if strings.Join(got, ",") != strings.Join(tc.wantEnum, ",") {
				t.Errorf("tool %q property %q: enum = %v, want %v", tc.tool, tc.prop, got, tc.wantEnum)
			}
		}
		if tc.wantDefault != nil && prop["default"] != tc.wantDefault {
			t.Errorf("tool %q property %q: default = %v, want %v", tc.tool, tc.prop, prop["default"], tc.wantDefault)
		}
		if tc.wantMin != 0 && prop["minimum"] != tc.wantMin {
			t.Errorf("tool %q property %q: minimum = %v, want %v", tc.tool, tc.prop, prop["minimum"], tc.wantMin)
		}
		if tc.wantMax != 0 && prop["maximum"] != tc.wantMax {
			t.Errorf("tool %q property %q: maximum = %v, want %v", tc.tool, tc.prop, prop["maximum"], tc.wantMax)
		}
	}
}

// TestCatalogRequiredArguments pins which arguments a client must send.
func TestCatalogRequiredArguments(t *testing.T) {
	want := map[string][]string{
		"list_projects":    {},
		"get_project":      {},
		"create_task":      {"title", "prompt"},
		"list_tasks":       {},
		"get_task":         {"task_number"},
		"update_task":      {"task_number"},
		"delete_task":      {"task_number"},
		"run_task":         {"task_number"},
		"run_all":          {},
		"start_wildfire":   {},
		"stop_agent":       {},
		"get_agent_status": {},
		"wait_for_task":    {"task_number"},
		"get_task_diff":    {"task_number"},
		"get_agent_screen": {},
		"get_insights":     {},
		"list_logs":        {},
		"get_log":          {"log_id"},
		// v10.1 Torch — Telegram bridge control.
		"telegram_status":    {},
		"telegram_configure": {},
		"telegram_pair":      {},
		"telegram_unpair":    {"chat_id"},
	}

	tools := listCatalog(t, false)
	if len(tools) != len(want) {
		t.Errorf("catalog has %d tools, expectations cover %d", len(tools), len(want))
	}
	for _, tool := range tools {
		expected, ok := want[tool.Name]
		if !ok {
			t.Errorf("unexpected tool %q in catalog — add it to the expectations", tool.Name)
			continue
		}
		got := requiredArguments(t, tool)
		if strings.Join(got, ",") != strings.Join(expected, ",") {
			t.Errorf("tool %q: required = %v, want %v", tool.Name, got, expected)
		}
	}
}

// --- schema helpers -------------------------------------------------------
//
// A client sees InputSchema as generic JSON, so the tests read it the same
// way rather than reaching for the Go schema type.

func toolSchema(t *testing.T, tool *mcp.Tool) map[string]any {
	t.Helper()
	raw, err := json.Marshal(tool.InputSchema)
	if err != nil {
		t.Fatalf("tool %q: marshal input schema: %v", tool.Name, err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("tool %q: unmarshal input schema: %v", tool.Name, err)
	}
	if schema["type"] != "object" {
		t.Errorf("tool %q: input schema type = %v, want object", tool.Name, schema["type"])
	}
	return schema
}

func schemaProperties(t *testing.T, tool *mcp.Tool) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	props, _ := toolSchema(t, tool)["properties"].(map[string]any)
	for name, v := range props {
		prop, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("tool %q: property %q is not an object", tool.Name, name)
		}
		out[name] = prop
	}
	return out
}

func requiredArguments(t *testing.T, tool *mcp.Tool) []string {
	t.Helper()
	raw, _ := toolSchema(t, tool)["required"].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		out = append(out, v.(string))
	}
	return out
}

func isRequired(t *testing.T, tool *mcp.Tool, name string) bool {
	t.Helper()
	for _, r := range requiredArguments(t, tool) {
		if r == name {
			return true
		}
	}
	return false
}
