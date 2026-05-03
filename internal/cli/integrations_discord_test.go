package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/watchfire-io/watchfire/internal/daemon/discord"
)

func TestRegisterDiscordCommandsHappyPath(t *testing.T) {
	var (
		mu       sync.Mutex
		captured []discord.SlashCommand
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bot test-token" {
			t.Fatalf("expected Bot auth, got %q", r.Header.Get("Authorization"))
		}
		if !strings.Contains(r.URL.Path, "/applications/app-1/guilds/guild-1/commands") {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		var cmd discord.SlashCommand
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &cmd); err != nil {
			t.Fatalf("malformed request body: %v", err)
		}
		mu.Lock()
		captured = append(captured, cmd)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"id":"x","name":%q}`, cmd.Name)
	}))
	defer srv.Close()

	results, err := discord.RegisterGuildCommands(context.Background(), srv.Client(), srv.URL, "app-1", "guild-1", "test-token", discord.WatchfireSlashCommands())
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	for _, r := range results {
		if !r.OK {
			t.Fatalf("command %q failed: %s", r.Name, r.Err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(captured) != 3 {
		t.Fatalf("expected 3 captured calls, got %d", len(captured))
	}
	names := []string{captured[0].Name, captured[1].Name, captured[2].Name}
	want := []string{"status", "retry", "cancel"}
	for i, n := range names {
		if n != want[i] {
			t.Fatalf("expected command[%d]=%q, got %q", i, want[i], n)
		}
	}
	// Schema sanity: retry + cancel require a string `task` arg.
	for _, c := range captured[1:] {
		if len(c.Options) != 1 || c.Options[0].Name != "task" || !c.Options[0].Required {
			t.Fatalf("expected required 'task' option on %s, got %+v", c.Name, c.Options)
		}
	}
}

func TestRegisterDiscordCommandsHandles4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"missing access","code":50001}`, http.StatusForbidden)
	}))
	defer srv.Close()

	results, err := discord.RegisterGuildCommands(context.Background(), srv.Client(), srv.URL, "app-1", "guild-1", "test-token", discord.WatchfireSlashCommands())
	if err != nil {
		t.Fatalf("register should not return Go error on 4xx, got %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	for _, r := range results {
		if r.OK {
			t.Fatalf("expected per-command failure, got OK")
		}
		if !strings.Contains(r.Err, "403") {
			t.Fatalf("expected 403 in err, got %q", r.Err)
		}
	}
}

func TestRegisterDiscordCommandsIdempotent(t *testing.T) {
	// Discord upserts on POST when the command name already exists.
	// The CLI doesn't need to do anything special — just that re-running
	// produces the same result without a Go-level error.
	count := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		w.WriteHeader(http.StatusOK) // upsert path
		fmt.Fprintln(w, `{"id":"x"}`)
	}))
	defer srv.Close()

	for i := 0; i < 2; i++ {
		results, err := discord.RegisterGuildCommands(context.Background(), srv.Client(), srv.URL, "app", "guild", "tok", discord.WatchfireSlashCommands())
		if err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
		for _, r := range results {
			if !r.OK {
				t.Fatalf("attempt %d: command %q failed: %s", i, r.Name, r.Err)
			}
		}
	}
	if count != 6 {
		t.Fatalf("expected 6 total POSTs (3 commands × 2 runs), got %d", count)
	}
}
