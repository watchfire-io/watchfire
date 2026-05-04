package echo

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// TestErrorResponse asserts the `errorResponse` helper produces an
// ephemeral reply containing the provided message in both the text +
// block fallbacks. Concrete provider handlers rely on this to render
// "Cancel failed: ..." messages without leaking the failure into a
// public channel.
func TestErrorResponse(t *testing.T) {
	resp := errorResponse("boom")
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if !resp.Ephemeral {
		t.Fatal("expected Ephemeral=true")
	}
	if resp.Text != "boom" {
		t.Fatalf("expected text 'boom', got %q", resp.Text)
	}
	if len(resp.Blocks) != 1 || resp.Blocks[0].Text != "boom" {
		t.Fatalf("expected single block with text 'boom', got %+v", resp.Blocks)
	}
}

// TestIsNotFound: the helper recognises ErrTaskNotFound directly,
// wrapped errors that contain its message, and rejects unrelated
// errors. The router uses this to render a friendly reply rather
// than a generic failure.
func TestIsNotFound(t *testing.T) {
	if !isNotFound(ErrTaskNotFound) {
		t.Fatal("ErrTaskNotFound should be recognised")
	}
	wrapped := fmt.Errorf("lookup task ABC: %w", ErrTaskNotFound)
	if !isNotFound(wrapped) {
		t.Fatal("wrapped ErrTaskNotFound should be recognised")
	}
	if isNotFound(errors.New("some other error")) {
		t.Fatal("unrelated error should not be recognised")
	}
	if isNotFound(nil) {
		t.Fatal("nil error should not be recognised")
	}
}

// TestFormatDuration covers the four branches of the formatter
// (sub-minute, sub-hour, sub-day, multi-day) plus the negative-duration
// guard.
func TestFormatDuration(t *testing.T) {
	cases := []struct {
		name string
		in   time.Duration
		want string
	}{
		{"negative clamps to zero", -5 * time.Second, "0s"},
		{"sub-minute seconds", 42 * time.Second, "42s"},
		{"sub-hour minutes", 3 * time.Minute, "3m"},
		{"sub-day hours+minutes", 2*time.Hour + 30*time.Minute, "2h30m"},
		{"multi-day", 26 * time.Hour, "1d2h"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatDuration(tc.in); got != tc.want {
				t.Fatalf("formatDuration(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestParseTaskRef covers numeric input (returned as task_number),
// alphanumeric input (returned as task_id), the leading `#` strip,
// and the empty-string short-circuit.
func TestParseTaskRef(t *testing.T) {
	cases := []struct {
		in     string
		num    int
		id     string
		ok     bool
	}{
		{"", 0, "", false},
		{"5", 5, "", true},
		{"#42", 42, "", true},
		{"abc12345", 0, "abc12345", true},
		{"  7  ", 7, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			n, id, ok := ParseTaskRef(tc.in)
			if ok != tc.ok || n != tc.num || id != tc.id {
				t.Fatalf("ParseTaskRef(%q) = (%d, %q, %v), want (%d, %q, %v)", tc.in, n, id, ok, tc.num, tc.id, tc.ok)
			}
		})
	}
}
