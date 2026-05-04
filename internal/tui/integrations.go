package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

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

	// v8.0 Echo — Inbound tab state. Lives alongside the v7.0 outbound
	// list so the same overlay surfaces both directions; users press Tab
	// to swap between Outbound and Inbound. Inbound shape: a vertical
	// stack of editable rows (listen address, public URL, per-provider
	// secret blocks) plus a status pill at the top.
	tab            integrationsTab
	inboundStatus  *pb.InboundStatus
	inboundCursor  int                  // index into the editable inbound rows below
	inboundEditing bool                 // true when the textinput is consuming keystrokes
	inboundInput   textinput.Model      // shared input for whichever inbound row is being edited
	inboundDraft   pb.InboundConfig     // local edit buffer; flushed via saveInboundConfigCmd on Enter
}

// integrationsTab disambiguates the two views inside the integrations
// overlay. Outbound is the v7.0 Relay endpoints list (default); Inbound
// is the v8.0 Echo listener configuration.
type integrationsTab int

const (
	integrationsTabOutbound integrationsTab = iota
	integrationsTabInbound
)

// inboundRow indexes the editable rows in the Inbound tab. They render
// in this order top-to-bottom; the cursor (`inboundCursor`) selects one
// at a time and Enter starts editing it.
type inboundRow int

const (
	inboundRowEnabled inboundRow = iota // master toggle (Disabled inverted)
	inboundRowListenAddr
	inboundRowPublicURL
	inboundRowRateLimit
	inboundRowGitHost          // v8.x — picker (github / github-enterprise / gitlab / bitbucket)
	inboundRowGitHostBaseURL   // v8.x — non-cloud installations
	inboundRowGitHubSecret
	inboundRowGitLabSecret
	inboundRowBitbucketSecret
	inboundRowSlackSecret
	inboundRowDiscordPubKey
	inboundRowDiscordAppID
	inboundRowDiscordBotToken
	inboundRowCount
)

// inboundGitHostCycle is the round-robin order of `GitHost` values
// pressed-Enter cycles through. Empty string maps to `github` for
// display; that's why "github" appears first.
var inboundGitHostCycle = []string{"github", "github-enterprise", "gitlab", "bitbucket"}

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
	inboundTI := textinput.New()
	inboundTI.CharLimit = 1024
	return &IntegrationsForm{
		cfg:          &pb.IntegrationsConfig{Github: &pb.GitHubIntegration{}},
		input:        ti,
		mode:         integrationsModeList,
		tab:          integrationsTabOutbound,
		inboundInput: inboundTI,
	}
}

// LoadInbound populates the Inbound tab from a freshly-fetched
// InboundStatus. Idempotent; called both on initial overlay open and
// after each saveInboundConfigCmd.
func (f *IntegrationsForm) LoadInbound(st *pb.InboundStatus) {
	if st == nil {
		return
	}
	f.inboundStatus = st
	if cfg := st.GetConfig(); cfg != nil {
		f.inboundDraft = pb.InboundConfig{
			ListenAddr:           cfg.GetListenAddr(),
			PublicUrl:            cfg.GetPublicUrl(),
			DiscordAppId:         cfg.GetDiscordAppId(),
			Disabled:             cfg.GetDisabled(),
			GithubSecretSet:      cfg.GetGithubSecretSet(),
			SlackSecretSet:       cfg.GetSlackSecretSet(),
			DiscordPublicKeySet:  cfg.GetDiscordPublicKeySet(),
			DiscordBotTokenSet:   cfg.GetDiscordBotTokenSet(),
			RateLimitPerMin:      cfg.GetRateLimitPerMin(),
			GitHost:              cfg.GetGitHost(),
			GitHostBaseUrl:       cfg.GetGitHostBaseUrl(),
			GitlabSecretSet:      cfg.GetGitlabSecretSet(),
			BitbucketSecretSet:   cfg.GetBitbucketSecretSet(),
		}
	}
}

// Tab returns the active overlay tab (outbound vs inbound).
func (f *IntegrationsForm) Tab() integrationsTab { return f.tab }

