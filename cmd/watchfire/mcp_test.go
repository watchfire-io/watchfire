package main

import (
	"strings"
	"testing"

	"github.com/watchfire-io/watchfire/internal/mcpserver/install"
)

// The CLI is the one onboarding surface that does not go through the daemon:
// `watchfire mcp install` calls install.Clients() in-process, while the TUI
// and GUI read the same data over SettingsService.GetMcpClientStatus. These
// tests pin the CLI to the shared package so the three surfaces cannot drift
// into telling a user different things about the same machine. The e2e test
// (internal/mcpserver, //go:build mcpe2e) checks the runtime half — that the
// daemon and the CLI report identical per-harness status.

func TestInstallClientIDsMatchSharedRegistry(t *testing.T) {
	got := installClientIDs()
	clients := install.Clients()
	if len(got) != len(clients) {
		t.Fatalf("installClientIDs() = %v, install.Clients() has %d entries", got, len(clients))
	}
	for i, c := range clients {
		if got[i] != c.ID {
			t.Errorf("installClientIDs()[%d] = %q, want %q", i, got[i], c.ID)
		}
	}

	// The five harnesses v9.0 promises, in display order.
	want := []string{"claude-code", "codex", "gemini", "opencode", "copilot"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("known clients = %v, want %v", got, want)
	}
}

// TestMcpInstallHelpListsEveryClient — the command's own help text is built
// from installClientIDs(), so adding a harness must not require touching it.
func TestMcpInstallHelpListsEveryClient(t *testing.T) {
	long := mcpInstallCmd.Long
	for _, c := range install.Clients() {
		if !strings.Contains(long, c.ID) {
			t.Errorf("`watchfire mcp install` help does not mention client %q", c.ID)
		}
	}
	if !strings.Contains(long, "custom") {
		t.Error("`watchfire mcp install` help does not mention the Custom fallback")
	}
}

// TestMcpServeSilencesUsage — a serve failure is a runtime problem (no
// daemon, broken pipe), never a usage problem. Cobra's default is to print
// the flag list on any RunE error, which buries the real message in the MCP
// client's log.
func TestMcpServeSilencesUsage(t *testing.T) {
	if !mcpServeCmd.SilenceUsage {
		t.Error("mcp serve must set SilenceUsage so runtime errors are not buried in a flag dump")
	}
}

// TestMcpCommandsDocumentLocalOnly — the local-only guarantee is the reason
// this server needs no auth story, so every place a user reads about it says
// so.
func TestMcpCommandsDocumentLocalOnly(t *testing.T) {
	for name, text := range map[string]string{
		"mcp":         mcpCmd.Long,
		"mcp serve":   mcpServeCmd.Long,
		"mcp install": mcpInstallCmd.Long,
	} {
		if !strings.Contains(text, "stdio") {
			t.Errorf("`watchfire %s` help does not mention the stdio transport", name)
		}
	}
	for name, text := range map[string]string{
		"mcp":         mcpCmd.Long,
		"mcp serve":   mcpServeCmd.Long,
		"mcp install": mcpInstallCmd.Long,
	} {
		if !strings.Contains(text, "opens no listening socket") {
			t.Errorf("`watchfire %s` help does not state that no listening socket is opened", name)
		}
	}
}
