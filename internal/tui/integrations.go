package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"

	pb "github.com/watchfire-io/watchfire/proto"
)

// integrationsRowKind disambiguates rows in the IntegrationsForm list.
// The list shows webhook + slack + discord endpoints as one flat list; a
// trailing "GitHub" row is always present (single-instance config).
type integrationsRowKind int

const (
	integrationsRowWebhook integrationsRowKind = iota
	integrationsRowSlack
	integrationsRowDiscord
	integrationsRowGitHub
)

// integrationsRow captures one selectable row in the overlay list.
type integrationsRow struct {
	Kind     integrationsRowKind
	ID       string // empty for the GitHub single-instance row
	Label    string
	URLLabel string // masked
	Events   *pb.IntegrationEvents
	Muted    int
}

// integrationsAddField captures one input field in the stacked add-form
// sequence. The form prompts for URL → label → events checkboxes →
// project-mute multi-select; current step is `step`.
type integrationsAddField int

const (
	addFieldKind integrationsAddField = iota
	addFieldURL
	addFieldLabel
	addFieldEvents
	addFieldMutes
	addFieldDone
)

// IntegrationsForm is the TUI overlay for the v7.0 Relay integrations
// settings UI. Mirrors the layout patterns of `globalsettings.go` —
// single Bubbletea component, owns the textinput when editing, exposes
// View() + Update() helpers.
//
// State machine:
//   - mode "list" — rows + focus cursor, key handlers (a/e/d/t/Esc)
//   - mode "add"  — stacked form (URL → label → events → mutes)
//   - mode "confirm-delete" — y/n prompt
//
// The form does not own gRPC plumbing; the model layer dispatches the
// RPC commands from the key handler.
type IntegrationsForm struct {
	cfg          *pb.IntegrationsConfig
	rows         []integrationsRow
	cursor       int
	mode         integrationsFormMode
	width        int
	input        textinput.Model
	addStep      integrationsAddField
	addKind      integrationsRowKind
	addURL       string
	addLabel     string
	addEvents    pb.IntegrationEvents
	addMutes     []string
	statusLine   string
	deleteCursor int
	// projectIDs is the list of registered project IDs surfaced in the
	// project-mute multi-select. The model layer populates this from the
	// active GUI's projects-store equivalent — for the TUI we wire it
	// later by extending getProjectsCmd; today we leave it empty (the
	// step is skipped if empty).
	projectIDs []string
}

type integrationsFormMode int

const (
	integrationsModeList integrationsFormMode = iota
	integrationsModeAdd
	integrationsModeConfirmDelete
)

// NewIntegrationsForm returns an empty form ready to be Load()ed.
func NewIntegrationsForm() *IntegrationsForm {
	ti := textinput.New()
	ti.CharLimit = 256
	return &IntegrationsForm{
		cfg:   &pb.IntegrationsConfig{Github: &pb.GitHubIntegration{}},
		input: ti,
		mode:  integrationsModeList,
	}
}

// Load populates the form from a freshly-fetched IntegrationsConfig.
func (f *IntegrationsForm) Load(cfg *pb.IntegrationsConfig) {
	if cfg == nil {
		cfg = &pb.IntegrationsConfig{Github: &pb.GitHubIntegration{}}
	}
	f.cfg = cfg
	f.rebuildRows()
	if f.cursor >= len(f.rows) {
		f.cursor = len(f.rows) - 1
	}
	if f.cursor < 0 {
		f.cursor = 0
	}
}

// SetWidth applies the available horizontal space to the form. Called
// from the model on each render before View().
func (f *IntegrationsForm) SetWidth(w int) {
	if w > 0 {
		f.width = w
	}
}

// SetProjectIDs lets the model layer hand in the current project list
// so the project-mute multi-select can show real options.
func (f *IntegrationsForm) SetProjectIDs(ids []string) {
	f.projectIDs = append([]string(nil), ids...)
}

// Mode returns the current form mode (list / add / confirm-delete).
func (f *IntegrationsForm) Mode() integrationsFormMode { return f.mode }

// Reset returns the form to a list-mode initial state.
func (f *IntegrationsForm) Reset() {
	f.mode = integrationsModeList
	f.cursor = 0
	f.addStep = addFieldKind
	f.addURL = ""
	f.addLabel = ""
	f.addEvents = pb.IntegrationEvents{TaskFailed: true, RunComplete: true}
	f.addMutes = nil
	f.statusLine = ""
	f.input.Blur()
	f.input.SetValue("")
}

// SetStatus sets a transient status line shown at the bottom of the
// overlay (e.g. "Sent — HTTP 200" after a Test).
func (f *IntegrationsForm) SetStatus(s string) {
	f.statusLine = s
}

