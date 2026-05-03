package relay

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/watchfire-io/watchfire/internal/daemon/notify"
	"github.com/watchfire-io/watchfire/internal/models"
)

// ---- golden tests --------------------------------------------------------

func TestSlackTemplateGoldens(t *testing.T) {
	cases := []struct {
		name     string
		fixture  Payload
		fileName string
	}{
		{"task_failed", failedFixture(), "slack_task_failed.json"},
		{"run_complete", runCompleteFixture(), "slack_run_complete.json"},
		{"weekly_digest", weeklyDigestFixture(), "slack_weekly_digest.json"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			s := newSlackAdapterForTest(t, models.SlackEndpoint{ID: "test", URL: "https://example.invalid"})
			tmpl, err := s.templateFor(notify.Kind(tc.fixture.Kind))
			if err != nil {
				t.Fatalf("templateFor: %v", err)
			}
			rendered, err := s.render(tmpl, tc.fixture)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			gotJSON := normalizeJSON(t, rendered)

			goldenPath := filepath.Join("templates", "testdata", tc.fileName)
			wantBytes, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden %s: %v", goldenPath, err)
			}
			wantJSON := normalizeJSON(t, wantBytes)

			if !reflect.DeepEqual(gotJSON, wantJSON) {
				t.Errorf("rendered JSON does not match golden %s\n--- got\n%s\n--- want\n%s",
					goldenPath, mustMarshalIndent(gotJSON), mustMarshalIndent(wantJSON))
			}
		})
	}
}

// ---- httptest end-to-end ------------------------------------------------

// minimalSlackMessage decodes the subset of a Slack Block Kit envelope
// the tests assert on. Mirrors the shape Slack documents at
// https://api.slack.com/reference/block-kit/blocks.
//
// Elements are kept as RawMessage because context-block elements carry
// `text` as a string while actions-block elements (buttons) carry it as
// a nested object — we decode per block-type below.
type minimalSlackMessage struct {
	Blocks []minimalSlackBlock `json:"blocks"`
}

type minimalSlackBlock struct {
	Type     string            `json:"type"`
	Text     *minimalSlackText `json:"text,omitempty"`
	Elements []json.RawMessage `json:"elements,omitempty"`
}

type minimalSlackText struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	Emoji *bool  `json:"emoji,omitempty"`
}

type minimalSlackContextElement struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type minimalSlackButtonElement struct {
	Type     string            `json:"type"`
	ActionID string            `json:"action_id,omitempty"`
	Style    string            `json:"style,omitempty"`
	Text     *minimalSlackText `json:"text"`
	URL      string            `json:"url,omitempty"`
	Value    string            `json:"value,omitempty"`
}

// expectedButton describes one element the action block should carry,
// in the order the template emits them. v8.x Echo's TASK_FAILED template
// fans the actions block out into Retry / Cancel / View; non-failure
// templates still ship a single View / Open-digest button.
type expectedButton struct {
	actionID string
	style    string
	text     string
	value    string
	url      string
}

