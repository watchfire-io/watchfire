// Package tui — v6.0 Ember fleet insights overlay.
//
// Pressing `I` (uppercase) anywhere in the TUI opens a read-only overlay
// rendering the cross-project rollup the dashboard ships in 0058: a
// sparkline of tasks-per-day, the top-projects list, and the agent
// breakdown. The TUI is single-project (entered via `watchfire` from
// inside a project root), so "fleet" data is fetched on demand from the
// daemon's `InsightsService.GetGlobalInsights` RPC. Esc / q closes.
//
// The TUI sparkline is a unicode block ramp — keeps the overlay
// dependency-free.
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

// FleetInsights wraps the proto response so the rest of the TUI doesn't
// import pb directly. nil means "still loading" / "no fetch issued".
type FleetInsights struct {
	Window string // "7d" | "30d" | "90d" | "all"
	Data   *pb.GlobalInsights
	Err    error
}

// FleetInsightsLoadedMsg is dispatched when the daemon RPC returns.
type FleetInsightsLoadedMsg struct {
	Insights FleetInsights
}

// loadFleetInsightsCmd issues the gRPC call. Window selection is encoded
// as a (start, end) pair: 7d / 30d / 90d compute back from now; "all"
// passes both bounds unset.
func loadFleetInsightsCmd(conn *grpc.ClientConn, window string) tea.Cmd {
	return func() tea.Msg {
		if conn == nil {
			return FleetInsightsLoadedMsg{Insights: FleetInsights{Window: window, Err: fmt.Errorf("daemon not connected")}}
		}
		client := pb.NewInsightsServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		req := &pb.GetGlobalInsightsRequest{Meta: &pb.RequestMeta{Origin: "tui"}}
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

		resp, err := client.GetGlobalInsights(ctx, req)
		if err != nil {
			return FleetInsightsLoadedMsg{Insights: FleetInsights{Window: window, Err: err}}
		}
		return FleetInsightsLoadedMsg{Insights: FleetInsights{Window: window, Data: resp}}
	}
}

// renderFleetInsightsOverlay renders the boxed overlay matching the spec
// idiom: header line + sparkline + top projects + agent breakdown. The
// box width adapts to the longest interior line.
func renderFleetInsightsOverlay(insights FleetInsights, width int) string {
	if width < 50 {
		width = 50
	}
	if width > 80 {
		width = 80
	}

	header := fleetHeaderLine(insights.Window)
	var body []string

	switch {
	case insights.Err != nil:
		body = append(body, lipgloss.NewStyle().Foreground(colorYellow).Render("Failed to load: "+insights.Err.Error()))
	case insights.Data == nil:
		body = append(body, lipgloss.NewStyle().Foreground(colorDim).Render("Loading…"))
	case insights.Data.GetTasksTotal() == 0:
		body = append(body, lipgloss.NewStyle().Foreground(colorDim).Render("No completed tasks in this window."))
	default:
		body = append(body, fleetKpiLine(insights.Data))
		body = append(body, "")
		body = append(body, fleetSparklineLine(insights.Data))
		body = append(body, fleetTopProjectsLine(insights.Data))
		body = append(body, fleetAgentsLine(insights.Data))
	}

	body = append(body, "")
	body = append(body, lipgloss.NewStyle().Foreground(colorDim).Render(
		"1 / 3 / 9 / 0 cycle window · Enter open project · Esc close",
	))

	content := lipgloss.JoinVertical(lipgloss.Left, append([]string{header, ""}, body...)...)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorOrange).
		Padding(0, 1).
		Width(width)

	return box.Render(content)
}

func fleetHeaderLine(window string) string {
	label := windowLongLabel(window)
	title := lipgloss.NewStyle().Foreground(colorOrange).Bold(true).Render("Fleet insights")
	return title + lipgloss.NewStyle().Foreground(colorDim).Render(" · last "+label)
}

func windowLongLabel(window string) string {
	switch window {
	case "7d":
		return "7 days"
	case "90d":
		return "90 days"
	case "all":
		return "all time"
	default:
		return "30 days"
	}
}

func fleetKpiLine(data *pb.GlobalInsights) string {
	total := data.GetTasksTotal()
	successPct := 0
	if total > 0 {
		successPct = int(float64(data.GetTasksSucceeded()) / float64(total) * 100)
	}
	dur := formatDurationMs(data.GetTotalDurationMs())
	cost := fmt.Sprintf("$%.2f", data.GetTotalCostUsd())
	if data.GetTasksMissingCost() > 0 {
		cost += fmt.Sprintf(" (%d part)", data.GetTasksMissingCost())
	}
	return fmt.Sprintf("Total: %d  Success: %d%%  Time: %s  Cost: %s", total, successPct, dur, cost)
}

const sparklineRamp = " ▁▂▃▄▅▆▇█"

func fleetSparklineLine(data *pb.GlobalInsights) string {
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

func fleetTopProjectsLine(data *pb.GlobalInsights) string {
	rows := data.GetTopProjects()
	if len(rows) == 0 {
		return prefixedRow("Top proj", lipgloss.NewStyle().Foreground(colorDim).Render("(none yet)"))
	}
	parts := make([]string, 0, len(rows))
	for _, p := range rows {
		parts = append(parts, fmt.Sprintf("%s %d", p.GetProjectName(), p.GetCount()))
	}
	return prefixedRow("Top proj", strings.Join(parts, "  "))
}

func fleetAgentsLine(data *pb.GlobalInsights) string {
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

func prefixedRow(label, body string) string {
	prefix := lipgloss.NewStyle().Foreground(colorDim).Render(fmt.Sprintf("%-9s", label) + " ┊ ")
	return prefix + body
}

func formatDurationMs(ms int64) string {
	if ms <= 0 {
		return "0m"
	}
	totalMin := ms / 60_000
	if totalMin < 60 {
		return fmt.Sprintf("%dm", totalMin)
	}
	hr := totalMin / 60
	rem := totalMin % 60
	return fmt.Sprintf("%dh %02dm", hr, rem)
}
