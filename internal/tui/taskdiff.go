// Package tui — v6.0 Ember inline diff viewer overlay.
//
// Press `d` on a focused completed task to open the overlay. Two-pane:
// left = file list, right = diff body. Lipgloss colours the lines —
// green for `+`, red for `-`, dim for context. No syntax highlighting in
// v6.0 — keeping the dep tree light is the contract.
//
// Keys (overlay-active):
//   j / down   next file
//   k / up     prev file
//   /          start filter (Enter / Esc to confirm / cancel)
//   r          refresh diff
//   esc / q    close overlay
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

// taskDiffState carries everything the overlay needs across renders.
// Loaded path: while `Data == nil && Err == nil` we show "Loading…".
type taskDiffState struct {
	TaskNumber  int32
	TaskTitle   string
	Data        *pb.FileDiffSet
	Err         error
	Selected    int
	Filter      string
	FilterMode  bool
	Loading     bool
	scrollFiles int
}

// TaskDiffLoadedMsg is dispatched when the daemon GetTaskDiff RPC returns.
type TaskDiffLoadedMsg struct {
	TaskNumber int32
	Data       *pb.FileDiffSet
	Err        error
}

// loadTaskDiffCmd issues the gRPC fetch.
func loadTaskDiffCmd(conn *grpc.ClientConn, projectID string, taskNumber int32) tea.Cmd {
	return func() tea.Msg {
		if conn == nil {
			return TaskDiffLoadedMsg{TaskNumber: taskNumber, Err: fmt.Errorf("daemon not connected")}
		}
		client := pb.NewInsightsServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		resp, err := client.GetTaskDiff(ctx, &pb.GetTaskDiffRequest{
			Meta:       &pb.RequestMeta{Origin: "tui"},
			ProjectId:  projectID,
			TaskNumber: taskNumber,
		})
		if err != nil {
			return TaskDiffLoadedMsg{TaskNumber: taskNumber, Err: err}
		}
		return TaskDiffLoadedMsg{TaskNumber: taskNumber, Data: resp}
	}
}

// filteredFiles returns the indexable subset of the diff that the file
// pane currently shows. With an empty filter, this is the full list.
func (s *taskDiffState) filteredFiles() []*pb.FileDiff {
	if s.Data == nil {
		return nil
	}
	if strings.TrimSpace(s.Filter) == "" {
		return s.Data.GetFiles()
	}
	needle := strings.ToLower(s.Filter)
	out := make([]*pb.FileDiff, 0, len(s.Data.GetFiles()))
	for _, f := range s.Data.GetFiles() {
		if strings.Contains(strings.ToLower(f.GetPath()), needle) {
			out = append(out, f)
		}
	}
	return out
}

func (s *taskDiffState) selectedFile() *pb.FileDiff {
	files := s.filteredFiles()
	if len(files) == 0 {
		return nil
	}
	if s.Selected < 0 {
		s.Selected = 0
	}
	if s.Selected >= len(files) {
		s.Selected = len(files) - 1
	}
	return files[s.Selected]
}

// handleTaskDiffKey routes keys while the diff overlay is open.
func (m *Model) handleTaskDiffKey(msg tea.KeyMsg) tea.Cmd {
	s := &m.taskDiff

	if s.FilterMode {
		switch msg.Type {
		case tea.KeyEscape:
			s.FilterMode = false
			s.Filter = ""
			s.Selected = 0
			return nil
		case tea.KeyEnter:
			s.FilterMode = false
			return nil
		case tea.KeyBackspace:
			if s.Filter != "" {
				s.Filter = s.Filter[:len(s.Filter)-1]
				s.Selected = 0
			}
			return nil
		case tea.KeyRunes:
			s.Filter += string(msg.Runes)
			s.Selected = 0
			return nil
		case tea.KeySpace:
			s.Filter += " "
			s.Selected = 0
			return nil
		}
		return nil
	}

	switch msg.String() {
	case "esc", "q":
		m.activeOverlay = overlayNone
		return nil
	case "j", "down":
		s.Selected++
		s.selectedFile() // clamps
		return nil
	case "k", "up":
		s.Selected--
		s.selectedFile() // clamps
		return nil
	case "/":
		s.FilterMode = true
		s.Filter = ""
		s.Selected = 0
		return nil
	case "r":
		s.Loading = true
		s.Data = nil
		s.Err = nil
		if m.conn != nil {
			return loadTaskDiffCmd(m.conn, m.projectID, s.TaskNumber)
		}
	}
	return nil
}