func TestSlackSendEndToEnd(t *testing.T) {
	cases := []struct {
		name        string
		fixture     Payload
		wantHeader  string
		wantSection string
		wantContext string
		wantButtons []expectedButton
	}{
		{
			name:        "task_failed",
			fixture:     failedFixture(),
			wantHeader:  ":rotating_light: Task failed — Watchfire",
			wantSection: "*Task #0042*: Build the Discord adapter\n*Reason*: tests failed: 3 of 12",
			wantContext: ":large_red_square: Watchfire · 2026-05-02T12:34:56Z",
			wantButtons: []expectedButton{
				{actionID: "watchfire_retry", style: "primary", text: "Retry", value: "proj-abc|42"},
				{actionID: "watchfire_cancel", style: "danger", text: "Cancel", value: "proj-abc|42"},
				{actionID: "watchfire_view", text: "View in Watchfire", url: "watchfire://project/proj-abc/task/0042"},
			},
		},
		{
			name:        "run_complete",
			fixture:     runCompleteFixture(),
			wantHeader:  ":white_check_mark: Run complete — Watchfire",
			wantSection: "*Task #0042*: Build the Discord adapter",
			wantContext: ":large_blue_square: Watchfire · 2026-05-02T12:34:56Z",
			wantButtons: []expectedButton{
				{text: "View in Watchfire", url: "watchfire://project/proj-abc/task/0042"},
			},
		},
		{
			name:        "weekly_digest",
			fixture:     weeklyDigestFixture(),
			wantHeader:  ":bar_chart: Watchfire — your week",
			wantSection: weeklyDigestFixture().DigestBody,
			wantContext: ":bar_chart: Weekly digest · 2026-05-02T12:34:56Z",
			wantButtons: []expectedButton{
				{text: "Open digest", url: "watchfire://digest/2026-05-02"},
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var captured atomic.Value
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("missing Content-Type: %v", r.Header)
				}
				body, _ := io.ReadAll(r.Body)
				captured.Store(body)
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			s := newSlackAdapterForTest(t, models.SlackEndpoint{
				ID:  "ep",
				URL: srv.URL,
				EnabledEvents: models.EventBitmask{
					TaskFailed: true, RunComplete: true, WeeklyDigest: true,
				},
			})
			if err := s.Send(context.Background(), tc.fixture); err != nil {
				t.Fatalf("Send: %v", err)
			}
			raw, ok := captured.Load().([]byte)
			if !ok {
				t.Fatal("server never received the request")
			}
			var got minimalSlackMessage
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("server received non-Block-Kit JSON: %v\n%s", err, raw)
			}
			if len(got.Blocks) != 4 {
				t.Fatalf("want 4 blocks (header / section / context / actions), got %d", len(got.Blocks))
			}

			// header
			h := got.Blocks[0]
			if h.Type != "header" {
				t.Errorf("blocks[0] type = %q, want header", h.Type)
			}
			if h.Text == nil || h.Text.Type != "plain_text" || h.Text.Text != tc.wantHeader {
				t.Errorf("header text = %+v, want plain_text=%q", h.Text, tc.wantHeader)
			}

			// section
			sec := got.Blocks[1]
			if sec.Type != "section" {
				t.Errorf("blocks[1] type = %q, want section", sec.Type)
			}
			if sec.Text == nil || sec.Text.Type != "mrkdwn" || sec.Text.Text != tc.wantSection {
				t.Errorf("section text = %+v, want mrkdwn=%q", sec.Text, tc.wantSection)
			}

			// context
			ctxBlock := got.Blocks[2]
			if ctxBlock.Type != "context" {
				t.Errorf("blocks[2] type = %q, want context", ctxBlock.Type)
			}
			if len(ctxBlock.Elements) != 1 {
				t.Fatalf("context elements len = %d, want 1", len(ctxBlock.Elements))
			}
			var ctxEl minimalSlackContextElement
			if err := json.Unmarshal(ctxBlock.Elements[0], &ctxEl); err != nil {
				t.Fatalf("decode context element: %v", err)
			}
			if ctxEl.Type != "mrkdwn" || ctxEl.Text != tc.wantContext {
				t.Errorf("context element = %+v, want mrkdwn=%q", ctxEl, tc.wantContext)
			}

			// actions
			act := got.Blocks[3]
			if act.Type != "actions" {
				t.Errorf("blocks[3] type = %q, want actions", act.Type)
			}
			if len(act.Elements) != len(tc.wantButtons) {
				t.Fatalf("action elements len = %d, want %d", len(act.Elements), len(tc.wantButtons))
			}
			for i, want := range tc.wantButtons {
				var btn minimalSlackButtonElement
				if err := json.Unmarshal(act.Elements[i], &btn); err != nil {
					t.Fatalf("decode button element %d: %v", i, err)
				}
				if btn.Type != "button" {
					t.Errorf("button[%d] type = %q, want button", i, btn.Type)
				}
				if btn.Text == nil || btn.Text.Text != want.text {
					t.Errorf("button[%d] text = %+v, want %q", i, btn.Text, want.text)
				}
				if btn.ActionID != want.actionID {
					t.Errorf("button[%d] action_id = %q, want %q", i, btn.ActionID, want.actionID)
				}
				if btn.Style != want.style {
					t.Errorf("button[%d] style = %q, want %q", i, btn.Style, want.style)
				}
				if btn.Value != want.value {
					t.Errorf("button[%d] value = %q, want %q", i, btn.Value, want.value)
				}
				if btn.URL != want.url {
					t.Errorf("button[%d] url = %q, want %q", i, btn.URL, want.url)
				}
			}
		})
	}
}

