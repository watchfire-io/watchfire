// Package tui — v6.0 Ember export picker overlay.
//
// The picker is opened with Ctrl+e from anywhere in the project view; it
// presents Markdown / CSV options, calls InsightsService.ExportReport on
// the daemon (scope = current project), writes the returned bytes to the
// project root, and prints a status-bar confirmation. Future per-project
// + global Insights overlays (tasks 0057/0058) trigger the same picker
// via the lower-case `e` key once those overlays land.
package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"google.golang.org/grpc"

	"github.com/watchfire-io/watchfire/internal/daemon/insights"
	pb "github.com/watchfire-io/watchfire/proto"
)

// ExportPicker holds the picker's selection state. Two formats: Markdown
// (default) and CSV. The picker is dormant unless m.activeOverlay ==
// overlayExport.
type ExportPicker struct {
	selected int // 0 = Markdown, 1 = CSV

	// Scope drives which RPC variant we send when the user picks a format.
	// scopeKind == "project" → ProjectId set; scopeKind == "single_task"
	// → SingleTask set; scopeKind == "global" → Global=true.
	scopeKind  string
	projectID  string
	taskNumber int32

	// outputDir is where the rendered file lands. For per-project /
	// single-task scopes this is the project root; for global it's the
	// current working directory (per the spec).
	outputDir string
}

// NewExportPicker constructs a default-state picker.
func NewExportPicker() *ExportPicker {
	return &ExportPicker{selected: 0}
}

// OpenForProject configures the picker to target the per-project export
// scope. Call this immediately before flipping m.activeOverlay = overlayExport.
func (p *ExportPicker) OpenForProject(projectID, projectPath string) {
	p.scopeKind = "project"
	p.projectID = projectID
	p.taskNumber = 0
	p.outputDir = projectPath
	p.selected = 0
}

// OpenForSingleTask targets a one-task export — used by the future per-task
// diff overlay (task 0055) once it lands.
func (p *ExportPicker) OpenForSingleTask(projectID string, taskNumber int32, projectPath string) {
	p.scopeKind = "single_task"
	p.projectID = projectID
	p.taskNumber = taskNumber
	p.outputDir = projectPath
	p.selected = 0
}

// OpenForGlobal targets the fleet-wide rollup export.
func (p *ExportPicker) OpenForGlobal() {
	p.scopeKind = "global"
	p.projectID = ""
	p.taskNumber = 0
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	p.outputDir = cwd
	p.selected = 0
}

// MoveUp / MoveDown wrap around so the picker feels native.
func (p *ExportPicker) MoveUp() {
	if p.selected > 0 {
		p.selected--
	} else {
		p.selected = 1
	}
}

func (p *ExportPicker) MoveDown() {
	p.selected = (p.selected + 1) % 2
}

// Format returns the proto enum value for the currently-highlighted row.
func (p *ExportPicker) Format() pb.ExportFormat {
	if p.selected == 1 {
		return pb.ExportFormat_CSV
	}
	return pb.ExportFormat_MARKDOWN
}

// View renders the picker overlay. Width is fixed (~24 cols) because the
// labels are short and a wider box looks lopsided over the dim background.
func (p *ExportPicker) View() string {
	const innerW = 22
	row := func(label string, idx int) string {
		marker := "  "
		st := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "240", Dark: "245"})
		if idx == p.selected {
			marker = "❯ "
			st = lipgloss.NewStyle().Foreground(colorOrange).Bold(true)
		}
		return st.Render(marker+label) + lipgloss.NewStyle().Render("")
	}
	body := []string{
		row("Markdown (.md)", 0),
		row("CSV (.csv)", 1),
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorOrange).
		Padding(0, 1).
		Width(innerW)

	title := lipgloss.NewStyle().Foreground(colorOrange).Bold(true).Render("Export")
	hint := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "240", Dark: "245"}).
		Render("↑/↓ · Enter · Esc")

	content := title + "\n" + body[0] + "\n" + body[1] + "\n" + hint
	return box.Render(content)
}

// runExport calls ExportReport on the daemon and writes the returned bytes
// to outputDir. Returns the absolute filename written so the caller can
// surface a status message.
func runExport(conn *grpc.ClientConn, p ExportPicker) tea.Cmd {
	return func() tea.Msg {
		if conn == nil {
			return ExportFailedMsg{Err: fmt.Errorf("daemon not connected")}
		}
		client := pb.NewInsightsServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		req := &pb.ExportReportRequest{
			Format: p.Format(),
			Meta:   &pb.RequestMeta{Origin: "tui"},
		}
		switch p.scopeKind {
		case "project":
			req.Scope = &pb.ExportReportRequest_ProjectId{ProjectId: p.projectID}
		case "single_task":
			req.Scope = &pb.ExportReportRequest_SingleTask{
				SingleTask: fmt.Sprintf("%s:%d", p.projectID, p.taskNumber),
			}
		case "global":
			req.Scope = &pb.ExportReportRequest_Global{Global: true}
		default:
			return ExportFailedMsg{Err: fmt.Errorf("unknown export scope %q", p.scopeKind)}
		}

		resp, err := client.ExportReport(ctx, req)
		if err != nil {
			return ExportFailedMsg{Err: err}
		}

		dir := p.outputDir
		if dir == "" {
			dir = "."
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return ExportFailedMsg{Err: fmt.Errorf("create %s: %w", dir, err)}
		}
		path := filepath.Join(dir, resp.Filename)
		if err := os.WriteFile(path, resp.Content, 0o644); err != nil {
			return ExportFailedMsg{Err: fmt.Errorf("write %s: %w", path, err)}
		}
		return ExportCompletedMsg{Filename: resp.Filename, Path: path}
	}
}

// ExportCompletedMsg is dispatched on a successful export. The TUI's
// msghandler converts it into a status-bar confirmation.
type ExportCompletedMsg struct {
	Filename string
	Path     string
}

// ExportFailedMsg is dispatched when the export RPC or file write fails.
type ExportFailedMsg struct {
	Err error
}

// Compile-time assertion that the picker can produce filenames matching
// the canonical `watchfire-<scope>-<YYYY-MM-DD>.<ext>` shape — the daemon
// authors them, so we just make sure the import wiring stays clean. This
// silences "unused import" if we ever drop the Format() helper.
var _ = insights.MimeType
