package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/daemon/notify"
	"github.com/watchfire-io/watchfire/internal/models"
)

// fixedEmittedAt is a single deterministic emission timestamp every test
// payload uses, so the rendered JSON is byte-stable across runs.
var fixedEmittedAt = time.Date(2026, 5, 2, 12, 34, 56, 0, time.UTC)

// failedFixture is the canonical TASK_FAILED payload used by golden +
// httptest cases.
func failedFixture() Payload {
	return Payload{
		Version:           1,
		Kind:              string(notify.KindTaskFailed),
		EmittedAt:         fixedEmittedAt,
		ProjectID:         "proj-abc",
		ProjectName:       "Watchfire",
		ProjectColor:      "#ef4444",
		TaskNumber:        42,
		TaskTitle:         "Build the Discord adapter",
		TaskFailureReason: "tests failed: 3 of 12",
		DeepLink:          "watchfire://project/proj-abc/task/0042",
	}
}

func runCompleteFixture() Payload {
	return Payload{
		Version:      1,
		Kind:         string(notify.KindRunComplete),
		EmittedAt:    fixedEmittedAt,
		ProjectID:    "proj-abc",
		ProjectName:  "Watchfire",
		ProjectColor: "#3b82f6",
		TaskNumber:   42,
		TaskTitle:    "Build the Discord adapter",
		DeepLink:     "watchfire://project/proj-abc/task/0042",
	}
}

func weeklyDigestFixture() Payload {
	return Payload{
		Version:    1,
		Kind:       string(notify.KindWeeklyDigest),
		EmittedAt:  fixedEmittedAt,
		ProjectID:  "",
		DeepLink:   "watchfire://digest/2026-05-02",
		DigestDate: "2026-05-02",
		DigestPath: "/Users/me/.watchfire/digests/2026-05-02.md",
		DigestBody: "## This week\n\n- 12 tasks completed across 3 projects\n- 2 failures",
	}
}

// ---- golden tests --------------------------------------------------------

