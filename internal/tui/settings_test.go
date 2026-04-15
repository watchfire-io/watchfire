package tui

import (
	"testing"

	pb "github.com/watchfire-io/watchfire/proto"
)

func TestSettingsCycleAgent(t *testing.T) {
	f := NewSettingsForm()
	f.LoadFromProject(&pb.Project{Name: "demo", DefaultAgent: "claude-code"})

	agentIdx := -1
	for i, field := range f.fields {
		if field.Key == "default_agent" {
			agentIdx = i
			break
		}
	}
	if agentIdx == -1 {
		t.Fatalf("default_agent field missing from settings form")
	}

	f.cursor = agentIdx
	start := f.fields[agentIdx].CycleIndex
	opts := f.fields[agentIdx].CycleOptions
	if len(opts) == 0 {
		t.Fatalf("expected cycle options")
	}
	if opts[start].Value != "claude-code" {
		t.Fatalf("expected starting value claude-code, got %q", opts[start].Value)
	}

	changed, key, value := f.Toggle()
	if !changed {
		t.Fatalf("expected toggle on cycle field to report change")
	}
	if key != "default_agent" {
		t.Fatalf("expected key default_agent, got %q", key)
	}
	next := (start + 1) % len(opts)
	if value != opts[next].Value {
		t.Fatalf("expected value %q, got %v", opts[next].Value, value)
	}

	// Cycle all the way around.
	for i := 0; i < len(opts); i++ {
		f.Toggle()
	}
	if f.fields[agentIdx].CycleIndex != next {
		// After len(opts) more toggles, we return to `next`.
		t.Fatalf("expected cycle to wrap back, got index %d", f.fields[agentIdx].CycleIndex)
	}
}

func TestSettingsCycleAgentFallsBackForUnknown(t *testing.T) {
	f := NewSettingsForm()
	f.LoadFromProject(&pb.Project{Name: "demo", DefaultAgent: ""})

	for _, field := range f.fields {
		if field.Key != "default_agent" {
			continue
		}
		if len(field.CycleOptions) == 0 {
			t.Fatalf("expected at least one cycle option")
		}
		got := field.CycleOptions[field.CycleIndex].Value
		if got != "claude-code" {
			t.Fatalf("expected fallback to claude-code, got %q", got)
		}
		return
	}
	t.Fatalf("default_agent field missing")
}
