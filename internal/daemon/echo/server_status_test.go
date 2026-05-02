package echo

import (
	"testing"
	"time"

	"github.com/watchfire-io/watchfire/internal/models"
)

// TestRecordAndLastDelivery: per-provider timestamps round-trip through
// RecordDelivery / LastDelivery and stay zero for providers that haven't
// fired yet. Backs the IntegrationsService.GetInboundStatus RPC's
// `last_*_delivery_unix` fields.
func TestRecordAndLastDelivery(t *testing.T) {
	srv := New(models.InboundConfig{}, nil)

	if !srv.LastDelivery("github").IsZero() {
		t.Fatal("expected zero LastDelivery before any RecordDelivery call")
	}

	before := time.Now().UTC().Add(-time.Second)
	srv.RecordDelivery("github")
	got := srv.LastDelivery("github")
	if got.IsZero() || got.Before(before) {
		t.Fatalf("LastDelivery did not advance: %v (before=%v)", got, before)
	}

	// Slack still untouched.
	if !srv.LastDelivery("slack").IsZero() {
		t.Fatal("recording GitHub leaked into Slack timestamp")
	}

	// Empty provider name is a safe no-op (no panic, no unintended insert).
	srv.RecordDelivery("")
	if srv.LastDelivery("") != (time.Time{}) {
		t.Fatal("empty-key delivery should not register")
	}
}

// TestBindErrorEmptyByDefault: BindError reports empty until Run is
// called against an unbindable address. We don't actually bind here
// (avoids racing the listener) — the contract is "empty when never
// attempted, populated on bind failure".
func TestBindErrorEmptyByDefault(t *testing.T) {
	srv := New(models.InboundConfig{}, nil)
	if got := srv.BindError(); got != "" {
		t.Fatalf("BindError on fresh server should be empty, got %q", got)
	}
}
