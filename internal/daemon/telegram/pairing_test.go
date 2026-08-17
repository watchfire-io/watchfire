package telegram

import (
	"strings"
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/models"
)

func TestGenerateCodeShapeAndAlphabet(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		code, err := generateCode()
		if err != nil {
			t.Fatalf("generateCode: %v", err)
		}
		if len(code) != CodeLength {
			t.Fatalf("code length %d, want %d: %q", len(code), CodeLength, code)
		}
		for _, r := range code {
			if !strings.ContainsRune(codeAlphabet, r) {
				t.Fatalf("code %q contains %q outside the alphabet", code, r)
			}
		}
		for _, ambiguous := range "0O1Il" {
			if strings.ContainsRune(code, ambiguous) {
				t.Fatalf("code %q contains ambiguous char %q", code, ambiguous)
			}
		}
		seen[code] = true
	}
	if len(seen) < 45 {
		t.Fatalf("suspiciously many duplicate codes: %d unique of 50", len(seen))
	}
}

func TestPairingLifecycle(t *testing.T) {
	p := NewPairing()

	// Nothing issued yet: nothing redeems, state none.
	if p.Consume("") || p.Consume("ANYTHING") {
		t.Fatal("consume must fail before any Begin")
	}
	if st := p.Status(); st.State != StateNone {
		t.Fatalf("initial state = %v, want none", st.State)
	}

	code, expires, err := p.Begin(0)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if until := time.Until(expires); until < 9*time.Minute || until > 10*time.Minute+time.Second {
		t.Fatalf("default TTL out of range: %s", until)
	}
	if st := p.Status(); st.State != StatePending || !st.ExpiresAt.Equal(expires) {
		t.Fatalf("pending status mismatch: %+v", st)
	}

	// Wrong code: no redeem, still pending.
	if p.Consume("WRONGCOD") {
		t.Fatal("wrong code redeemed")
	}
	if st := p.Status(); st.State != StatePending {
		t.Fatalf("state after wrong code = %v, want pending", st.State)
	}

	// Right code redeems once, case-insensitively — and only once.
	if !p.Consume(strings.ToLower(code)) {
		t.Fatal("valid code (lowercased) did not redeem")
	}
	if p.Consume(code) {
		t.Fatal("code redeemed twice — must be single-use")
	}

	// Complete flips to paired and carries the chat.
	p.Complete(models.TelegramPairedChat{ChatID: 42, Username: "nuno"})
	st := p.Status()
	if st.State != StatePaired || st.Chat == nil || st.Chat.ChatID != 42 {
		t.Fatalf("paired status mismatch: %+v", st)
	}

	// A new Begin clears the paired result and returns to pending.
	if _, _, err := p.Begin(time.Minute); err != nil {
		t.Fatalf("second Begin: %v", err)
	}
	if st := p.Status(); st.State != StatePending || st.Chat != nil {
		t.Fatalf("state after re-Begin = %+v, want pending", st)
	}
}

func TestPairingBeginInvalidatesPriorCode(t *testing.T) {
	p := NewPairing()
	code1, _, err := p.Begin(0)
	if err != nil {
		t.Fatalf("Begin 1: %v", err)
	}
	code2, _, err := p.Begin(0)
	if err != nil {
		t.Fatalf("Begin 2: %v", err)
	}
	if code1 == code2 {
		t.Fatalf("two Begins minted the same code %q", code1)
	}
	if p.Consume(code1) {
		t.Fatal("invalidated first code still redeemed")
	}
	if !p.Consume(code2) {
		t.Fatal("active second code failed to redeem")
	}
}

func TestPairingTTLExpiry(t *testing.T) {
	p := NewPairing()
	now := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	p.now = func() time.Time { return now }

	code, expires, err := p.Begin(10 * time.Minute)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if !expires.Equal(now.Add(10 * time.Minute)) {
		t.Fatalf("expiry = %s, want %s", expires, now.Add(10*time.Minute))
	}

	// One second before expiry: still redeemable (but don't consume yet).
	now = expires.Add(-time.Second)
	if st := p.Status(); st.State != StatePending {
		t.Fatalf("state just before expiry = %v, want pending", st.State)
	}

	// Past expiry: expired state, code refuses to redeem.
	now = expires.Add(time.Second)
	if st := p.Status(); st.State != StateExpired {
		t.Fatalf("state past expiry = %v, want expired", st.State)
	}
	if p.Consume(code) {
		t.Fatal("expired code redeemed")
	}
}