// SwitchTab toggles between the Outbound and Inbound tabs. Resets local
// edit state (inboundEditing) when leaving the Inbound tab so the next
// re-entry starts in selection mode.
func (f *IntegrationsForm) SwitchTab() {
	if f.tab == integrationsTabOutbound {
		f.tab = integrationsTabInbound
	} else {
		f.tab = integrationsTabOutbound
	}
	f.inboundEditing = false
	f.inboundInput.Blur()
}

// MoveInboundCursor advances the cursor within the Inbound tab.
func (f *IntegrationsForm) MoveInboundCursor(delta int) {
	if f.inboundEditing {
		return
	}
	next := f.inboundCursor + delta
	if next < 0 {
		next = 0
	}
	if next >= int(inboundRowCount) {
		next = int(inboundRowCount) - 1
	}
	f.inboundCursor = next
}

// IsInboundEditing reports whether the textinput is consuming keystrokes
// in the Inbound tab. Used by the key handler to decide whether to
// forward keys to the inboundInput vs. to the cursor / toggle shortcuts.
func (f *IntegrationsForm) IsInboundEditing() bool { return f.inboundEditing }

// InboundInputModel exposes the inbound textinput so the model's update
// dispatch can forward key events while editing one of the rows.
func (f *IntegrationsForm) InboundInputModel() *textinput.Model { return &f.inboundInput }

// StartInboundEdit enters edit mode for the row under the cursor.
// Toggle rows (enabled / disabled) flip immediately and stay in
// selection mode; text rows focus the textinput.
func (f *IntegrationsForm) StartInboundEdit() {
	row := inboundRow(f.inboundCursor)
	if row == inboundRowEnabled {
		f.inboundDraft.Disabled = !f.inboundDraft.Disabled
		return
	}
	if row == inboundRowGitHost {
		// Cycle through the supported Git host values rather than open a
		// textinput. Keeps the user's only valid options to the four the
		// daemon actually wires up handlers for.
		f.inboundDraft.GitHost = nextGitHost(f.inboundDraft.GetGitHost())
		return
	}
	f.inboundEditing = true
	f.inboundInput.Reset()
	switch row {
	case inboundRowListenAddr:
		f.inboundInput.SetValue(f.inboundDraft.GetListenAddr())
		f.inboundInput.Placeholder = "127.0.0.1:8765"
	case inboundRowPublicURL:
		f.inboundInput.SetValue(f.inboundDraft.GetPublicUrl())
		f.inboundInput.Placeholder = "https://your-tunnel.ngrok.app"
	case inboundRowRateLimit:
		f.inboundInput.SetValue(strconv.Itoa(int(f.inboundDraft.GetRateLimitPerMin())))
		f.inboundInput.Placeholder = "30 (req/min/IP, 0=default, -1=disable)"
	case inboundRowGitHostBaseURL:
		f.inboundInput.SetValue(f.inboundDraft.GetGitHostBaseUrl())
		f.inboundInput.Placeholder = "https://github.example.com (Enterprise / self-hosted)"
	case inboundRowDiscordAppID:
		f.inboundInput.SetValue(f.inboundDraft.GetDiscordAppId())
		f.inboundInput.Placeholder = "Discord application ID"
	default:
		f.inboundInput.SetValue("")
		f.inboundInput.Placeholder = "Paste secret here (Enter to save)"
	}
	f.inboundInput.Focus()
}

// nextGitHost rotates through the supported Git host values for the
// inboundRowGitHost picker. Empty input cycles to the first explicit
// value so an unset field becomes "github" → "github-enterprise" →
// "gitlab" → "bitbucket" → "github" on successive Enter presses.
func nextGitHost(current string) string {
	if current == "" {
		current = inboundGitHostCycle[0]
	}
	for i, v := range inboundGitHostCycle {
		if v == current {
			return inboundGitHostCycle[(i+1)%len(inboundGitHostCycle)]
		}
	}
	return inboundGitHostCycle[0]
}

// CancelInboundEdit aborts the in-progress edit and returns to selection
// mode without persisting anything.
func (f *IntegrationsForm) CancelInboundEdit() {
	f.inboundEditing = false
	f.inboundInput.Blur()
	f.inboundInput.SetValue("")
}