// CurrentRow returns the row under the cursor (or zero-value if list is
// empty). Used by the model when dispatching e/d/t actions.
func (f *IntegrationsForm) CurrentRow() integrationsRow {
	if f.cursor < 0 || f.cursor >= len(f.rows) {
		return integrationsRow{}
	}
	return f.rows[f.cursor]
}

// MoveUp / MoveDown advance the cursor.
func (f *IntegrationsForm) MoveUp() {
	if f.cursor > 0 {
		f.cursor--
	}
}
func (f *IntegrationsForm) MoveDown() {
	if f.cursor < len(f.rows)-1 {
		f.cursor++
	}
}

// StartAdd transitions to the stacked add-form starting at the kind
// picker step.
func (f *IntegrationsForm) StartAdd() {
	f.mode = integrationsModeAdd
	f.addStep = addFieldKind
	f.addKind = integrationsRowWebhook
	f.addURL = ""
	f.addLabel = ""
	f.addEvents = pb.IntegrationEvents{TaskFailed: true, RunComplete: true}
	f.addMutes = nil
}

// CycleAddKind moves the add-form's kind picker forward / backward.
func (f *IntegrationsForm) CycleAddKind(delta int) {
	if f.addStep != addFieldKind {
		return
	}
	idx := int(f.addKind) + delta
	if idx < 0 {
		idx = int(integrationsRowGitHub)
	}
	if idx > int(integrationsRowGitHub) {
		idx = 0
	}
	f.addKind = integrationsRowKind(idx)
}

// AdvanceAdd advances the stacked form one step. Returns true when the
// final step has been confirmed (so the caller can dispatch Save).
func (f *IntegrationsForm) AdvanceAdd() bool {
	switch f.addStep {
	case addFieldKind:
		// GitHub single-instance: skip URL + label, jump to scopes via
		// the events placeholder step (we reuse the events field for
		// "enabled / draft_default" toggles).
		if f.addKind == integrationsRowGitHub {
			f.addStep = addFieldEvents
			return false
		}
		f.input.Reset()
		f.input.Placeholder = "https://hooks.slack.com/services/..."
		f.input.SetValue("")
		f.input.Focus()
		f.addStep = addFieldURL
	case addFieldURL:
		f.addURL = strings.TrimSpace(f.input.Value())
		if f.addURL == "" {
			return false
		}
		f.input.Blur()
		f.input.SetValue("")
		f.input.Placeholder = "Friendly name (optional)"
		f.input.Focus()
		f.addStep = addFieldLabel
	case addFieldLabel:
		f.addLabel = strings.TrimSpace(f.input.Value())
		f.input.Blur()
		f.addStep = addFieldEvents
	case addFieldEvents:
		// Events / GitHub-toggles are confirmed in-step via space; Tab
		// advances to mutes.
		f.addStep = addFieldMutes
	case addFieldMutes:
		f.addStep = addFieldDone
		return true
	}
	return false
}

// ToggleAddEvent flips one event bit in the add form. Index maps to
// task_failed / run_complete / weekly_digest.
func (f *IntegrationsForm) ToggleAddEvent(idx int) {
	if f.addStep != addFieldEvents {
		return
	}
	if f.addKind == integrationsRowGitHub {
		// Reused step for GitHub — toggles enabled / draft_default.
		switch idx {
		case 0:
			f.addEvents.TaskFailed = !f.addEvents.TaskFailed
		case 1:
			f.addEvents.RunComplete = !f.addEvents.RunComplete
		}
		return
	}
	switch idx {
	case 0:
		f.addEvents.TaskFailed = !f.addEvents.TaskFailed
	case 1:
		f.addEvents.RunComplete = !f.addEvents.RunComplete
	case 2:
		f.addEvents.WeeklyDigest = !f.addEvents.WeeklyDigest
	}
}

// CancelAdd aborts the add form and returns to list mode.
func (f *IntegrationsForm) CancelAdd() {
	f.mode = integrationsModeList
	f.addStep = addFieldKind
	f.input.Blur()
	f.input.SetValue("")
}

// StartDeleteConfirm flips into a y/n confirm prompt for the current row.
func (f *IntegrationsForm) StartDeleteConfirm() {
	if len(f.rows) == 0 {
		return
	}
	f.mode = integrationsModeConfirmDelete
	f.deleteCursor = f.cursor
}

// FinishDeleteConfirm returns the row that was queued for deletion and
// flips back to list mode. Caller dispatches the RPC.
func (f *IntegrationsForm) FinishDeleteConfirm(confirm bool) (integrationsRow, bool) {
	row := integrationsRow{}
	ok := false
	if confirm && f.deleteCursor >= 0 && f.deleteCursor < len(f.rows) {
		row = f.rows[f.deleteCursor]
		ok = true
	}
	f.mode = integrationsModeList
	return row, ok
}