// openTaskDiffOverlay flips the overlay on, resets state, and dispatches
// the gRPC fetch. Returns nil if no task is selected or it isn't done.
func (m *Model) openTaskDiffOverlay() tea.Cmd {
	t := m.taskList.SelectedTask()
	if t == nil {
		return nil
	}
	m.taskDiff = taskDiffState{
		TaskNumber: t.TaskNumber,
		TaskTitle:  t.Title,
		Loading:    true,
	}
	m.activeOverlay = overlayTaskDiff
	if m.conn == nil {
		return nil
	}
	return loadTaskDiffCmd(m.conn, m.projectID, t.TaskNumber)
}

// renderTaskDiffOverlay paints the two-pane overlay (file list + diff body).
func renderTaskDiffOverlay(s taskDiffState, width, height int) string {
	if width < 60 {
		width = 60
	}
	if height < 16 {
		height = 16
	}

	header := taskDiffHeaderLine(s)
	statusLine := taskDiffStatusLine(s)

	bodyHeight := height - 6 // borders, header, status
	if bodyHeight < 6 {
		bodyHeight = 6
	}

	leftWidth := 28
	if leftWidth > width/3 {
		leftWidth = width / 3
	}
	rightWidth := width - leftWidth - 5 // 4 borders + 1 sep
	if rightWidth < 30 {
		rightWidth = 30
	}

	var body string
	switch {
	case s.Err != nil:
		body = lipgloss.NewStyle().
			Foreground(colorYellow).
			Width(width-2).
			Height(bodyHeight).
			Render("Failed to load: " + s.Err.Error())
	case s.Data == nil || s.Loading:
		body = lipgloss.NewStyle().
			Foreground(colorDim).
			Width(width-2).
			Height(bodyHeight).
			Render("Loading…")
	case len(s.Data.GetFiles()) == 0:
		body = lipgloss.NewStyle().
			Foreground(colorDim).
			Width(width-2).
			Height(bodyHeight).
			Render("No changes — this task didn't touch any files.")
	default:
		left := renderTaskDiffFileList(s, leftWidth, bodyHeight)
		right := renderTaskDiffBody(s.selectedFile(), rightWidth, bodyHeight)
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, separator(bodyHeight), right)
	}

	parts := []string{header, "", body, "", statusLine}
	content := lipgloss.JoinVertical(lipgloss.Left, parts...)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorOrange).
		Padding(0, 1).
		Width(width)

	return box.Render(content)
}

func taskDiffHeaderLine(s taskDiffState) string {
	title := lipgloss.NewStyle().Foreground(colorOrange).Bold(true).Render(
		fmt.Sprintf("Diff · #%04d", s.TaskNumber),
	)
	if s.TaskTitle != "" {
		title += lipgloss.NewStyle().Foreground(colorDim).Render(" · " + s.TaskTitle)
	}
	if s.Data != nil {
		summary := lipgloss.NewStyle().Foreground(colorDim).Render(fmt.Sprintf(
			" · %d files, +%d -%d",
			len(s.Data.GetFiles()),
			s.Data.GetTotalAdditions(),
			s.Data.GetTotalDeletions(),
		))
		title += summary
	}
	return title
}

func taskDiffStatusLine(s taskDiffState) string {
	if s.FilterMode {
		return lipgloss.NewStyle().Foreground(colorCyan).Render(
			"/" + s.Filter + "▏",
		)
	}
	hint := "j/k navigate · / filter · r refresh · esc close"
	if s.Data != nil && s.Data.GetTruncated() {
		hint = "diff truncated · " + hint
	}
	return lipgloss.NewStyle().Foreground(colorDim).Render(hint)
}