// CommitInboundEdit folds the textinput value into the local draft for
// the row under the cursor and returns the snapshot so the caller can
// dispatch saveInboundConfigCmd. Returns nil if no actual change was
// made (empty input on a non-secret row).
func (f *IntegrationsForm) CommitInboundEdit() *pb.InboundConfig {
	value := strings.TrimSpace(f.inboundInput.Value())
	row := inboundRow(f.inboundCursor)
	out := f.snapshotInboundDraft()
	switch row {
	case inboundRowListenAddr:
		out.ListenAddr = value
		f.inboundDraft.ListenAddr = value
	case inboundRowPublicURL:
		out.PublicUrl = value
		f.inboundDraft.PublicUrl = value
	case inboundRowRateLimit:
		// Empty / unparsable input falls back to 0 = "use the daemon
		// default" (30 req/min/IP). Negative values opt out of the
		// limiter; the daemon-side `NewRateLimiter` treats them as nil.
		n, err := strconv.Atoi(value)
		if err != nil {
			n = 0
		}
		out.RateLimitPerMin = int32(n)
		f.inboundDraft.RateLimitPerMin = int32(n)
	case inboundRowGitHostBaseURL:
		out.GitHostBaseUrl = value
		f.inboundDraft.GitHostBaseUrl = value
	case inboundRowDiscordAppID:
		out.DiscordAppId = value
		f.inboundDraft.DiscordAppId = value
	case inboundRowGitHubSecret:
		out.GithubSecret = value
	case inboundRowGitLabSecret:
		out.GitlabSecret = value
	case inboundRowBitbucketSecret:
		out.BitbucketSecret = value
	case inboundRowSlackSecret:
		out.SlackSecret = value
	case inboundRowDiscordPubKey:
		out.DiscordPublicKey = value
	case inboundRowDiscordBotToken:
		out.DiscordBotToken = value
	default:
		return nil
	}
	f.inboundEditing = false
	f.inboundInput.Blur()
	f.inboundInput.SetValue("")
	return out
}

// snapshotInboundDraft returns a copy of the inbound draft suitable for
// SaveInboundConfig — preserves every non-secret field so a save targeted
// at one row does not blank the others.
func (f *IntegrationsForm) snapshotInboundDraft() *pb.InboundConfig {
	return &pb.InboundConfig{
		ListenAddr:      f.inboundDraft.GetListenAddr(),
		PublicUrl:       f.inboundDraft.GetPublicUrl(),
		DiscordAppId:    f.inboundDraft.GetDiscordAppId(),
		Disabled:        f.inboundDraft.GetDisabled(),
		RateLimitPerMin: f.inboundDraft.GetRateLimitPerMin(),
		GitHost:         f.inboundDraft.GetGitHost(),
		GitHostBaseUrl:  f.inboundDraft.GetGitHostBaseUrl(),
	}
}

// FlushInboundDraft returns a snapshot of the local draft for cases
// where the user toggled the Enabled switch and wants to push the
// change immediately (Tab on a toggle row triggers a save).
func (f *IntegrationsForm) FlushInboundDraft() *pb.InboundConfig {
	return f.snapshotInboundDraft()
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
		if f.tab == integrationsTabInbound {
			return f.renderInbound()
		}
		return f.renderList()
	}
}

