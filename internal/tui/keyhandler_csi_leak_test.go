package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/watchfire-io/watchfire/proto"
)

// modelWithRunningAgent returns a Model wired so the
// forward-to-agent branch in handleTerminalKey would otherwise fire.
// The conn is a lazy gRPC client to a non-routable address — no dial
// happens until a tea.Cmd is invoked, which the tests never do; the
// tests only inspect whether handleTerminalKey returns a cmd.
func modelWithRunningAgent(t *testing.T) *Model {
	t.Helper()
	conn, err := grpc.NewClient("127.0.0.1:0",
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &Model{
		projectID:   "test-project",
		conn:        conn,
		agentStatus: &pb.AgentStatus{IsRunning: true},
		terminal:    NewTerminal(),
	}
}

// TestHandleTerminalKey_DropsAltRunes asserts that the Alt+rune leak
// shape — produced by Bubble Tea when it consumes `\x1b<rune>` from an
// unrecognised escape sequence — is silently dropped. The classic case
// is `Alt+[` from a partial SGR mouse sequence.
func TestHandleTerminalKey_DropsAltRunes(t *testing.T) {
	m := modelWithRunningAgent(t)

	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}, Alt: true}
	cmd := m.handleTerminalKey(msg)

	if cmd != nil {
		t.Fatalf("Alt+[ produced cmd %v, want nil — should have been dropped", cmd)
	}
}

// TestHandleTerminalKey_DropsSGRMouseResidue asserts that the
// `<button;col;row M` tail of a split SGR mouse sequence is recognised
// and dropped before reaching the forward-to-agent branch.
func TestHandleTerminalKey_DropsSGRMouseResidue(t *testing.T) {
	cases := []string{
		"<64;105;35M",  // wheel up
		"<65;105;35M",  // wheel down
		"<0;10;5M",     // left button press
		"<0;10;5m",     // lowercase release variant
		"<32;100;50M",  // motion event
		"<99;999;999M", // large coordinates
	}
	for _, residue := range cases {
		t.Run(residue, func(t *testing.T) {
			m := modelWithRunningAgent(t)
			msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(residue)}
			cmd := m.handleTerminalKey(msg)
			if cmd != nil {
				t.Fatalf("residue %q produced cmd %v, want nil — should have been dropped", residue, cmd)
			}
		})
	}
}

// TestHandleTerminalKey_AllowsLegitimateRunes proves the guards don't
// over-filter. Plain typed text must still produce a forward-to-agent
// cmd.
func TestHandleTerminalKey_AllowsLegitimateRunes(t *testing.T) {
	cases := []string{
		"hello",
		"a",
		"<not-a-mouse-event>",
		"<button>",
		"<64;105;35X", // wrong terminator
		"64;105;35M",  // missing leading <
	}

	for _, text := range cases {
		t.Run(text, func(t *testing.T) {
			m := modelWithRunningAgent(t)
			msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)}
			cmd := m.handleTerminalKey(msg)
			if cmd == nil {
				t.Fatalf("legitimate input %q was dropped — over-filtering regression", text)
			}
		})
	}
}
