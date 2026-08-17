package task

import (
	"fmt"
	"sort"
	"time"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/models"
)

// RetrofitFoldWindow returns the done, non-deleted tasks that a
// retrofit-definition run should fold into the project definition: every
// done task with a number above the project's last-retrofit watermark
// (all done tasks on the first run). Sorted by task number ascending so
// the prompt reads in shipping order.
func (m *Manager) RetrofitFoldWindow(projectPath string) ([]*models.Task, error) {
	project, err := config.LoadProject(projectPath)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, fmt.Errorf("project not found: %s", projectPath)
	}

	active, err := config.LoadActiveTasks(projectPath)
	if err != nil {
		return nil, err
	}

	var window []*models.Task
	for _, t := range active {
		if t.Status == models.TaskStatusDone && t.TaskNumber > project.LastRetrofitTaskNumber {
			window = append(window, t)
		}
	}
	sort.Slice(window, func(i, j int) bool {
		return window[i].TaskNumber < window[j].TaskNumber
	})
	return window, nil
}

// AdvanceRetrofitWatermark moves the project's last-retrofit watermark to
// the highest done, non-deleted task number — called when a
// retrofit-definition session signals completion, so the next run only
// folds tasks completed after this one. Returns the new watermark. A
// no-op (watermark unchanged, nothing saved) when no done task exceeds
// the current watermark.
func (m *Manager) AdvanceRetrofitWatermark(projectPath string) (int, error) {
	project, err := config.LoadProject(projectPath)
	if err != nil {
		return 0, err
	}
	if project == nil {
		return 0, fmt.Errorf("project not found: %s", projectPath)
	}

	active, err := config.LoadActiveTasks(projectPath)
	if err != nil {
		return 0, err
	}

	maxDone := project.LastRetrofitTaskNumber
	for _, t := range active {
		if t.Status == models.TaskStatusDone && t.TaskNumber > maxDone {
			maxDone = t.TaskNumber
		}
	}
	if maxDone == project.LastRetrofitTaskNumber {
		return project.LastRetrofitTaskNumber, nil
	}

	project.LastRetrofitTaskNumber = maxDone
	project.UpdatedAt = time.Now().UTC()
	if err := config.SaveProject(projectPath, project); err != nil {
		return 0, err
	}
	return maxDone, nil
}

// RetrofitArchiveCandidates returns the done, non-deleted tasks at or
// below the last-retrofit watermark — the folded tasks the confirm-gated
// archive would soft-delete. Sorted by task number ascending.
func (m *Manager) RetrofitArchiveCandidates(projectPath string) ([]*models.Task, error) {
	project, err := config.LoadProject(projectPath)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, fmt.Errorf("project not found: %s", projectPath)
	}

	active, err := config.LoadActiveTasks(projectPath)
	if err != nil {
		return nil, err
	}

	var candidates []*models.Task
	for _, t := range active {
		if t.Status == models.TaskStatusDone && t.TaskNumber <= project.LastRetrofitTaskNumber {
			candidates = append(candidates, t)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].TaskNumber < candidates[j].TaskNumber
	})
	return candidates, nil
}

// ArchiveRetrofitTasks soft-deletes every retrofit archive candidate,
// marking each with RetrofitArchived so insights rollups keep counting
// it from the trash. Reversible via the normal Restore path (which clears
// the mark). Callers MUST have obtained explicit user confirmation —
// this method never checks, it only selects conservatively: done,
// non-deleted, at or below the watermark. Returns the archived tasks.
func (m *Manager) ArchiveRetrofitTasks(projectPath string) ([]*models.Task, error) {
	candidates, err := m.RetrofitArchiveCandidates(projectPath)
	if err != nil {
		return nil, err
	}

	for _, t := range candidates {
		t.RetrofitArchived = true
		t.Delete()
		if err := config.SaveTask(projectPath, t); err != nil {
			return nil, err
		}
	}
	return candidates, nil
}