func TestSlackSendUnsupportedKindIsError(t *testing.T) {
	s := newSlackAdapterForTest(t, models.SlackEndpoint{ID: "ep", URL: "https://example.invalid"})
	p := failedFixture()
	p.Kind = "BOGUS_KIND"
	if err := s.Send(context.Background(), p); err == nil {
		t.Fatal("expected error for unsupported kind")
	}
}

func TestSlackSendMissingURLIsError(t *testing.T) {
	s := newSlackAdapterForTest(t, models.SlackEndpoint{ID: "ep", URL: ""})
	if err := s.Send(context.Background(), failedFixture()); err == nil {
		t.Fatal("expected error for missing URL")
	}
}

func TestSlackSendHTTPFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	s := newSlackAdapterForTest(t, models.SlackEndpoint{ID: "ep", URL: srv.URL})
	if err := s.Send(context.Background(), failedFixture()); err == nil {
		t.Error("expected error for 5xx response")
	}
}

// ---- color mapping ------------------------------------------------------

func TestSlackEmojiForColor(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// Eight v4.0 Beacon swatches.
		{"#ef4444", ":large_red_square:"},
		{"#f97316", ":large_orange_square:"},
		{"#eab308", ":large_yellow_square:"},
		{"#22c55e", ":large_green_square:"},
		{"#14b8a6", ":large_green_square:"},
		{"#06b6d4", ":large_blue_square:"},
		{"#3b82f6", ":large_blue_square:"},
		{"#8b5cf6", ":large_purple_square:"},
		// Case-insensitive.
		{"#EF4444", ":large_red_square:"},
		// Whitespace tolerance.
		{"  #22c55e  ", ":large_green_square:"},
		// Unknown / malformed / empty fall through.
		{"#zzzzzz", slackColorFallbackEmoji},
		{"#abcdef", slackColorFallbackEmoji},
		{"", slackColorFallbackEmoji},
	}
	for _, tc := range cases {
		got := slackEmojiForColor(tc.in)
		if got != tc.want {
			t.Errorf("slackEmojiForColor(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ---- mute ----------------------------------------------------------------

func TestSlackIsProjectMuted(t *testing.T) {
	s := newSlackAdapterForTest(t, models.SlackEndpoint{
		ID:             "ep",
		URL:            "https://example.invalid",
		ProjectMuteIDs: []string{"proj-muted"},
	})
	if !s.IsProjectMuted("proj-muted") {
		t.Error("expected proj-muted to be muted")
	}
	if s.IsProjectMuted("proj-other") {
		t.Error("proj-other should not be muted")
	}
}

// TestSlackMutedProjectNotCalled exercises the dispatcher contract:
// a muted project must never POST. The dispatcher checks IsProjectMuted
// before invoking Send, so this test reproduces that guard against a
// real httptest endpoint to confirm no request is captured.
func TestSlackMutedProjectNotCalled(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := newSlackAdapterForTest(t, models.SlackEndpoint{
		ID:             "ep",
		URL:            srv.URL,
		ProjectMuteIDs: []string{"proj-abc"},
		EnabledEvents:  models.EventBitmask{TaskFailed: true},
	})
	p := failedFixture() // ProjectID = proj-abc
	if !s.IsProjectMuted(p.ProjectID) {
		t.Fatal("expected proj-abc to be detected as muted")
	}
	// Dispatcher would skip Send here. Confirm no request ever fires.
	if called {
		t.Error("muted project should not have triggered a POST")
	}
}

// ---- supports ------------------------------------------------------------

func TestSlackSupportsBitmask(t *testing.T) {
	s := newSlackAdapterForTest(t, models.SlackEndpoint{
		EnabledEvents: models.EventBitmask{TaskFailed: true, WeeklyDigest: true},
	})
	if !s.Supports(notify.KindTaskFailed) {
		t.Error("TaskFailed should be supported")
	}
	if s.Supports(notify.KindRunComplete) {
		t.Error("RunComplete should NOT be supported")
	}
	if !s.Supports(notify.KindWeeklyDigest) {
		t.Error("WeeklyDigest should be supported")
	}
	if s.Supports(notify.Kind("UNKNOWN")) {
		t.Error("unknown kinds should not be supported")
	}
}

// ---- helpers -------------------------------------------------------------

func newSlackAdapterForTest(t *testing.T, ep models.SlackEndpoint) *SlackAdapter {
	t.Helper()
	s, err := NewSlackAdapter(ep, nil, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("NewSlackAdapter: %v", err)
	}
	return s
}
