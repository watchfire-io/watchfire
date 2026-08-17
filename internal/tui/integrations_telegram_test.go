package tui

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/watchfire-io/watchfire/proto"
)

// telegramTestConfig builds an IntegrationsConfig with a configured
// Telegram bridge and two paired chats exercising every per-chat field.
func telegramTestConfig() *pb.IntegrationsConfig {
	return &pb.IntegrationsConfig{
		Github: &pb.GitHubIntegration{},
		Telegram: &pb.TelegramIntegration{
			Enabled:       true,
			TokenSet:      true,
			EnabledEvents: &pb.IntegrationEvents{TaskFailed: true, RunComplete: true},
			PairedChats: []*pb.TelegramPairedChatInfo{
				{ChatId: 111, Username: "alice", Muted: true},
				{ChatId: 222, DefaultProjectId: "proj-1", Watch: true},
			},
		},
	}
}

// TestTelegramRowsRendered pins the list-mode shape: a Telegram summary
// row is always present, and each paired chat renders its own row with
// identity + default project + muted/watch flags.
func TestTelegramRowsRendered(t *testing.T) {
	out := renderListSnapshot(telegramTestConfig())
	mustContain(t, out, "Telegram bridge — enabled, token set, 2 chat(s)")
	mustContain(t, out, "@alice")
	mustContain(t, out, "id 111")
	mustContain(t, out, "muted")
	mustContain(t, out, "chat 222")
	mustContain(t, out, "project proj-1")
	mustContain(t, out, "watch")
	// Footer documents the telegram keys.
	mustContain(t, out, "p pair telegram")
	mustContain(t, out, "m/w mute/watch chat")
}

// TestTelegramRowUnconfigured verifies the summary row still shows up
// (as "not configured") when the daemon has no telegram config, so the
// user can discover the feature and 'e' into the add form.
func TestTelegramRowUnconfigured(t *testing.T) {
	out := renderListSnapshot(&pb.IntegrationsConfig{Github: &pb.GitHubIntegration{}})
	mustContain(t, out, "Telegram bridge (not configured)")
}

// TestTelegramCycleAddKind verifies the add form's kind picker includes
// Telegram and wraps correctly in both directions.
func TestTelegramCycleAddKind(t *testing.T) {
	f := NewIntegrationsForm()
	f.Load(&pb.IntegrationsConfig{Github: &pb.GitHubIntegration{}})
	f.StartAdd()

	f.CycleAddKind(-1) // Webhook → wraps back to Telegram
	if f.addKind != integrationsRowTelegram {
		t.Fatalf("cycle -1 from Webhook should wrap to Telegram, got %v", f.addKind)
	}
	f.CycleAddKind(+1) // Telegram → wraps forward to Webhook
	if f.addKind != integrationsRowWebhook {
		t.Fatalf("cycle +1 from Telegram should wrap to Webhook, got %v", f.addKind)
	}
	for i := 0; i < 4; i++ { // Webhook → Slack → Discord → GitHub → Telegram
		f.CycleAddKind(+1)
	}
	if f.addKind != integrationsRowTelegram {
		t.Fatalf("four steps from Webhook should land on Telegram, got %v", f.addKind)
	}
}

// TestTelegramAddFlow walks the telegram add form: kind → masked token
// → enabled + events step (terminal — no mutes step) → snapshot.
func TestTelegramAddFlow(t *testing.T) {
	f := NewIntegrationsForm()
	f.Load(&pb.IntegrationsConfig{Github: &pb.GitHubIntegration{}})
	f.StartAdd()
	f.addKind = integrationsRowTelegram

	if done := f.AdvanceAdd(); done {
		t.Fatalf("kind step should not be terminal")
	}
	if f.addStep != addFieldURL {
		t.Fatalf("step after kind should be the token input, got %v", f.addStep)
	}

	f.input.SetValue("123456:ABC-token")
	if done := f.AdvanceAdd(); done {
		t.Fatalf("token step should not be terminal")
	}
	if f.addStep != addFieldEvents {
		t.Fatalf("telegram should skip the label step, got %v", f.addStep)
	}

	f.ToggleAddEvent(0) // Enabled: default true → false
	f.ToggleAddEvent(3) // WEEKLY_DIGEST: default false → true

	if done := f.AdvanceAdd(); !done {
		t.Fatalf("events step should be terminal for telegram")
	}

	got := f.TelegramAddSnapshot()
	if got.GetEnabled() {
		t.Errorf("enabled toggle should have flipped off")
	}
	if got.GetBotToken() != "123456:ABC-token" {
		t.Errorf("token not captured: %q", got.GetBotToken())
	}
	ev := got.GetEnabledEvents()
	if !ev.GetTaskFailed() || !ev.GetRunComplete() || !ev.GetWeeklyDigest() {
		t.Errorf("events snapshot wrong: %+v", ev)
	}
	if got.GetPairedChats() != nil {
		t.Errorf("add snapshot must never carry paired chats")
	}
}

