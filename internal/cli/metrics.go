package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/watchfire-io/watchfire/internal/config"
	"github.com/watchfire-io/watchfire/internal/daemon/metrics"
	"github.com/watchfire-io/watchfire/internal/models"
)

var metricsBackfillForce bool

var metricsCmd = &cobra.Command{
	Use:   "metrics",
	Short: "Manage per-task metrics (v6.0 Ember)",
	Long:  "Inspect and backfill the per-task metrics records that feed Insights and the weekly digest.",
}

var metricsBackfillCmd = &cobra.Command{
	Use:   "backfill",
	Short: "Reconstruct duration-only metrics for completed tasks",
	Long: `Walk every registered project and write '<n>.metrics.yaml' for any
completed task that doesn't have one yet. Token + cost fields are left
nil because the original session log may already be rotated; live
captures (post-upgrade) populate those fields. Idempotent — re-running
skips tasks that already have metrics unless --force is passed.`,
	RunE: runMetricsBackfill,
}

func init() {
	metricsBackfillCmd.Flags().BoolVar(&metricsBackfillForce, "force", false, "Overwrite existing metrics files")
	metricsCmd.AddCommand(metricsBackfillCmd)
	rootCmd.AddCommand(metricsCmd)
}

func runMetricsBackfill(_ *cobra.Command, _ []string) error {
	index, err := config.LoadProjectsIndex()
	if err != nil {
		return fmt.Errorf("load projects index: %w", err)
	}
	if index == nil || len(index.Projects) == 0 {
		fmt.Println(styleHint.Render("No projects registered. Run 'watchfire init' inside a project to register it."))
		return nil
	}

	var totalWritten, totalSkipped, totalProjects int
	for _, entry := range index.Projects {
		written, skipped, err := backfillProject(entry.ProjectID, entry.Path, metricsBackfillForce)
		if err != nil {
			fmt.Printf("  %s %s: %v\n", styleLabel.Render("error"), entry.Name, err)
			continue
		}
		totalProjects++
		totalWritten += written
		totalSkipped += skipped
		fmt.Printf("  %s  %d written, %d skipped\n", styleValue.Render(entry.Name), written, skipped)
	}
	fmt.Printf("\n%s %d projects, %d metrics written, %d skipped\n",
		styleLabel.Render("Backfill complete:"), totalProjects, totalWritten, totalSkipped)
	return nil
}

func backfillProject(projectID, projectPath string, force bool) (written, skipped int, err error) {
	tasks, err := config.LoadAllTasks(projectPath)
	if err != nil {
		return 0, 0, err
	}
	for _, t := range tasks {
		if t == nil || t.IsDeleted() {
			continue
		}
		if t.Status != models.TaskStatusDone {
			continue
		}
		if !force && config.MetricsExists(projectPath, t.TaskNumber) {
			skipped++
			continue
		}
		m := metrics.BuildBaseMetrics(projectID, t)
		if m == nil {
			continue
		}
		if writeErr := config.WriteMetrics(projectPath, m); writeErr != nil {
			return written, skipped, writeErr
		}
		written++
	}
	return written, skipped, nil
}
