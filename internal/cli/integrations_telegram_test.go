package cli

import (
	"testing"

	pb "github.com/watchfire-io/watchfire/proto"
)

// TestParseIntegrationKindTable pins the kind-string parsing used by
// `watchfire integrations test <kind> <id>`, telegram included.
func TestParseIntegrationKindTable(t *testing.T) {
	cases := []struct {
		in      string
		want    pb.IntegrationKind
		wantErr bool
	}{
		{"webhook", pb.IntegrationKind_WEBHOOK, false},
		{"slack", pb.IntegrationKind_SLACK, false},
		{"discord", pb.IntegrationKind_DISCORD, false},
		{"github", pb.IntegrationKind_GITHUB, false},
		{"telegram", pb.IntegrationKind_TELEGRAM, false},
		{"Telegram", pb.IntegrationKind_TELEGRAM, false}, // case-insensitive
		{"signal", 0, true},
		{"", 0, true},
	}
	for _, tc := range cases {
		got, err := parseIntegrationKind(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseIntegrationKind(%q): expected error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseIntegrationKind(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseIntegrationKind(%q): want %v got %v", tc.in, tc.want, got)
		}
	}
}

// TestDetectIntegrationKindTelegram pins the single-arg auto-detect
// path for `watchfire integrations test telegram`.
func TestDetectIntegrationKindTelegram(t *testing.T) {
	cases := []struct {
		name   string
		cfg    *pb.IntegrationsConfig
		id     string
		want   pb.IntegrationKind
		wantOK bool
	}{
		{
			name:   "configured telegram matches id telegram",
			cfg:    &pb.IntegrationsConfig{Telegram: &pb.TelegramIntegration{Enabled: true}},
			id:     "telegram",
			want:   pb.IntegrationKind_TELEGRAM,
			wantOK: true,
		},
		{
			name:   "disabled telegram still detectable (test surfaces the disabled state)",
			cfg:    &pb.IntegrationsConfig{Telegram: &pb.TelegramIntegration{}},
			id:     "telegram",
			want:   pb.IntegrationKind_TELEGRAM,
			wantOK: true,
		},
		{
			name:   "unconfigured telegram not detected",
			cfg:    &pb.IntegrationsConfig{},
			id:     "telegram",
			wantOK: false,
		},
		{
			name:   "nil config",
			cfg:    nil,
			id:     "telegram",
			wantOK: false,
		},
		{
			name: "webhook id wins over telegram fallback",
			cfg: &pb.IntegrationsConfig{
				Webhooks: []*pb.WebhookIntegration{{Id: "telegram"}},
				Telegram: &pb.TelegramIntegration{},
			},
			id:     "telegram",
			want:   pb.IntegrationKind_WEBHOOK,
			wantOK: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := detectIntegrationKind(tc.cfg, tc.id)
			if ok != tc.wantOK {
				t.Fatalf("ok: want %v got %v", tc.wantOK, ok)
			}
			if ok && got != tc.want {
				t.Errorf("kind: want %v got %v", tc.want, got)
			}
		})
	}
}

// TestBuildTelegramAddPayload pins the `integrations add telegram`
// payload semantics: fresh add gets defaults, re-add preserves the
// configured events, and the result is always enabled with the token
// attached and no paired chats.
func TestBuildTelegramAddPayload(t *testing.T) {
	cases := []struct {
		name     string
		existing *pb.TelegramIntegration
		token    string
		want     *pb.IntegrationEvents
	}{
		{
			name:     "fresh add uses default events",
			existing: nil,
			token:    "111:AAA",
			want:     &pb.IntegrationEvents{TaskFailed: true, RunComplete: true},
		},
		{
			name:     "existing without events uses defaults",
			existing: &pb.TelegramIntegration{Enabled: false},
			token:    "111:AAA",
			want:     &pb.IntegrationEvents{TaskFailed: true, RunComplete: true},
		},
		{
			name: "token rotation preserves configured events",
			existing: &pb.TelegramIntegration{
				Enabled:       false,
				EnabledEvents: &pb.IntegrationEvents{WeeklyDigest: true},
				PairedChats:   []*pb.TelegramPairedChatInfo{{ChatId: 1}},
			},
			token: "222:BBB",
			want:  &pb.IntegrationEvents{WeeklyDigest: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildTelegramAddPayload(tc.existing, tc.token)
			if !got.GetEnabled() {
				t.Errorf("add must always come out enabled")
			}
			if got.GetBotToken() != tc.token {
				t.Errorf("token: want %q got %q", tc.token, got.GetBotToken())
			}
			ev := got.GetEnabledEvents()
			if ev.GetTaskFailed() != tc.want.GetTaskFailed() ||
				ev.GetRunComplete() != tc.want.GetRunComplete() ||
				ev.GetWeeklyDigest() != tc.want.GetWeeklyDigest() {
				t.Errorf("events: want %+v got %+v", tc.want, ev)
			}
			if got.GetPairedChats() != nil {
				t.Errorf("add payload must never carry paired chats (the daemon-side merge owns them)")
			}
		})
	}
}