func renderTaskDiffFileList(s taskDiffState, width, height int) string {
	files := s.filteredFiles()
	if len(files) == 0 {
		return lipgloss.NewStyle().
			Width(width).
			Height(height).
			Foreground(colorDim).
			Render("No matches.")
	}

	// Scroll so the selection stays in view.
	if s.Selected < 0 {
		s.Selected = 0
	}
	if s.Selected >= len(files) {
		s.Selected = len(files) - 1
	}

	maxRows := height
	start := 0
	if s.Selected >= maxRows {
		start = s.Selected - maxRows + 1
	}
	end := start + maxRows
	if end > len(files) {
		end = len(files)
	}

	rowStyle := lipgloss.NewStyle().Width(width)
	selectedStyle := rowStyle.Foreground(colorWhite).Bold(true)
	mutedStyle := rowStyle.Foreground(colorDim)

	rows := make([]string, 0, maxRows)
	for i := start; i < end; i++ {
		f := files[i]
		marker := fileStatusMarker(f.GetStatus())
		adds, dels := countLineKinds(f)
		counts := ""
		if adds > 0 {
			counts += lipgloss.NewStyle().Foreground(colorGreen).Render(fmt.Sprintf(" +%d", adds))
		}
		if dels > 0 {
			counts += lipgloss.NewStyle().Foreground(colorRed).Render(fmt.Sprintf(" -%d", dels))
		}
		path := truncatePath(f.GetPath(), width-len(marker)-len(counts)-2)
		row := fmt.Sprintf("%s %s%s", marker, path, counts)
		if i == s.Selected {
			rows = append(rows, selectedStyle.Render("▸ "+row))
		} else {
			rows = append(rows, mutedStyle.Render("  "+row))
		}
	}
	for len(rows) < maxRows {
		rows = append(rows, "")
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

func fileStatusMarker(status pb.FileDiff_Status) string {
	switch status {
	case pb.FileDiff_ADDED:
		return lipgloss.NewStyle().Foreground(colorGreen).Render("A")
	case pb.FileDiff_DELETED:
		return lipgloss.NewStyle().Foreground(colorRed).Render("D")
	case pb.FileDiff_RENAMED:
		return lipgloss.NewStyle().Foreground(colorCyan).Render("R")
	default:
		return lipgloss.NewStyle().Foreground(colorYellow).Render("M")
	}
}

func countLineKinds(f *pb.FileDiff) (adds, dels int) {
	for _, h := range f.GetHunks() {
		for _, l := range h.GetLines() {
			switch l.GetKind() {
			case pb.DiffLine_ADD:
				adds++
			case pb.DiffLine_DEL:
				dels++
			}
		}
	}
	return adds, dels
}

func truncatePath(path string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(path) <= max {
		return path
	}
	if max < 4 {
		return path[:max]
	}
	return "…" + path[len(path)-(max-1):]
}

func separator(height int) string {
	style := lipgloss.NewStyle().Foreground(colorDim)
	rows := make([]string, height)
	for i := range rows {
		rows[i] = style.Render("│")
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

func renderTaskDiffBody(file *pb.FileDiff, width, height int) string {
	if file == nil {
		return lipgloss.NewStyle().Width(width).Height(height).Foreground(colorDim).Render("(no file selected)")
	}

	rows := []string{
		lipgloss.NewStyle().Bold(true).Render(file.GetPath()),
	}
	if file.GetOldPath() != "" && file.GetOldPath() != file.GetPath() {
		rows = append(rows, lipgloss.NewStyle().Foreground(colorDim).Render("renamed from "+file.GetOldPath()))
	}
	rows = append(rows, "")

	hunks := file.GetHunks()
	if len(hunks) == 1 && len(hunks[0].GetLines()) == 0 {
		rows = append(rows, lipgloss.NewStyle().Foreground(colorDim).Render(
			defaultIfEmpty(hunks[0].GetHeader(), "Binary file changed"),
		))
	} else if len(hunks) == 0 {
		rows = append(rows, lipgloss.NewStyle().Foreground(colorDim).Render("No textual changes."))
	} else {
		for _, h := range hunks {
			rows = append(rows, renderHunkRows(h, width)...)
			if len(rows) >= height {
				break
			}
		}
	}

	if len(rows) > height {
		rows = rows[:height]
	}
	for len(rows) < height {
		rows = append(rows, "")
	}
	return lipgloss.NewStyle().Width(width).Render(lipgloss.JoinVertical(lipgloss.Left, rows...))
}

func renderHunkRows(h *pb.Hunk, width int) []string {
	headerLine := lipgloss.NewStyle().Foreground(colorDim).Render(fmt.Sprintf(
		"@@ -%d,%d +%d,%d @@ %s",
		h.GetOldStart(), h.GetOldLines(),
		h.GetNewStart(), h.GetNewLines(),
		h.GetHeader(),
	))
	out := []string{headerLine}
	addStyle := lipgloss.NewStyle().Foreground(colorGreen)
	delStyle := lipgloss.NewStyle().Foreground(colorRed)
	ctxStyle := lipgloss.NewStyle().Foreground(colorDim)
	for _, l := range h.GetLines() {
		text := l.GetText()
		if w := lipgloss.Width(text); w > width-2 {
			text = truncatePath(text, width-2)
		}
		switch l.GetKind() {
		case pb.DiffLine_ADD:
			out = append(out, addStyle.Render("+ "+text))
		case pb.DiffLine_DEL:
			out = append(out, delStyle.Render("- "+text))
		default:
			out = append(out, ctxStyle.Render("  "+text))
		}
	}
	return out
}

func defaultIfEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
