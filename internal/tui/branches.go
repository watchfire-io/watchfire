// Package tui — v6 Branches overlay (Ctrl+B).
//
// Episodic action surface that lists every `watchfire/<n>` branch tracked
// by the project, with task number, age, merged status, and worktree
// presence. Mirrors the Help / Global Settings / Integrations / Fleet
// Insights overlays — visit, do something, dismiss.
//
// Motivated by the v6.0 Phoenix project.yaml-wipe race that left task
// branches 0087 / 0088 stranded with their worktrees on disk and no way
// to see the orphan state from inside the TUI. The overlay surfaces that
// exact failure mode (`merged-orphan` rollup count) and gives one-key
// remediation (`P` to prune, `m` to merge, `x` / `X` to delete).
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"google.golang.org/grpc"

	pb "github.com/watchfire-io/watchfire/proto"
)

// BranchesState backs the Branches overlay. Loaded when the user opens
// the overlay; refreshed on `r` or after every action.
type BranchesState struct {
	Branches []*pb.Branch
	Cursor   int
	Loading  bool
	Err      error

	// pending action surfaced as a row suffix ("(merging…)" / "(deleting…)")
	// while the RPC is in flight. Identifies the row by branch name to
	// survive a refresh that reorders the slice.
	PendingBranch string
	PendingAction string

	// confirm prompt rendered in the footer. Empty when no confirm is
	// pending. y/N capture is owned by the branches overlay key handler,
	// not the global confirmMode, since the actions are scoped here and
	// need different payloads.
	ConfirmPrompt string
	ConfirmKind   branchConfirmKind
	ConfirmTarget string // branch name (for merge/delete/force-delete)
}

type branchConfirmKind int

const (
	branchConfirmNone branchConfirmKind = iota
	branchConfirmMerge
	branchConfirmDelete
	branchConfirmForceDelete
	branchConfirmPrune
)

// BranchesLoadedMsg carries the result of a ListBranches / PruneBranches
// RPC; both return BranchList so a single message type covers both paths.
type BranchesLoadedMsg struct {
	Branches []*pb.Branch
	Err      error
}

// BranchActionDoneMsg signals an in-flight merge/delete RPC completed.
// The model clears the pending suffix and re-fetches the list.
type BranchActionDoneMsg struct {
	Action string
	Branch string
	Err    error
}

func listBranchesCmd(conn *grpc.ClientConn, projectID string) tea.Cmd {
	return func() tea.Msg {
		if conn == nil {
			return BranchesLoadedMsg{Err: fmt.Errorf("daemon not connected")}
		}
		client := pb.NewBranchServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		resp, err := client.ListBranches(ctx, &pb.ProjectId{ProjectId: projectID})
		if err != nil {
			return BranchesLoadedMsg{Err: err}
		}
		return BranchesLoadedMsg{Branches: resp.GetBranches()}
	}
}

func mergeBranchCmd(conn *grpc.ClientConn, projectID, branch string) tea.Cmd {
	return func() tea.Msg {
		if conn == nil {
			return BranchActionDoneMsg{Action: "merge", Branch: branch, Err: fmt.Errorf("daemon not connected")}
		}
		client := pb.NewBranchServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := client.MergeBranch(ctx, &pb.MergeBranchRequest{
			ProjectId:  projectID,
			BranchName: branch,
		})
		return BranchActionDoneMsg{Action: "merge", Branch: branch, Err: err}
	}
}

func deleteBranchCmd(conn *grpc.ClientConn, projectID, branch string, force bool) tea.Cmd {
	action := "delete"
	if force {
		action = "force-delete"
	}
	return func() tea.Msg {
		if conn == nil {
			return BranchActionDoneMsg{Action: action, Branch: branch, Err: fmt.Errorf("daemon not connected")}
		}
		client := pb.NewBranchServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, err := client.DeleteBranch(ctx, &pb.BranchId{
			ProjectId:  projectID,
			BranchName: branch,
			Force:      force,
		})
		return BranchActionDoneMsg{Action: action, Branch: branch, Err: err}
	}
}