// InputModel exposes the embedded textinput so the model's update
// dispatch can forward key events.
func (f *IntegrationsForm) InputModel() *textinput.Model { return &f.input }

// IsEditing reports whether the form is in a mode that consumes raw
// key events (vs. the list-mode shortcuts a/e/d/t).
func (f *IntegrationsForm) IsEditing() bool { return f.mode != integrationsModeList }

// AddSnapshot returns the staged add-form values for the model layer
// to roll into a SaveIntegrationRequest.
func (f *IntegrationsForm) AddSnapshot() (kind integrationsRowKind, url, label string, events *pb.IntegrationEvents, mutes []string) {
	ev := f.addEvents
	return f.addKind, f.addURL, f.addLabel, &ev, append([]string(nil), f.addMutes...)
}

// rebuildRows flattens the IntegrationsConfig into the row list.
func (f *IntegrationsForm) rebuildRows() {
	rows := make([]integrationsRow, 0)
	for _, w := range f.cfg.GetWebhooks() {
		rows = append(rows, integrationsRow{
			Kind:     integrationsRowWebhook,
			ID:       w.GetId(),
			Label:    f.deriveLabel(w.GetLabel(), "Webhook"),
			URLLabel: w.GetUrlLabel(),
			Events:   w.GetEnabledEvents(),
			Muted:    len(w.GetProjectMuteIds()),
		})
	}
	for _, sl := range f.cfg.GetSlack() {
		rows = append(rows, integrationsRow{
			Kind:     integrationsRowSlack,
			ID:       sl.GetId(),
			Label:    f.deriveLabel(sl.GetLabel(), "Slack"),
			URLLabel: sl.GetUrlLabel(),
			Events:   sl.GetEnabledEvents(),
			Muted:    len(sl.GetProjectMuteIds()),
		})
	}
	for _, dc := range f.cfg.GetDiscord() {
		rows = append(rows, integrationsRow{
			Kind:     integrationsRowDiscord,
			ID:       dc.GetId(),
			Label:    f.deriveLabel(dc.GetLabel(), "Discord"),
			URLLabel: dc.GetUrlLabel(),
			Events:   dc.GetEnabledEvents(),
			Muted:    len(dc.GetProjectMuteIds()),
		})
	}
	// GitHub single-instance row is always present.
	rows = append(rows, integrationsRow{
		Kind:  integrationsRowGitHub,
		Label: githubRowLabel(f.cfg.GetGithub()),
	})
	f.rows = rows
}

func (f *IntegrationsForm) deriveLabel(label, fallback string) string {
	if label != "" {
		return label
	}
	return fallback
}

func githubRowLabel(g *pb.GitHubIntegration) string {
	if g == nil || !g.GetEnabled() {
		return "GitHub Auto-PR (disabled)"
	}
	scope := "all projects"
	if n := len(g.GetProjectScopes()); n > 0 {
		scope = fmt.Sprintf("%d project(s)", n)
	}
	if g.GetDraftDefault() {
		return fmt.Sprintf("GitHub Auto-PR — draft, %s", scope)
	}
	return fmt.Sprintf("GitHub Auto-PR — %s", scope)
}

func kindLabel(k integrationsRowKind) string {
	switch k {
	case integrationsRowWebhook:
		return "Webhook"
	case integrationsRowSlack:
		return "Slack"
	case integrationsRowDiscord:
		return "Discord"
	case integrationsRowGitHub:
		return "GitHub"
	}
	return "?"
}

// View renders the overlay content (without the surrounding box —
// model.View applies the overlay style).
func (f *IntegrationsForm) View() string {
	switch f.mode {
	case integrationsModeAdd:
		return f.renderAdd()
	case integrationsModeConfirmDelete:
		return f.renderConfirmDelete()
	default:
		return f.renderList()
	}
}

func (f *IntegrationsForm) renderList() string {
	var b strings.Builder
	b.WriteString(overlayTitleStyle.Render("Integrations"))
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Foreground(colorDim).Render("Forward Watchfire's notifications to outside channels."))
	b.WriteString("\n\n")

	if len(f.rows) == 0 {
		b.WriteString("(none configured)\n")
	} else {
		for i, row := range f.rows {
			labelWidth := 22
			// GitHub's row label embeds the entire status string ("Auto-PR
			// — draft, all projects"), so let it take the URL column too.
			if row.Kind == integrationsRowGitHub {
				labelWidth = 50
			}
			line := fmt.Sprintf("%-8s %-*s %s", kindLabel(row.Kind), labelWidth, truncate(row.Label, labelWidth), row.URLLabel)
			line = strings.TrimRight(line, " ")
			if row.Events != nil {
				line += "  " + eventChips(row.Events)
			}
			if row.Muted > 0 {
				line += fmt.Sprintf("  · %d muted", row.Muted)
			}
			if i == f.cursor {
				line = settingsCursorStyle.Render("▸ " + line)
			} else {
				line = "  " + line
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	if f.statusLine != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(colorCyan).Render(f.statusLine))
		b.WriteString("\n")
	}
	b.WriteString(lipgloss.NewStyle().Foreground(colorDim).Render("a add · e edit · d delete · t test · ↑↓ move · Esc close"))
	return b.String()
}

