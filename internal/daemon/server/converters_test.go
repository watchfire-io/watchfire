package server

import (
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/models"
)

func TestModelToProtoTaskPreservesAgent(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name  string
		agent string
	}{
		{"non-empty", "codex"},
		{"empty", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := &models.Task{
				TaskID:     "abcd1234",
				TaskNumber: 1,
				Title:      "t",
				Prompt:     "p",
				Agent:      tc.agent,
				Status:     models.TaskStatusReady,
				CreatedAt:  now,
				UpdatedAt:  now,
			}
			pt := modelToProtoTask(task, "proj-1")
			if pt.Agent != tc.agent {
				t.Errorf("Agent: got %q, want %q", pt.Agent, tc.agent)
			}
		})
	}
}
