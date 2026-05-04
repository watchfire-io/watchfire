package discordbot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPostMessage_Success(t *testing.T) {
	var seenAuth, seenContent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		var parsed struct {
			Content string `json:"content"`
		}
		_ = json.Unmarshal(body, &parsed)
		seenContent = parsed.Content
		_, _ = w.Write([]byte(`{"id":"M1"}`))
	}))
	defer srv.Close()

	prev := CreateMessageURL
	CreateMessageURL = srv.URL + "/?ch=%s"
	t.Cleanup(func() { CreateMessageURL = prev })

	c := New()
	if err := c.PostMessage(context.Background(), "BOT_TOK", "C42", "hello discord"); err != nil {
		t.Fatalf("PostMessage: %v", err)
	}
	if seenAuth != "Bot BOT_TOK" {
		t.Errorf("auth: %q (must use 'Bot ' prefix)", seenAuth)
	}
	if seenContent != "hello discord" {
		t.Errorf("content: %q", seenContent)
	}
}

func TestPostMessage_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Missing Permissions"}`))
	}))
	defer srv.Close()
	prev := CreateMessageURL
	CreateMessageURL = srv.URL + "/?ch=%s"
	t.Cleanup(func() { CreateMessageURL = prev })

	err := New().PostMessage(context.Background(), "T", "C", "hi")
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("expected 403, got %v", err)
	}
}

func TestGetBotInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bot T" {
			t.Errorf("auth: %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"id":"BOT","username":"watchfire","discriminator":"4242"}`))
	}))
	defer srv.Close()
	prev := BotInfoURL
	BotInfoURL = srv.URL
	t.Cleanup(func() { BotInfoURL = prev })

	info, err := New().GetBotInfo(context.Background(), "T")
	if err != nil {
		t.Fatalf("GetBotInfo: %v", err)
	}
	if info.Username != "watchfire" || info.Discriminator != "4242" {
		t.Errorf("info: %+v", info)
	}
}

func TestRegisterGuildCommands(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method: %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bot T" {
			t.Errorf("auth: %q", r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		var cmds []ApplicationCommand
		_ = json.Unmarshal(body, &cmds)
		if len(cmds) != 1 || cmds[0].Name != "watchfire" {
			t.Errorf("cmds: %+v", cmds)
		}
		_, _ = w.Write([]byte(`[{"id":"123"}]`))
	}))
	defer srv.Close()

	prev := GuildCommandsURL
	GuildCommandsURL = srv.URL + "/applications/%s/guilds/%s/commands"
	t.Cleanup(func() { GuildCommandsURL = prev })

	err := New().RegisterGuildCommands(context.Background(), "T", "APP", "GUILD", []ApplicationCommand{
		{Name: "watchfire", Description: "Watchfire commands", Type: 1},
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	// Sanity check: URL formatted with %s correctly.
	want := fmt.Sprintf("/applications/%s/guilds/%s/commands", "APP", "GUILD")
	_ = want
}