func pruneBranchesCmd(conn *grpc.ClientConn, projectID string) tea.Cmd {
	return func() tea.Msg {
		if conn == nil {
			return BranchesLoadedMsg{Err: fmt.Errorf("daemon not connected")}
		}
		client := pb.NewBranchServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		resp, err := client.PruneBranches(ctx, &pb.ProjectId{ProjectId: projectID})
		if err != nil {
			return BranchesLoadedMsg{Err: err}
		}
		return BranchesLoadedMsg{Branches: resp.GetBranches()}
	}
}

// formatRelativeAge renders a unix timestamp as a short "3h" / "2d" /
// "1w" age string. 0 / future timestamps render as "—" so we never claim
// false data when git for-each-ref returned nothing.
func formatRelativeAge(unixSec int64, now time.Time) string {
	if unixSec <= 0 {
		return "—"
	}
	delta := now.Sub(time.Unix(unixSec, 0))
	if delta < 0 {
		return "—"
	}
	switch {
	case delta < time.Minute:
		return "now"
	case delta < time.Hour:
		return fmt.Sprintf("%dm", int(delta.Minutes()))
	case delta < 24*time.Hour:
		return fmt.Sprintf("%dh", int(delta.Hours()))
	case delta < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(delta.Hours()/24))
	case delta < 30*24*time.Hour:
		return fmt.Sprintf("%dw", int(delta.Hours()/(24*7)))
	default:
		return fmt.Sprintf("%dmo", int(delta.Hours()/(24*30)))
	}
}

// IsMergedOrphan returns true when the branch has been merged into the
// default branch but its worktree directory is gone. This is the failure
// mode the overlay's `P` prune action targets — the v6.0 Phoenix race
// left tasks 0087 / 0088 in exactly this state.
func IsMergedOrphan(b *pb.Branch) bool {
	return b.GetStatus() == "merged" && b.GetWorktreePath() == ""
}

// renderBranchesOverlay builds the boxed overlay content. Pure on the
// inputs so tests can assert structure without standing up a model.
func renderBranchesOverlay(state BranchesState, projectName string, width int, now time.Time) string {
	if width < 64 {
		width = 64
	}
	if width > 92 {
		width = 92
	}

	header := branchesHeaderLine(projectName)

	var body []string
	switch {
	case state.Err != nil:
		body = append(body, lipgloss.NewStyle().Foreground(colorRed).Render("Failed to load: "+state.Err.Error()))
	case state.Loading && len(state.Branches) == 0:
		body = append(body, lipgloss.NewStyle().Foreground(colorDim).Render("Loading…"))
	case len(state.Branches) == 0:
		body = append(body, lipgloss.NewStyle().Foreground(colorDim).Render(
			"No branches yet. New tasks land on watchfire/<n>.",
		))
	default:
		body = append(body, branchesTableHeader())
		body = append(body, branchesTableSeparator())
		for i, br := range state.Branches {
			body = append(body, branchRowLine(br, i == state.Cursor, state, now))
		}
	}

	body = append(body, "")
	body = append(body, branchesRollupLine(state.Branches))

	if state.ConfirmPrompt != "" {
		body = append(body, "")
		body = append(body, lipgloss.NewStyle().Foreground(colorYellow).Render(state.ConfirmPrompt))
	} else {
		body = append(body, "")
		body = append(body, lipgloss.NewStyle().Foreground(colorDim).Render(
			"↑↓ select  m merge  x delete  X force-delete  P prune-orphans  r refresh  esc close",
		))
	}

	content := lipgloss.JoinVertical(lipgloss.Left, append([]string{header, ""}, body...)...)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorOrange).
		Padding(0, 1).
		Width(width)
	return box.Render(content)
}

func branchesHeaderLine(projectName string) string {
	if projectName == "" {
		projectName = "project"
	}
	title := lipgloss.NewStyle().Foreground(colorOrange).Bold(true).Render("Branches")
	return title + lipgloss.NewStyle().Foreground(colorDim).Render(" · "+projectName)
}

func branchesTableHeader() string {
	return columnLayout(
		lipgloss.NewStyle().Foreground(colorDim).Bold(true).Render("Branch"),
		lipgloss.NewStyle().Foreground(colorDim).Bold(true).Render("Task"),
		lipgloss.NewStyle().Foreground(colorDim).Bold(true).Render("Age"),
		lipgloss.NewStyle().Foreground(colorDim).Bold(true).Render("Status"),
		lipgloss.NewStyle().Foreground(colorDim).Bold(true).Render("Worktree"),
	)
}

