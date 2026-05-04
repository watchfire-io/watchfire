package slackbot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPostMessage_Success(t *testing.T) {
	var seenAuth, seenChannel, seenText string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		var parsed struct {
			Channel string `json:"channel"`
			Text    string `json:"text"`
		}
		_ = json.Unmarshal(body, &parsed)
		seenChannel, seenText = parsed.Channel, parsed.Text
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	prev := PostMessageURL
	PostMessageURL = srv.URL
	t.Cleanup(func() { PostMessageURL = prev })

	c := New()
	if err := c.PostMessage(context.Background(), "xoxb-fake", "#general", "hello"); err != nil {
		t.Fatalf("PostMessage: %v", err)
	}
	if seenAuth != "Bearer xoxb-fake" {
		t.Errorf("auth header: %q", seenAuth)
	}
	if seenChannel != "#general" || seenText != "hello" {
		t.Errorf("body: ch=%q text=%q", seenChannel, seenText)
	}
}

func TestPostMessage_NotOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error":"channel_not_found"}`))
	}))
	defer srv.Close()
	prev := PostMessageURL
	PostMessageURL = srv.URL
	t.Cleanup(func() { PostMessageURL = prev })

	err := New().PostMessage(context.Background(), "tok", "#nope", "hi")
	if err == nil || !strings.Contains(err.Error(), "channel_not_found") {
		t.Fatalf("expected channel_not_found, got %v", err)
	}
}

func TestPostMessage_RejectsEmpty(t *testing.T) {
	c := New()
	if err := c.PostMessage(context.Background(), "", "#general", "hi"); err == nil {
		t.Fatal("expected empty-token rejection")
	}
	if err := c.PostMessage(context.Background(), "tok", "", "hi"); err == nil {
		t.Fatal("expected empty-channel rejection")
	}
}

func TestAuthTest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer T" {
			t.Errorf("auth: %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"ok":true,"user":"watchfire"}`))
	}))
	defer srv.Close()
	prev := AuthTestURL
	AuthTestURL = srv.URL
	t.Cleanup(func() { AuthTestURL = prev })

	got, err := New().AuthTest(context.Background(), "T")
	if err != nil {
		t.Fatalf("AuthTest: %v", err)
	}
	if got != "watchfire" {
		t.Errorf("user: %q", got)
	}
}
