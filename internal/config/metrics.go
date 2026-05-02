package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/watchfire-io/watchfire/internal/models"
)

// MetricsFileSuffix is the on-disk suffix for per-task metrics files.
// Sibling of the canonical task file (`<n>.yaml`); kept separate so the
// user-facing task YAML doesn't get polluted with derived data and so
// `watchfire metrics backfill` can recompute without touching the task.
const MetricsFileSuffix = ".metrics.yaml"

// MetricsFile returns the absolute path to a task's metrics YAML.
func MetricsFile(projectPath string, taskNumber int) string {
	return filepath.Join(ProjectTasksDir(projectPath), formatTaskNumber(taskNumber)+MetricsFileSuffix)
}

// WriteMetrics persists a TaskMetrics record next to its canonical task
// file. Creates the tasks directory if missing — backfill across a
// freshly-checked-out worktree should still succeed.
func WriteMetrics(projectPath string, m *models.TaskMetrics) error {
	if m == nil {
		return fmt.Errorf("WriteMetrics: nil metrics")
	}
	if m.TaskNumber <= 0 {
		return fmt.Errorf("WriteMetrics: invalid task_number %d", m.TaskNumber)
	}
	if err := os.MkdirAll(ProjectTasksDir(projectPath), 0o755); err != nil {
		return err
	}
	return SaveYAML(MetricsFile(projectPath, m.TaskNumber), m)
}

// ReadMetrics loads a TaskMetrics record. Returns (nil, nil) when the
// file doesn't exist so callers can branch on "no data yet" without
// distinguishing it from a real I/O error.
func ReadMetrics(projectPath string, taskNumber int) (*models.TaskMetrics, error) {
	path := MetricsFile(projectPath, taskNumber)
	if !FileExists(path) {
		return nil, nil
	}
	var m models.TaskMetrics
	if err := LoadYAML(path, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// MetricsExists reports whether `<n>.metrics.yaml` is present.
func MetricsExists(projectPath string, taskNumber int) bool {
	return FileExists(MetricsFile(projectPath, taskNumber))
}
