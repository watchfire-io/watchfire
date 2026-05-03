package discord

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestRegistrarRegistersOnGuildCreate: pushing a GuildEventCreate through
// the gateway's events channel should produce a successful per-guild
// status entry once the registrar's HTTP POSTs come back 2xx.
func TestRegistrarRegistersOnGuildCreate(t *testing.T) {
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		posts.Add(1)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintln(w, `{"id":"x"}`)
	}))
	defer srv.Close()

	// We hand-build a Gateway whose Events channel we control directly
	// (bypassing the websocket). Tests that exercise the dispatcher
	// itself live in gateway_test.go.
	gw := &Gateway{events: make(chan GuildEvent, 4)}
	reg := NewRegistrar(RegistrarConfig{
		Gateway:      gw,
		AppID:        "app-1",
		Token:        "tok",
		CommandsBase: srv.URL,
		HTTPClient:   srv.Client(),
		Logger:       log.New(io.Discard, "", 0),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		reg.Run(ctx)
		close(done)
	}()

	gw.events <- GuildEvent{Type: GuildEventCreate, GuildID: "g1", GuildName: "Guild One"}

	// Wait for the registrar to record the status. Three commands × one
	// guild = 3 POSTs.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s, ok := reg.Status("g1"); ok && s.Registered {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	s, ok := reg.Status("g1")
	if !ok {
		t.Fatal("expected status for g1")
	}
	if !s.Registered {
		t.Fatalf("expected registered, got error %q", s.Error)
	}
	if s.GuildName != "Guild One" {
		t.Fatalf("expected guild name preserved, got %q", s.GuildName)
	}
	if posts.Load() != 3 {
		t.Fatalf("expected 3 POSTs (one per command), got %d", posts.Load())
	}

	cancel()
	close(gw.events)
	<-done
}

// TestRegistrarHandlesPerCommand4xx: when Discord returns 403 on a
// command, the registrar records the guild as not-registered and
// surfaces the error string.
func TestRegistrarHandlesPerCommand4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"missing access","code":50001}`, http.StatusForbidden)
	}))
	defer srv.Close()

	gw := &Gateway{events: make(chan GuildEvent, 1)}
	reg := NewRegistrar(RegistrarConfig{
		Gateway:      gw,
		AppID:        "app-1",
		Token:        "tok",
		CommandsBase: srv.URL,
		HTTPClient:   srv.Client(),
		Logger:       log.New(io.Discard, "", 0),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		reg.Run(ctx)
		close(done)
	}()

	gw.events <- GuildEvent{Type: GuildEventCreate, GuildID: "g2", GuildName: "Guild Two"}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s, ok := reg.Status("g2"); ok && s.Error != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	s, ok := reg.Status("g2")
	if !ok {
		t.Fatal("expected status for g2")
	}
	if s.Registered {
		t.Fatalf("expected not registered, got Registered=true")
	}
	if s.Error == "" {
		t.Fatal("expected Error to carry the per-command failure message")
	}

	cancel()
	close(gw.events)
	<-done
}

// TestRegistrarRemovesGuildOnDelete: GuildEventDelete drops the guild
// from the status snapshot so kicked guilds disappear from the UI.
func TestRegistrarRemovesGuildOnDelete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"id":"x"}`)
	}))
	defer srv.Close()

	gw := &Gateway{events: make(chan GuildEvent, 4)}
	reg := NewRegistrar(RegistrarConfig{
		Gateway:      gw,
		AppID:        "app-1",
		Token:        "tok",
		CommandsBase: srv.URL,
		HTTPClient:   srv.Client(),
		Logger:       log.New(io.Discard, "", 0),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		reg.Run(ctx)
		close(done)
	}()

	gw.events <- GuildEvent{Type: GuildEventCreate, GuildID: "g3", GuildName: "Guild Three"}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := reg.Status("g3"); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok := reg.Status("g3"); !ok {
		t.Fatal("expected status for g3 after create")
	}

	gw.events <- GuildEvent{Type: GuildEventDelete, GuildID: "g3"}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := reg.Status("g3"); !ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok := reg.Status("g3"); ok {
		t.Fatal("expected status for g3 to be removed after delete")
	}

	cancel()
	close(gw.events)
	<-done
}