// TestTelegramAddEmptyTokenKeeps verifies the write-only convention: an
// empty token input still advances (meaning "keep the stored token")
// and the snapshot carries an empty BotToken.
func TestTelegramAddEmptyTokenKeeps(t *testing.T) {
	f := NewIntegrationsForm()
	f.Load(telegramTestConfig())
	f.StartTelegramEdit()

	if f.addStep != addFieldURL {
		t.Fatalf("StartTelegramEdit should land on the token step, got %v", f.addStep)
	}
	if !f.addTelegramEnabled {
		t.Fatalf("edit should prefill enabled=true from the config")
	}
	if !f.addEvents.TaskFailed || !f.addEvents.RunComplete || f.addEvents.WeeklyDigest {
		t.Fatalf("edit should prefill events from the config, got failed=%v complete=%v digest=%v",
			f.addEvents.TaskFailed, f.addEvents.RunComplete, f.addEvents.WeeklyDigest)
	}

	// Leave the token empty and advance — legal for telegram.
	if done := f.AdvanceAdd(); done {
		t.Fatalf("empty token should advance to events, not finish")
	}
	if f.addStep != addFieldEvents {
		t.Fatalf("empty token should still reach the events step, got %v", f.addStep)
	}
	if done := f.AdvanceAdd(); !done {
		t.Fatalf("events step should be terminal")
	}
	if got := f.TelegramAddSnapshot().GetBotToken(); got != "" {
		t.Errorf("empty input must mean keep-token, got %q", got)
	}
}

// TestTelegramPairingRender pins the pairing block lifecycle: pending
// shows code + deep link + countdown, paired shows the success line,
// expired prompts for a fresh code.
func TestTelegramPairingRender(t *testing.T) {
	f := NewIntegrationsForm()
	f.SetWidth(80)
	f.Load(telegramTestConfig())

	f.StartPairing(&pb.BeginTelegramPairingResponse{
		Code:        "ABCD1234",
		DeepLink:    "https://t.me/testbot?start=ABCD1234",
		BotUsername: "testbot",
		ExpiresAt:   timestamppb.New(time.Now().Add(5 * time.Minute)),
	})
	if !f.PairingActive() {
		t.Fatalf("pairing should be active after StartPairing")
	}
	out := f.View()
	mustContain(t, out, "ABCD1234")
	mustContain(t, out, "https://t.me/testbot?start=ABCD1234")
	mustContain(t, out, "expires in")
	mustContain(t, out, "/pair ABCD1234")

	f.LoadPairingStatus(&pb.TelegramPairingStatus{
		State: pb.TelegramPairingState_TELEGRAM_PAIRING_PAIRED,
		Chat:  &pb.TelegramPairedChatInfo{ChatId: 333, Username: "bob"},
	})
	if f.PairingActive() {
		t.Fatalf("pairing should not stay active once paired")
	}
	mustContain(t, f.View(), "✓ Paired with @bob")

	f.StartPairing(&pb.BeginTelegramPairingResponse{Code: "X", ExpiresAt: timestamppb.New(time.Now())})
	f.LoadPairingStatus(&pb.TelegramPairingStatus{State: pb.TelegramPairingState_TELEGRAM_PAIRING_EXPIRED})
	mustContain(t, f.View(), "expired")

	f.Reset()
	if strings.Contains(f.View(), "Pairing") {
		t.Errorf("Reset should clear the pairing block")
	}
}

// TestCountdownLabel pins the countdown formatting.
func TestCountdownLabel(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		until time.Time
		want  string
	}{
		{now.Add(9*time.Minute + 58*time.Second), "9m58s"},
		{now.Add(45 * time.Second), "45s"},
		{now.Add(-time.Second), "expired"},
		{now, "expired"},
		{now.Add(time.Minute), "1m00s"},
	}
	for _, tc := range cases {
		if got := countdownLabel(tc.until, now); got != tc.want {
			t.Errorf("countdownLabel(%v): want %q got %q", tc.until.Sub(now), tc.want, got)
		}
	}
}

// TestTelegramChatTogglePayload verifies the mute/watch toggle payload
// preserves everything the Save merge would otherwise reset: enabled
// state, events, other chats' flags, and the target chat's default
// project.
func TestTelegramChatTogglePayload(t *testing.T) {
	tg := telegramTestConfig().GetTelegram()

	got := telegramChatTogglePayload(tg, 222, "mute")
	if got == nil {
		t.Fatalf("payload should build for a known chat")
	}
	if !got.GetEnabled() {
		t.Errorf("enabled state must be preserved")
	}
	if got.GetBotToken() != "" {
		t.Errorf("toggle must never carry a token (empty = keep)")
	}
	ev := got.GetEnabledEvents()
	if !ev.GetTaskFailed() || !ev.GetRunComplete() || ev.GetWeeklyDigest() {
		t.Errorf("events must be preserved, got %+v", ev)
	}
	if len(got.GetPairedChats()) != 2 {
		t.Fatalf("all chats must ride along, got %d", len(got.GetPairedChats()))
	}
	var c111, c222 *pb.TelegramPairedChatInfo
	for _, pc := range got.GetPairedChats() {
		switch pc.GetChatId() {
		case 111:
			c111 = pc
		case 222:
			c222 = pc
		}
	}
	if c111 == nil || !c111.GetMuted() || c111.GetWatch() {
		t.Errorf("untouched chat 111 must keep its flags, got %+v", c111)
	}
	if c222 == nil || !c222.GetMuted() {
		t.Errorf("chat 222 mute should have flipped on, got %+v", c222)
	}
	if c222.GetDefaultProjectId() != "proj-1" || !c222.GetWatch() {
		t.Errorf("chat 222 must keep default project + watch, got %+v", c222)
	}

	// Watch flips independently of mute.
	got = telegramChatTogglePayload(tg, 222, "watch")
	for _, pc := range got.GetPairedChats() {
		if pc.GetChatId() == 222 && (pc.GetWatch() || pc.GetMuted()) {
			t.Errorf("chat 222 watch should have flipped off and mute stayed off, got %+v", pc)
		}
	}

	if telegramChatTogglePayload(tg, 999, "mute") != nil {
		t.Errorf("unknown chat must yield nil payload")
	}
	if telegramChatTogglePayload(nil, 111, "mute") != nil {
		t.Errorf("nil config must yield nil payload")
	}
}