// renderInbound draws the v8.0 Echo Inbound tab: status pill + listen
// address + public URL + per-provider secrets. Mirrors the GUI's
// InboundSection layout but in plain-text form.
func (f *IntegrationsForm) renderInbound() string {
	var b strings.Builder
	b.WriteString(overlayTitleStyle.Render("Integrations · Inbound (Echo)"))
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Foreground(colorDim).Render("Receive signed deliveries from GitHub / Slack / Discord."))
	b.WriteString("\n\n")

	// Status pill — green when listening, red when bind failed,
	// dim when disabled by user toggle.
	listening := false
	bindErr := ""
	addrDisplay := f.inboundDraft.GetListenAddr()
	if addrDisplay == "" {
		addrDisplay = "127.0.0.1:8765"
	}
	if f.inboundStatus != nil {
		listening = f.inboundStatus.GetListening()
		bindErr = f.inboundStatus.GetBindError()
		if a := f.inboundStatus.GetListenAddr(); a != "" {
			addrDisplay = a
		}
	}
	pill := ""
	switch {
	case f.inboundDraft.GetDisabled():
		pill = lipgloss.NewStyle().Foreground(colorDim).Render("● Disabled")
	case listening:
		pill = lipgloss.NewStyle().Foreground(colorCyan).Render("● Listening")
	case bindErr != "":
		pill = lipgloss.NewStyle().Foreground(colorRed).Render("● Bind error")
	default:
		pill = lipgloss.NewStyle().Foreground(colorRed).Render("● Not listening")
	}
	b.WriteString(fmt.Sprintf("%s  %s\n", pill, lipgloss.NewStyle().Foreground(colorDim).Render(addrDisplay)))
	if bindErr != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(colorRed).Render(bindErr))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	rows := f.inboundRowDescriptors()
	for i, r := range rows {
		marker := "  "
		if i == f.inboundCursor && !f.inboundEditing {
			marker = settingsCursorStyle.Render("▸ ")
		}
		line := fmt.Sprintf("%s%-26s %s", marker, r.label, r.value)
		b.WriteString(line)
		b.WriteString("\n")
	}

	// v8.x Echo — per-guild Discord auto-registration status. Read-only
	// list mirroring the GUI's DiscordGuildList. Empty when no guilds
	// have been registered yet (bot token not configured, or bot just
	// added and gateway hasn't received GUILD_CREATE yet).
	if f.inboundStatus != nil {
		guilds := f.inboundStatus.GetDiscordGuilds()
		if len(guilds) > 0 {
			b.WriteString("\n")
			b.WriteString(lipgloss.NewStyle().Foreground(colorDim).Render("Registered Discord guilds"))
			b.WriteString("\n")
			for _, g := range guilds {
				glyph := lipgloss.NewStyle().Foreground(colorRed).Render("✗")
				if g.GetRegistered() {
					glyph = lipgloss.NewStyle().Foreground(colorCyan).Render("✓")
				}
				name := g.GetGuildName()
				if name == "" {
					name = "(unknown)"
				}
				b.WriteString(fmt.Sprintf("  %s %-30s %s\n", glyph, name, lipgloss.NewStyle().Foreground(colorDim).Render(g.GetGuildId())))
				if g.GetError() != "" && !g.GetRegistered() {
					b.WriteString(fmt.Sprintf("      %s\n", lipgloss.NewStyle().Foreground(colorRed).Render(g.GetError())))
				}
			}
		}
	}

	if f.inboundEditing {
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().Foreground(colorCyan).Render("Editing: "+rows[f.inboundCursor].label) + "\n")
		b.WriteString(f.inboundInput.View())
		b.WriteString("\n\n")
		b.WriteString(lipgloss.NewStyle().Foreground(colorDim).Render("Enter saves · Esc cancels"))
	} else {
		b.WriteString("\n")
		if f.statusLine != "" {
			b.WriteString(lipgloss.NewStyle().Foreground(colorCyan).Render(f.statusLine))
			b.WriteString("\n")
		}
		b.WriteString(lipgloss.NewStyle().Foreground(colorDim).Render("Tab outbound/inbound · ↑↓ move · Enter edit/toggle · Esc close"))
	}
	return b.String()
}

type inboundRowDescriptor struct {
	label string
	value string
}