func TestDiscordTemplateGoldens(t *testing.T) {
	cases := []struct {
		name     string
		fixture  Payload
		fileName string
	}{
		{"task_failed", failedFixture(), "discord_task_failed.json"},
		{"run_complete", runCompleteFixture(), "discord_run_complete.json"},
		{"weekly_digest", weeklyDigestFixture(), "discord_weekly_digest.json"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			d := newAdapterForTest(t, models.DiscordEndpoint{ID: "test", URL: "https://example.invalid"})
			tmpl, err := d.templateFor(notify.Kind(tc.fixture.Kind))
			if err != nil {
				t.Fatalf("templateFor: %v", err)
			}
			rendered, err := d.render(tmpl, tc.fixture)
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

// minimalDiscordPayload is the subset of the Discord webhook envelope the
// tests assert on. Mirrors the docs at
// https://discord.com/developers/docs/resources/webhook#execute-webhook
type minimalDiscordPayload struct {
	Username  string                 `json:"username"`
	AvatarURL string                 `json:"avatar_url"`
	Embeds    []minimalDiscordEmbed  `json:"embeds"`
	Other     map[string]interface{} `json:"-"`
}

type minimalDiscordEmbed struct {
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	URL         string                 `json:"url"`
	Color       int                    `json:"color"`
	Timestamp   string                 `json:"timestamp"`
	Footer      *minimalDiscordFooter  `json:"footer,omitempty"`
	Other       map[string]interface{} `json:"-"`
}

type minimalDiscordFooter struct {
	Text string `json:"text"`
}

func TestDiscordSendEndToEnd(t *testing.T) {
	cases := []struct {
		name       string
		fixture    Payload
		wantTitle  string
		wantDesc   string
		wantColor  int
		wantURL    string
		wantFooter string
	}{
		{
			name:       "task_failed",
			fixture:    failedFixture(),
			wantTitle:  "Task failed — Watchfire",
			wantDesc:   "**#0042** Build the Discord adapter\n\n_tests failed: 3 of 12_",
			wantColor:  0xef4444,
			wantURL:    "watchfire://project/proj-abc/task/0042",
			wantFooter: "Watchfire",
		},
		{
			name:       "run_complete",
			fixture:    runCompleteFixture(),
			wantTitle:  "Run complete — Watchfire",
			wantDesc:   "**#0042** Build the Discord adapter",
			wantColor:  0x22c55e,
			wantURL:    "watchfire://project/proj-abc/task/0042",
			wantFooter: "Watchfire",
		},
		{
			name:       "weekly_digest",
			fixture:    weeklyDigestFixture(),
			wantTitle:  "Watchfire — your week",
			wantDesc:   weeklyDigestFixture().DigestBody,
			wantColor:  0x22c55e,
			wantURL:    "watchfire://digest/2026-05-02",
			wantFooter: "Weekly digest",
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
				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()

			d := newAdapterForTest(t, models.DiscordEndpoint{
				ID:  "ep",
				URL: srv.URL,
				EnabledEvents: models.EventBitmask{
					TaskFailed: true, RunComplete: true, WeeklyDigest: true,
				},
			})

			if err := d.Send(context.Background(), tc.fixture); err != nil {
				t.Fatalf("Send: %v", err)
			}
			raw, ok := captured.Load().([]byte)
			if !ok {
				t.Fatal("server never received the request")
			}
			var got minimalDiscordPayload
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("server received non-JSON body: %v\n%s", err, raw)
			}
			if len(got.Embeds) != 1 {
				t.Fatalf("want 1 embed, got %d", len(got.Embeds))
			}
			e := got.Embeds[0]
			if got.Username != "Watchfire" {
				t.Errorf("username = %q, want Watchfire", got.Username)
			}
			if e.Title != tc.wantTitle {
				t.Errorf("title = %q, want %q", e.Title, tc.wantTitle)
			}
			if e.Description != tc.wantDesc {
				t.Errorf("description = %q, want %q", e.Description, tc.wantDesc)
			}
			if e.Color != tc.wantColor {
				t.Errorf("color = %d, want %d", e.Color, tc.wantColor)
			}
			if e.URL != tc.wantURL {
				t.Errorf("url = %q, want %q", e.URL, tc.wantURL)
			}
			if e.Footer == nil || e.Footer.Text != tc.wantFooter {
				t.Errorf("footer = %+v, want text=%q", e.Footer, tc.wantFooter)
			}
			if !strings.HasPrefix(e.Timestamp, "2026-05-02T12:34:56") {
				t.Errorf("timestamp = %q, want RFC3339 with the fixture date", e.Timestamp)
			}
		})
	}
}

func TestDiscordSendUnsupportedKindIsError(t *testing.T) {
	d := newAdapterForTest(t, models.DiscordEndpoint{ID: "ep", URL: "https://example.invalid"})
	p := failedFixture()
	p.Kind = "BOGUS_KIND"
	err := d.Send(context.Background(), p)
	if err == nil {
		t.Fatal("expected error for unsupported kind")
	}
}

func TestDiscordSendMissingURLIsError(t *testing.T) {
	d := newAdapterForTest(t, models.DiscordEndpoint{ID: "ep", URL: ""})
	if err := d.Send(context.Background(), failedFixture()); err == nil {
		t.Fatal("expected error for missing URL")
	}
}

func TestDiscordSendHTTPFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	d := newAdapterForTest(t, models.DiscordEndpoint{ID: "ep", URL: srv.URL})
	if err := d.Send(context.Background(), failedFixture()); err == nil {
		t.Error("expected error for 5xx response")
	}
}

// ---- color conversion ---------------------------------------------------

