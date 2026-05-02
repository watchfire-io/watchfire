package tui

import (
	"strings"
	"testing"

	pb "github.com/watchfire-io/watchfire/proto"
)

// renderListSnapshot builds an IntegrationsForm at a fixed width, loads
// the supplied config, and returns its list-mode View() output. The
// `golden` table below pins the structural shape of each variant —
// the GitHub row is always present (single-instance config); webhook /
// slack / discord rows scale with the input.
func renderListSnapshot(cfg *pb.IntegrationsConfig) string {
	f := NewIntegrationsForm()
	f.SetWidth(80)
	f.Load(cfg)
	return f.View()
}

// TestIntegrationsListEmpty verifies the 0-integrations golden: only
// the always-present GitHub row + the "(none configured)"-equivalent
// state shows up.
func TestIntegrationsListEmpty(t *testing.T) {
	out := renderListSnapshot(&pb.IntegrationsConfig{Github: &pb.GitHubIntegration{}})
	mustContain(t, out, "Integrations")
	mustContain(t, out, "GitHub")
	mustContain(t, out, "GitHub Auto-PR (disabled)")
	// Footer key hints stay constant across variants.
	mustContain(t, out, "a add")
	mustContain(t, out, "Esc close")
}

// TestIntegrationsListOne verifies a single Slack integration renders.
func TestIntegrationsListOne(t *testing.T) {
	cfg := &pb.IntegrationsConfig{
		Slack: []*pb.SlackIntegration{
			{
				Id:       "slack-1",
				Label:    "alerts",
				UrlLabel: "***hooks.slack.com/services/T0/...",
				EnabledEvents: &pb.IntegrationEvents{
					TaskFailed: true, RunComplete: true,
				},
			},
		},
		Github: &pb.GitHubIntegration{},
	}
	out := renderListSnapshot(cfg)
	mustContain(t, out, "Slack")
	mustContain(t, out, "alerts")
	mustContain(t, out, "***hooks.slack.com")
	// Two-event chip — the renderer styles them but the text labels
	// must show through for the snapshot to remain readable.
	mustContain(t, out, "FAIL")
	mustContain(t, out, "DONE")
}

// TestIntegrationsListThree exercises the multi-row + per-project mute
// chip path. One webhook + one Slack + one Discord, each with a
// different mute count, plus an enabled GitHub config.
func TestIntegrationsListThree(t *testing.T) {
	cfg := &pb.IntegrationsConfig{
		Webhooks: []*pb.WebhookIntegration{{
			Id: "w-1", Label: "ops", UrlLabel: "***ops.example.com/incoming",
			EnabledEvents: &pb.IntegrationEvents{TaskFailed: true},
			ProjectMuteIds: []string{"p-1"},
		}},
		Slack: []*pb.SlackIntegration{{
			Id: "s-1", Label: "channel", UrlLabel: "***hooks.slack.com/services/T0/...",
			EnabledEvents: &pb.IntegrationEvents{TaskFailed: true, RunComplete: true},
			ProjectMuteIds: []string{"p-1", "p-2"},
		}},
		Discord: []*pb.DiscordIntegration{{
			Id: "d-1", Label: "general", UrlLabel: "***discord.com/api/webhooks/...",
			EnabledEvents: &pb.IntegrationEvents{WeeklyDigest: true},
		}},
		Github: &pb.GitHubIntegration{
			Enabled:       true,
			DraftDefault:  true,
			ProjectScopes: []string{"p-1"},
		},
	}
	out := renderListSnapshot(cfg)

	mustContain(t, out, "Webhook")
	mustContain(t, out, "ops")
	mustContain(t, out, "Slack")
	mustContain(t, out, "channel")
	mustContain(t, out, "Discord")
	mustContain(t, out, "general")
	mustContain(t, out, "GitHub Auto-PR — draft")
	// 1-mute and 2-mute chips both show through.
	mustContain(t, out, "1 muted")
	mustContain(t, out, "2 muted")
	// WEEK chip from the Discord row.
	mustContain(t, out, "WEEK")
}