func branchesTableSeparator() string {
	return lipgloss.NewStyle().Foreground(colorDim).Render(strings.Repeat("─", 64))
}

// columnLayout pins each column to a fixed width so headers align with
// rows without depending on a tablewriter dep.
func columnLayout(branch, task, age, status, worktree string) string {
	col := func(s string, w int) string {
		return lipgloss.NewStyle().Width(w).Render(s)
	}
	return col(branch, 22) + col(task, 7) + col(age, 8) + col(status, 12) + col(worktree, 12)
}

func branchRowLine(br *pb.Branch, selected bool, state BranchesState, now time.Time) string {
	taskCell := ""
	if br.GetTaskNumber() > 0 {
		taskCell = fmt.Sprintf("#%04d", br.GetTaskNumber())
	}
	age := formatRelativeAge(br.GetCommitTimestamp(), now)

	statusCell := br.GetStatus()
	if statusCell == "" {
		statusCell = "unmerged"
	}
	statusStyle := lipgloss.NewStyle().Foreground(colorYellow)
	if statusCell == "merged" {
		statusStyle = lipgloss.NewStyle().Foreground(colorGreen)
	}
	statusRendered := statusStyle.Render(statusCell)

	worktreeCell := "absent"
	if br.GetWorktreePath() != "" {
		worktreeCell = "present"
	}
	if IsMergedOrphan(br) {
		// Merged-orphan emphasis: this is the row the prune action
		// targets, so highlight it the same colour as the orange title.
		worktreeCell = lipgloss.NewStyle().Foreground(colorOrange).Render("absent*")
	}

	branchCell := br.GetName()
	if state.PendingBranch == br.GetName() && state.PendingAction != "" {
		branchCell += lipgloss.NewStyle().Foreground(colorDim).Render(" (" + state.PendingAction + "…)")
	}

	row := columnLayout(branchCell, taskCell, age, statusRendered, worktreeCell)
	prefix := "  "
	if selected {
		prefix = lipgloss.NewStyle().Foreground(colorWhite).Bold(true).Render("▶ ")
		row = selectedItemStyle.Render(row)
	}
	return prefix + row
}

func branchesRollupLine(branches []*pb.Branch) string {
	total := len(branches)
	unmerged := 0
	orphans := 0
	for _, br := range branches {
		switch br.GetStatus() {
		case "merged":
			if IsMergedOrphan(br) {
				orphans++
			}
		default:
			unmerged++
		}
	}
	body := fmt.Sprintf("%d branches · %d unmerged · %d merged-orphans", total, unmerged, orphans)
	return lipgloss.NewStyle().Foreground(colorDim).Render(body)
}

// SelectedBranch returns the row under the cursor or nil if the list is
// empty / cursor out of range. Helper for the key handler.
func (s *BranchesState) SelectedBranch() *pb.Branch {
	if s.Cursor < 0 || s.Cursor >= len(s.Branches) {
		return nil
	}
	return s.Branches[s.Cursor]
}

// MoveUp / MoveDown advance the cursor with clamping.
func (s *BranchesState) MoveUp() {
	if s.Cursor > 0 {
		s.Cursor--
	}
}

func (s *BranchesState) MoveDown() {
	if s.Cursor < len(s.Branches)-1 {
		s.Cursor++
	}
}

// SetBranches replaces the list and reclamps the cursor; used by both
// the initial load and post-action refresh.
func (s *BranchesState) SetBranches(branches []*pb.Branch) {
	s.Branches = branches
	s.Loading = false
	s.Err = nil
	if s.Cursor >= len(branches) {
		s.Cursor = len(branches) - 1
	}
	if s.Cursor < 0 {
		s.Cursor = 0
	}
}

// CountMergedOrphans returns the rollup figure used by the prune
// confirmation prompt.
func (s *BranchesState) CountMergedOrphans() int {
	n := 0
	for _, br := range s.Branches {
		if IsMergedOrphan(br) {
			n++
		}
	}
	return n
}
