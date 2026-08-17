package cli

import (
	"strings"
	"testing"
)

func TestConfirmPromptRequiresExplicitYes(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"y\n", true},
		{"Y\n", true},
		{"yes\n", true},
		{"YES\n", true},
		{"n\n", false},
		{"no\n", false},
		{"\n", false},    // bare Enter defaults to no
		{"", false},      // EOF (non-interactive pipe) defaults to no
		{"yep\n", false}, // anything not exactly y/yes is no
	}
	for _, c := range cases {
		var out strings.Builder
		got := confirmPrompt(strings.NewReader(c.input), &out, "Archive 2 folded task(s)? [y/N] ")
		if got != c.want {
			t.Errorf("confirmPrompt(%q) = %v, want %v", c.input, got, c.want)
		}
		if !strings.Contains(out.String(), "Archive 2 folded task(s)?") {
			t.Errorf("prompt text not written for input %q", c.input)
		}
	}
}
