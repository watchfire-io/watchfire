package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	pb "github.com/watchfire-io/watchfire/proto"
)

func renderHeader(project *pb.Project, leftTab, rightTab int, agentStatus *pb.AgentStatus, gitInfo *pb.GitInfo, width int) string {
	// Project dot and name
	projectName := "Watchfire"
	projectColor := "#888888"
	if project != nil {
		projectName = project.Name
		if project.Color != "" {
			projectColor = project.Color
		}
	}

	dot := lipgloss.NewStyle().Foreground(lipgloss.Color(projectColor)).Render("●")
	name := lipgloss.NewStyle().Bold(true).Render(projectName)

	// Git info
	gitStr := ""
	if gitInfo != nil && gitInfo.CurrentBranch != "" {
		branch := lipgloss.NewStyle().Foreground(colorDim).Render(gitInfo.CurrentBranch)
		if gitInfo.IsDirty {
			branch += lipgloss.NewStyle().Foreground(colorYellow).Render(" ✱")
		}
		gitStr = "  " + branch
	}

	// Left tabs
	leftTabs := renderTabs([]string{"Tasks", "Definition", "Settings"}, leftTab)

	// Right tabs
	rightTabs := renderTabs([]string{"Chat", "Logs"}, rightTab)

	// Agent badge
	badge := renderAgentBadge(agentStatus)

	// Layout: dot name  leftTabs    rightTabs  badge
	left := fmt.Sprintf(" %s %s%s  %s", dot, name, gitStr, leftTabs)
	right := fmt.Sprintf("%s  %s ", rightTabs, badge)

	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}

	return headerStyle.Width(width).Render(left + strings.Repeat(" ", gap) + right)
}

func renderTabs(tabs []string, active int) string {
	var parts []string
	for i, tab := range tabs {
		if i == active {
			parts = append(parts, activeTabStyle.Render(tab))
		} else {
			parts = append(parts, inactiveTabStyle.Render(tab))
		}
	}
	return strings.Join(parts, tabSepStyle.Render(" | "))
}

func renderAgentBadge(status *pb.AgentStatus) string {
	if status == nil || !status.IsRunning {
		return badgeIdleStyle.Render("● Idle")
	}

	if status.Issue != nil && status.Issue.IssueType != "" {
		return badgeIssueStyle.Render("⚠ Issue")
	}

	switch status.Mode {
	case "task":
		return badgeActiveStyle.Render(fmt.Sprintf("● Task #%04d", status.TaskNumber))
	case "chat":
		return badgeActiveStyle.Render("● Chat")
	case "wildfire":
		return badgeWildfireStyle.Render("● Wildfire")
	case "start-all":
		return badgeActiveStyle.Render(fmt.Sprintf("● All #%04d", status.TaskNumber))
	case "generate-definition":
		return badgeActiveStyle.Render("● Gen Def")
	case "generate-tasks":
		return badgeActiveStyle.Render("● Gen Tasks")
	default:
		return badgeActiveStyle.Render("● Active")
	}
}
