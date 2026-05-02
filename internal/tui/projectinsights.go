// Package tui — v6.0 Ember per-project insights overlay.
//
// Pressing `i` on the task list opens a read-only overlay rendering the
// per-project rollup the daemon ships under
// `InsightsService.GetProjectInsights`: KPI strip, sparkline of tasks-per-day,
// agent breakdown, and duration percentiles. 1 / 3 / 9 / 0 cycle the window
// (7d / 30d / 90d / All); Esc / q close.
//
// Sibling of insights.go (which renders the fleet overlay). Both use the
// same unicode-block sparkline to stay dependency-free.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/watchfire-io/watchfire/proto"
)

// ProjectInsights wraps the proto response. nil Data means "still
// loading" or "no fetch issued".
type ProjectInsights struct {
	Window string // "7d" | "30d" | "90d" | "all"
	Data   *pb.ProjectInsights
	Err    error
}

// ProjectInsightsLoadedMsg is dispatched when the daemon RPC returns.
type ProjectInsightsLoadedMsg struct {
	Insights ProjectInsights
}

// loadProjectInsightsCmd issues the gRPC call. Window selection mirrors
// the fleet overlay: 7d / 30d / 90d compute back from now; "all" passes
// both bounds unset.
func loadProjectInsightsCmd(conn *grpc.ClientConn, projectID, window string) tea.Cmd {
	return func() tea.Msg {
		if conn == nil {
			return ProjectInsightsLoadedMsg{Insights: ProjectInsights{Window: window, Err: fmt.Errorf("daemon not connected")}}
		}
		client := pb.NewInsightsServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		req := &pb.GetProjectInsightsRequest{
			Meta:      &pb.RequestMeta{Origin: "tui"},
			ProjectId: projectID,
		}
		if window != "all" {
			days := 30
			switch window {
			case "7d":
				days = 7
			case "90d":
				days = 90
			}
			now := time.Now()
			start := now.Add(-time.Duration(days) * 24 * time.Hour)
			req.WindowStart = timestamppb.New(start)
			req.WindowEnd = timestamppb.New(now)
		}

		resp, err := client.GetProjectInsights(ctx, req)
		if err != nil {
			return ProjectInsightsLoadedMsg{Insights: ProjectInsights{Window: window, Err: err}}
		}
		return ProjectInsightsLoadedMsg{Insights: ProjectInsights{Window: window, Data: resp}}
	}
}

// renderProjectInsightsOverlay renders the boxed overlay matching the
// spec idiom: header line + KPI strip + sparkline + agents + duration.
func renderProjectInsightsOverlay(insights ProjectInsights, projectName string, width int) string {
	if width < 56 {
		width = 56
	}
	if width > 84 {
		width = 84
	}

	header := projectInsightsHeaderLine(projectName, insights.Window)
	var body []string

	switch {
	case insights.Err != nil:
		body = append(body, lipgloss.NewStyle().Foreground(colorYellow).Render("Failed to load: "+insights.Err.Error()))
	case insights.Data == nil:
		body = append(body, lipgloss.NewStyle().Foreground(colorDim).Render("Loading…"))
	case insights.Data.GetTasksTotal() == 0:
		body = append(body, lipgloss.NewStyle().Foreground(colorDim).Render("No completed tasks in this window."))
	default:
		body = append(body, projectInsightsKpiLine(insights.Data))
		body = append(body, "")
		body = append(body, projectSparklineLine(insights.Data))
		body = append(body, projectAgentsLine(insights.Data))
		body = append(body, projectDurationLine(insights.Data))
	}

	body = append(body, "")
	body = append(body, lipgloss.NewStyle().Foreground(colorDim).Render(
		"1 / 3 / 9 / 0 cycle window · Esc close",
	))

	content := lipgloss.JoinVertical(lipgloss.Left, append([]string{header, ""}, body...)...)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorOrange).
		Padding(0, 1).
		Width(width)

	return box.Render(content)
}

func projectInsightsHeaderLine(projectName, window string) string {
	label := windowLongLabel(window)
	if projectName == "" {
		projectName = "project"
	}
	title := lipgloss.NewStyle().Foreground(colorOrange).Bold(true).Render(
		"Insights · " + projectName,
	)
	return title + lipgloss.NewStyle().Foreground(colorDim).Render(" · last "+label)
}

func projectInsightsKpiLine(data *pb.ProjectInsights) string {
	total := data.GetTasksTotal()
	successPct := 0
	if total > 0 {
		successPct = int(float64(data.GetTasksSucceeded()) / float64(total) * 100)
	}
	dur := formatDurationMs(data.GetTotalDurationMs())
	cost := fmt.Sprintf("$%.2f", data.GetTotalCostUsd())
	if data.GetTasksMissingCost() > 0 {
		cost += fmt.Sprintf(" (%d partial)", data.GetTasksMissingCost())
	}
	return fmt.Sprintf("Total: %d  Success: %d%%  Time: %s  Cost: %s", total, successPct, dur, cost)
}

func projectSparklineLine(data *pb.ProjectInsights) string {
	buckets := data.GetTasksByDay()
	if len(buckets) == 0 {
		return prefixedRow("Tasks/day", lipgloss.NewStyle().Foreground(colorDim).Render("(no daily activity)"))
	}
	var peak int32
	for _, b := range buckets {
		if b.GetCount() > peak {
			peak = b.GetCount()
		}
	}
	if peak == 0 {
		peak = 1
	}
	rampRunes := []rune(sparklineRamp)
	scale := int32(len(rampRunes) - 1)
	var sb strings.Builder
	for _, b := range buckets {
		idx := int32(0)
		if peak > 0 {
			idx = b.GetCount() * scale / peak
		}
		if idx < 0 {
			idx = 0
		}
		if idx > scale {
			idx = scale
		}
		sb.WriteRune(rampRunes[idx])
	}
	return prefixedRow("Tasks/day", sb.String())
}

func projectAgentsLine(data *pb.ProjectInsights) string {
	rows := data.GetAgentBreakdown()
	if len(rows) == 0 {
		return prefixedRow("Agents", lipgloss.NewStyle().Foreground(colorDim).Render("(none yet)"))
	}
	parts := make([]string, 0, len(rows))
	for _, a := range rows {
		parts = append(parts, fmt.Sprintf("%s %d", a.GetAgent(), a.GetCount()))
	}
	return prefixedRow("Agents", strings.Join(parts, "  "))
}

func projectDurationLine(data *pb.ProjectInsights) string {
	avg := formatDurationMs(data.GetAvgDurationMs())
	p50 := formatDurationMs(data.GetP50DurationMs())
	p95 := formatDurationMs(data.GetP95DurationMs())
	body := fmt.Sprintf("avg %s  p50 %s  p95 %s", avg, p50, p95)
	return prefixedRow("Duration", body)
}