func (f *IntegrationsForm) renderAdd() string {
	var b strings.Builder
	b.WriteString(overlayTitleStyle.Render("Add integration"))
	b.WriteString("\n\n")

	switch f.addStep {
	case addFieldKind:
		b.WriteString("Kind: " + settingsToggleOn.Render(kindLabel(f.addKind)))
		b.WriteString("\n\n")
		b.WriteString(lipgloss.NewStyle().Foreground(colorDim).Render("← / → cycle · Tab next · Esc cancel"))
	case addFieldURL:
		b.WriteString("URL:\n")
		b.WriteString(f.input.View())
		b.WriteString("\n\n")
		b.WriteString(lipgloss.NewStyle().Foreground(colorDim).Render("Tab next · Esc cancel"))
	case addFieldLabel:
		b.WriteString(fmt.Sprintf("URL: %s\n\n", maskForDisplay(f.addURL)))
		b.WriteString("Label:\n")
		b.WriteString(f.input.View())
		b.WriteString("\n\n")
		b.WriteString(lipgloss.NewStyle().Foreground(colorDim).Render("Tab next · Esc cancel"))
	case addFieldEvents:
		if f.addKind == integrationsRowGitHub {
			b.WriteString(checkbox("Enabled", f.addEvents.TaskFailed) + "\n")
			b.WriteString(checkbox("Open as draft", f.addEvents.RunComplete) + "\n")
			b.WriteString("\n" + lipgloss.NewStyle().Foreground(colorDim).Render("Space toggles · Tab next · Esc cancel"))
		} else {
			b.WriteString(checkbox("TASK_FAILED", f.addEvents.TaskFailed) + "\n")
			b.WriteString(checkbox("RUN_COMPLETE", f.addEvents.RunComplete) + "\n")
			b.WriteString(checkbox("WEEKLY_DIGEST", f.addEvents.WeeklyDigest) + "\n")
			b.WriteString("\n" + lipgloss.NewStyle().Foreground(colorDim).Render("1/2/3 toggle event · Tab next · Esc cancel"))
		}
	case addFieldMutes:
		if len(f.projectIDs) == 0 {
			b.WriteString("(no projects to mute)\n")
		} else {
			for _, p := range f.projectIDs {
				b.WriteString(checkbox(p, contains(f.addMutes, p)) + "\n")
			}
		}
		b.WriteString("\n" + lipgloss.NewStyle().Foreground(colorDim).Render("Enter saves · Esc cancel"))
	}
	return b.String()
}

func (f *IntegrationsForm) renderConfirmDelete() string {
	var b strings.Builder
	b.WriteString(overlayTitleStyle.Render("Delete integration?"))
	b.WriteString("\n\n")
	row := integrationsRow{}
	if f.deleteCursor >= 0 && f.deleteCursor < len(f.rows) {
		row = f.rows[f.deleteCursor]
	}
	b.WriteString(fmt.Sprintf("%s — %s\n\n", kindLabel(row.Kind), row.Label))
	b.WriteString(lipgloss.NewStyle().Foreground(colorDim).Render("y confirm · n cancel"))
	return b.String()
}

func eventChips(e *pb.IntegrationEvents) string {
	if e == nil {
		return ""
	}
	parts := make([]string, 0, 3)
	if e.GetTaskFailed() {
		parts = append(parts, "FAIL")
	}
	if e.GetRunComplete() {
		parts = append(parts, "DONE")
	}
	if e.GetWeeklyDigest() {
		parts = append(parts, "WEEK")
	}
	if len(parts) == 0 {
		return lipgloss.NewStyle().Foreground(colorDim).Render("(no events)")
	}
	return lipgloss.NewStyle().Foreground(colorCyan).Render("[" + strings.Join(parts, "·") + "]")
}

func checkbox(label string, on bool) string {
	box := "[ ]"
	style := settingsToggleOff
	if on {
		box = "[x]"
		style = settingsToggleOn
	}
	return style.Render(box) + " " + label
}

func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return s[:max-1] + "…"
}

func maskForDisplay(url string) string {
	if len(url) <= 24 {
		return url
	}
	return url[:18] + "…" + url[len(url)-6:]
}

func contains(s []string, v string) bool {
	for _, e := range s {
		if e == v {
			return true
		}
	}
	return false
}
