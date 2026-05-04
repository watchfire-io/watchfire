package oauth

import (
	"errors"
	"testing"
	"time"
)

func TestStateStore_BeginConsume(t *testing.T) {
	s := NewStateStore()
	state, err := s.Begin("slack", "client", "secret", "http://127.0.0.1:1234/oauth/slack/callback", "#general")
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if state == "" {
		t.Fatal("expected non-empty state value")
	}

	clientID, clientSecret, redirectURI, channel, err := s.Consume("slack", state)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if clientID != "client" || clientSecret != "secret" {
		t.Fatalf("unexpected client creds: id=%s secret=%s", clientID, clientSecret)
	}
	if redirectURI != "http://127.0.0.1:1234/oauth/slack/callback" {
		t.Fatalf("unexpected redirect: %s", redirectURI)
	}
	if channel != "#general" {
		t.Fatalf("unexpected channel: %s", channel)
	}
}

func TestStateStore_ConsumeReplay(t *testing.T) {
	s := NewStateStore()
	state, _ := s.Begin("slack", "c", "s", "r", "")
	if _, _, _, _, err := s.Consume("slack", state); err != nil {
		t.Fatalf("first consume failed: %v", err)
	}
	// Replay must fail.
	if _, _, _, _, err := s.Consume("slack", state); !errors.Is(err, ErrUnknownState) {
		t.Fatalf("replay: want ErrUnknownState, got %v", err)
	}
}

func TestStateStore_ProviderMismatch(t *testing.T) {
	s := NewStateStore()
	state, _ := s.Begin("slack", "c", "s", "r", "")
	if _, _, _, _, err := s.Consume("discord", state); !errors.Is(err, ErrProviderMismatch) {
		t.Fatalf("provider mismatch: want ErrProviderMismatch, got %v", err)
	}
	// State entry should still exist after mismatch — a follow-up
	// callback to the correct provider should still succeed.
	if _, _, _, _, err := s.Consume("slack", state); err != nil {
		t.Fatalf("after mismatch: want clean consume, got %v", err)
	}
}

func TestStateStore_Expiry(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := NewStateStore().WithClock(func() time.Time { return now })
	state, _ := s.Begin("slack", "c", "s", "r", "")

	now = now.Add(11 * time.Minute) // > stateTTL
	if _, _, _, _, err := s.Consume("slack", state); !errors.Is(err, ErrUnknownState) {
		t.Fatalf("expired: want ErrUnknownState, got %v", err)
	}
}

func TestStateStore_Pending(t *testing.T) {
	s := NewStateStore()
	if s.Pending("slack") {
		t.Fatal("empty store should not report pending")
	}
	state, _ := s.Begin("slack", "c", "s", "r", "")
	if !s.Pending("slack") {
		t.Fatal("after Begin should be pending")
	}
	if s.Pending("discord") {
		t.Fatal("Begin('slack') should not light up discord")
	}
	_, _, _, _, _ = s.Consume("slack", state)
	if s.Pending("slack") {
		t.Fatal("after Consume should not be pending")
	}
}