func (f *IntegrationsForm) inboundRowDescriptors() []inboundRowDescriptor {
	rows := make([]inboundRowDescriptor, inboundRowCount)
	rows[inboundRowEnabled] = inboundRowDescriptor{
		label: "Enabled",
		value: boolLabel(!f.inboundDraft.GetDisabled()),
	}
	rows[inboundRowListenAddr] = inboundRowDescriptor{
		label: "Listen address",
		value: orPlaceholder(f.inboundDraft.GetListenAddr(), "127.0.0.1:8765"),
	}
	rows[inboundRowPublicURL] = inboundRowDescriptor{
		label: "Public URL",
		value: orPlaceholder(f.inboundDraft.GetPublicUrl(), "(unset)"),
	}
	rows[inboundRowRateLimit] = inboundRowDescriptor{
		label: "Rate limit (per IP per min)",
		value: rateLimitLabel(f.inboundDraft.GetRateLimitPerMin()),
	}
	rows[inboundRowGitHost] = inboundRowDescriptor{
		label: "Git host",
		value: gitHostLabel(f.inboundDraft.GetGitHost()),
	}
	rows[inboundRowGitHostBaseURL] = inboundRowDescriptor{
		label: "Git host base URL",
		value: orPlaceholder(f.inboundDraft.GetGitHostBaseUrl(), "(cloud default)"),
	}
	rows[inboundRowGitHubSecret] = inboundRowDescriptor{
		label: "GitHub secret",
		value: secretLabel(f.inboundDraft.GetGithubSecretSet(), f.lastDeliveryDisplay("github")),
	}
	rows[inboundRowGitLabSecret] = inboundRowDescriptor{
		label: "GitLab token",
		value: secretLabel(f.inboundDraft.GetGitlabSecretSet(), f.lastDeliveryDisplay("gitlab")),
	}
	rows[inboundRowBitbucketSecret] = inboundRowDescriptor{
		label: "Bitbucket secret",
		value: secretLabel(f.inboundDraft.GetBitbucketSecretSet(), f.lastDeliveryDisplay("bitbucket")),
	}
	rows[inboundRowSlackSecret] = inboundRowDescriptor{
		label: "Slack signing secret",
		value: secretLabel(f.inboundDraft.GetSlackSecretSet(), f.lastDeliveryDisplay("slack")),
	}
	rows[inboundRowDiscordPubKey] = inboundRowDescriptor{
		label: "Discord public key",
		value: secretLabel(f.inboundDraft.GetDiscordPublicKeySet(), f.lastDeliveryDisplay("discord")),
	}
	rows[inboundRowDiscordAppID] = inboundRowDescriptor{
		label: "Discord app ID",
		value: orPlaceholder(f.inboundDraft.GetDiscordAppId(), "(unset)"),
	}
	rows[inboundRowDiscordBotToken] = inboundRowDescriptor{
		label: "Discord bot token",
		value: secretLabel(f.inboundDraft.GetDiscordBotTokenSet(), ""),
	}
	return rows
}

// gitHostLabel maps a `GitHost` enum value to a friendly display string.
// Empty input renders as "github (default)" so the user knows the unset
// state still routes to github.com.
func gitHostLabel(v string) string {
	switch v {
	case "":
		return "github (default)"
	case "github":
		return "github"
	case "github-enterprise":
		return "github-enterprise"
	case "gitlab":
		return "gitlab"
	case "bitbucket":
		return "bitbucket"
	default:
		return v
	}
}

func (f *IntegrationsForm) lastDeliveryDisplay(provider string) string {
	if f.inboundStatus == nil {
		return ""
	}
	var unix int64
	switch provider {
	case "github":
		unix = f.inboundStatus.GetLastGithubDeliveryUnix()
	case "slack":
		unix = f.inboundStatus.GetLastSlackDeliveryUnix()
	case "discord":
		unix = f.inboundStatus.GetLastDiscordDeliveryUnix()
	case "gitlab":
		unix = f.inboundStatus.GetLastGitlabDeliveryUnix()
	case "bitbucket":
		unix = f.inboundStatus.GetLastBitbucketDeliveryUnix()
	}
	if unix == 0 {
		return ""
	}
	return fmt.Sprintf("· last %s ago", humanDuration(time.Since(time.Unix(unix, 0))))
}

func boolLabel(on bool) string {
	if on {
		return settingsToggleOn.Render("[x] on")
	}
	return settingsToggleOff.Render("[ ] off")
}

func secretLabel(isSet bool, lastDelivery string) string {
	base := lipgloss.NewStyle().Foreground(colorDim).Render("not set")
	if isSet {
		base = lipgloss.NewStyle().Foreground(colorCyan).Render("set")
	}
	if lastDelivery != "" {
		base += "  " + lipgloss.NewStyle().Foreground(colorDim).Render(lastDelivery)
	}
	return base
}

func orPlaceholder(s, placeholder string) string {
	if s == "" {
		return lipgloss.NewStyle().Foreground(colorDim).Render(placeholder)
	}
	return s
}

// rateLimitLabel renders the per-IP rate-limit row. 0 = "default (30 / min)";
// negative = "disabled"; positive = the literal "<N> / min".
func rateLimitLabel(perMin int32) string {
	switch {
	case perMin < 0:
		return lipgloss.NewStyle().Foreground(colorDim).Render("disabled")
	case perMin == 0:
		return lipgloss.NewStyle().Foreground(colorDim).Render("default (30 / min)")
	default:
		return strconv.Itoa(int(perMin)) + " / min"
	}
}

func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
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