// TestIntegrationsAddFormFlow exercises the stacked form: kind cycle →
// URL → label → events → mutes → confirm. Each AdvanceAdd step should
// either move forward or, on the final step, return true so the model
// dispatches Save.
func TestIntegrationsAddFormFlow(t *testing.T) {
	f := NewIntegrationsForm()
	f.Load(&pb.IntegrationsConfig{Github: &pb.GitHubIntegration{}})
	f.StartAdd()

	if f.Mode() != integrationsModeAdd {
		t.Fatalf("StartAdd should switch to add mode")
	}
	if got := f.addStep; got != addFieldKind {
		t.Fatalf("first step should be kind, got %v", got)
	}

	f.CycleAddKind(+1) // move from Webhook → Slack
	if f.addKind != integrationsRowSlack {
		t.Fatalf("cycle should advance to Slack, got %v", f.addKind)
	}

	if done := f.AdvanceAdd(); done {
		t.Fatalf("kind step should not be terminal")
	}
	if f.addStep != addFieldURL {
		t.Fatalf("step after kind should be URL, got %v", f.addStep)
	}

	f.input.SetValue("https://hooks.slack.com/services/T/B/x")
	if done := f.AdvanceAdd(); done {
		t.Fatalf("url step should not be terminal")
	}
	if f.addURL != "https://hooks.slack.com/services/T/B/x" {
		t.Fatalf("url not captured: %q", f.addURL)
	}

	f.input.SetValue("alerts")
	if done := f.AdvanceAdd(); done {
		t.Fatalf("label step should not be terminal")
	}
	if f.addLabel != "alerts" {
		t.Fatalf("label not captured: %q", f.addLabel)
	}

	f.AdvanceAdd() // events → mutes
	if f.addStep != addFieldMutes {
		t.Fatalf("step after events should be mutes, got %v", f.addStep)
	}

	if done := f.AdvanceAdd(); !done {
		t.Fatalf("mutes step should be terminal")
	}

	kind, url, label, events, mutes := f.AddSnapshot()
	if kind != integrationsRowSlack {
		t.Errorf("snapshot kind, want Slack got %v", kind)
	}
	if url != "https://hooks.slack.com/services/T/B/x" {
		t.Errorf("snapshot url: %q", url)
	}
	if label != "alerts" {
		t.Errorf("snapshot label: %q", label)
	}
	if events == nil || !events.GetTaskFailed() {
		t.Errorf("snapshot events should default to TASK_FAILED on")
	}
	if len(mutes) != 0 {
		t.Errorf("snapshot mutes should default empty, got %v", mutes)
	}
}

// TestIntegrationsConfirmDeleteRoundTrip exercises the y/n delete prompt.
func TestIntegrationsConfirmDeleteRoundTrip(t *testing.T) {
	f := NewIntegrationsForm()
	f.Load(&pb.IntegrationsConfig{
		Webhooks: []*pb.WebhookIntegration{{Id: "w-1", Label: "ops"}},
		Github:   &pb.GitHubIntegration{},
	})

	if got := f.CurrentRow(); got.ID != "w-1" {
		t.Fatalf("cursor should land on webhook row, got %+v", got)
	}

	f.StartDeleteConfirm()
	if f.Mode() != integrationsModeConfirmDelete {
		t.Fatalf("expected confirm-delete mode")
	}

	row, ok := f.FinishDeleteConfirm(false)
	if ok || row.ID != "" {
		t.Errorf("cancel should return ok=false")
	}
	if f.Mode() != integrationsModeList {
		t.Errorf("cancel should return to list mode")
	}

	f.StartDeleteConfirm()
	row, ok = f.FinishDeleteConfirm(true)
	if !ok || row.ID != "w-1" {
		t.Errorf("confirm should return the row, got ok=%v id=%q", ok, row.ID)
	}
}

func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("expected output to contain %q, got:\n%s", needle, haystack)
	}
}