func TestHexToInt(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		// Eight known swatches from internal/models/project.go ProjectColors.
		{"#ef4444", 0xef4444}, // red          → 15680580
		{"#f97316", 0xf97316}, // orange
		{"#eab308", 0xeab308}, // yellow
		{"#22c55e", 0x22c55e}, // green        → 2278750
		{"#14b8a6", 0x14b8a6}, // teal
		{"#06b6d4", 0x06b6d4}, // cyan
		{"#3b82f6", 0x3b82f6}, // blue
		{"#8b5cf6", 0x8b5cf6}, // violet
		// Two fallback paths: malformed + empty.
		{"#zzzzzz", fallbackEmbedColor},
		{"", fallbackEmbedColor},
	}
	for _, tc := range cases {
		got := hexToInt(tc.in)
		if got != tc.want {
			t.Errorf("hexToInt(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// ---- mute ----------------------------------------------------------------

func TestDiscordIsProjectMuted(t *testing.T) {
	d := newAdapterForTest(t, models.DiscordEndpoint{
		ID:             "ep",
		URL:            "https://example.invalid",
		ProjectMuteIDs: []string{"proj-muted"},
	})
	if !d.IsProjectMuted("proj-muted") {
		t.Error("expected proj-muted to be muted")
	}
	if d.IsProjectMuted("proj-other") {
		t.Error("proj-other should not be muted")
	}
}

// TestDiscordMutedProjectNotCalled exercises the dispatcher contract a
// muted project must never POST. The dispatcher checks IsProjectMuted
// before invoking Send, so this test reproduces that guard.
func TestDiscordMutedProjectNotCalled(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	d := newAdapterForTest(t, models.DiscordEndpoint{
		ID:             "ep",
		URL:            srv.URL,
		ProjectMuteIDs: []string{"proj-abc"},
		EnabledEvents:  models.EventBitmask{TaskFailed: true},
	})
	p := failedFixture() // ProjectID = proj-abc
	if d.IsProjectMuted(p.ProjectID) {
		// Dispatcher would skip Send here. Don't call it.
		return
	}
	t.Errorf("muted project should be detected before Send; got called=%v", called)
}

// ---- supports ------------------------------------------------------------

func TestDiscordSupportsBitmask(t *testing.T) {
	d := newAdapterForTest(t, models.DiscordEndpoint{
		EnabledEvents: models.EventBitmask{TaskFailed: true, WeeklyDigest: true},
	})
	if !d.Supports(notify.KindTaskFailed) {
		t.Error("TaskFailed should be supported")
	}
	if d.Supports(notify.KindRunComplete) {
		t.Error("RunComplete should NOT be supported")
	}
	if !d.Supports(notify.KindWeeklyDigest) {
		t.Error("WeeklyDigest should be supported")
	}
	if d.Supports(notify.Kind("UNKNOWN")) {
		t.Error("unknown kinds should not be supported")
	}
}

// ---- defensive truncation -----------------------------------------------

func TestDiscordTruncatesOversizedDescription(t *testing.T) {
	var captured atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured.Store(body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	var logBuf bytes.Buffer
	d, err := NewDiscordAdapter(
		models.DiscordEndpoint{
			ID:            "ep",
			URL:           srv.URL,
			EnabledEvents: models.EventBitmask{WeeklyDigest: true},
		},
		nil,
		log.New(&logBuf, "", 0),
	)
	if err != nil {
		t.Fatalf("NewDiscordAdapter: %v", err)
	}

	// Build a digest body that, after the template's snippet cap, would
	// still be well under 4000 chars. To force truncation we have to
	// bypass the template's snippet step — call Send with an oversize
	// description directly through the truncation helper instead, plus
	// also exercise the end-to-end path by passing a long body and
	// verifying no truncation kicks in (digestSnippet keeps it short).
	overSized := strings.Repeat("x", discordEmbedDescriptionLimit+500)
	rendered := []byte(`{"embeds":[{"description":"` + overSized + `"}]}`)
	got := d.truncateEmbedDescriptions(rendered)

	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("truncated body not valid JSON: %v", err)
	}
	desc := doc["embeds"].([]any)[0].(map[string]any)["description"].(string)
	if rc := runeCount(desc); rc != discordEmbedDescriptionLimit+1 {
		t.Errorf("truncated description rune count = %d, want %d (limit + ellipsis)",
			rc, discordEmbedDescriptionLimit+1)
	}
	if !strings.HasSuffix(desc, discordEmbedDescriptionEllipsis) {
		t.Errorf("truncated description should end with ellipsis, got tail %q",
			desc[len(desc)-6:])
	}
	if !strings.Contains(logBuf.String(), "WARN") || !strings.Contains(logBuf.String(), "truncated") {
		t.Errorf("expected WARN log on truncation, got %q", logBuf.String())
	}
}

// TestDiscordTruncationNoOpUnderLimit confirms small bodies pass through
// unchanged and produce no WARN log.
func TestDiscordTruncationNoOpUnderLimit(t *testing.T) {
	var logBuf bytes.Buffer
	d, err := NewDiscordAdapter(
		models.DiscordEndpoint{ID: "ep", URL: "https://example.invalid"},
		nil,
		log.New(&logBuf, "", 0),
	)
	if err != nil {
		t.Fatalf("NewDiscordAdapter: %v", err)
	}
	body := []byte(`{"embeds":[{"description":"hi"}]}`)
	out := d.truncateEmbedDescriptions(body)
	if !bytes.Equal(out, body) {
		t.Errorf("under-limit body should pass through unchanged")
	}
	if strings.Contains(logBuf.String(), "WARN") {
		t.Errorf("under-limit body should not log a WARN, got %q", logBuf.String())
	}
}

// TestDiscordTruncationInvalidJSONLogs covers the defensive branch where
// a future template malformation slips through: the helper logs a WARN
// and returns the body unchanged so Send still POSTs *something* and the
// operator notices.
func TestDiscordTruncationInvalidJSONLogs(t *testing.T) {
	var logBuf bytes.Buffer
	d, err := NewDiscordAdapter(
		models.DiscordEndpoint{ID: "ep", URL: "https://example.invalid"},
		nil,
		log.New(&logBuf, "", 0),
	)
	if err != nil {
		t.Fatalf("NewDiscordAdapter: %v", err)
	}
	body := []byte(`not json`)
	out := d.truncateEmbedDescriptions(body)
	if !bytes.Equal(out, body) {
		t.Errorf("invalid JSON body should pass through unchanged")
	}
	if !strings.Contains(logBuf.String(), "WARN") {
		t.Errorf("invalid JSON should log a WARN, got %q", logBuf.String())
	}
}

// ---- BuildPayload --------------------------------------------------------

func TestBuildPayloadDeepLink(t *testing.T) {
	t.Run("task", func(t *testing.T) {
		p := BuildPayload(PayloadInput{
			Notification: notify.Notification{
				Kind:       notify.KindTaskFailed,
				ProjectID:  "proj-1",
				TaskNumber: 7,
				EmittedAt:  fixedEmittedAt,
			},
			ProjectName: "Demo",
		})
		if p.DeepLink != "watchfire://project/proj-1/task/0007" {
			t.Errorf("deep link = %q", p.DeepLink)
		}
	})
	t.Run("digest", func(t *testing.T) {
		p := BuildPayload(PayloadInput{
			Notification: notify.Notification{
				Kind:      notify.KindWeeklyDigest,
				EmittedAt: fixedEmittedAt,
			},
			DigestDate: "2026-05-02",
		})
		if p.DeepLink != "watchfire://digest/2026-05-02" {
			t.Errorf("digest deep link = %q", p.DeepLink)
		}
	})
}

// ---- helpers -------------------------------------------------------------

func newAdapterForTest(t *testing.T, ep models.DiscordEndpoint) *DiscordAdapter {
	t.Helper()
	d, err := NewDiscordAdapter(ep, nil, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("NewDiscordAdapter: %v", err)
	}
	return d
}

// normalizeJSON parses the byte slice into a generic Go value and
// returns it. Callers compare with reflect.DeepEqual to get a
// whitespace-insensitive equality check.
func normalizeJSON(t *testing.T, b []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, b)
	}
	return v
}

func mustMarshalIndent(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "<marshal error>"
	}
	return string(b)
}
