package oauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildSlackAuthURL(t *testing.T) {
	url := BuildSlackAuthURL("CLIENT", "http://127.0.0.1:1234/oauth/slack/callback", "STATE_TOKEN")
	if !strings.HasPrefix(url, "https://slack.com/oauth/v2/authorize?") {
		t.Fatalf("unexpected prefix: %s", url)
	}
	for _, want := range []string{
		"client_id=CLIENT",
		"state=STATE_TOKEN",
		// scope should be url-encoded — strings.Contains on the
		// pre-encoded form catches the colon-separated keys.
		"scope=chat",
	} {
		if !strings.Contains(url, want) {
			t.Errorf("authorize URL missing %q: %s", want, url)
		}
	}
}

func TestExchangeSlackCode_Success(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if got := r.PostForm.Get("code"); got != "AUTH_CODE" {
			t.Errorf("code: got %q", got)
		}
		if got := r.PostForm.Get("client_id"); got != "CLIENT" {
			t.Errorf("client_id: got %q", got)
		}
		if got := r.PostForm.Get("client_secret"); got != "SECRET" {
			t.Errorf("client_secret: got %q", got)
		}
		_, _ = w.Write([]byte(`{
			"ok": true,
			"access_token": "xoxb-fake-token",
			"bot_user_id": "U12345",
			"team": {"id": "T0001", "name": "Acme HQ"}
		}`))
	}))
	defer tokenSrv.Close()

	authTestSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer xoxb-fake-token" {
			t.Errorf("auth.test missing bearer: %s", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"ok":true,"user":"watchfire-bot"}`))
	}))
	defer authTestSrv.Close()

	prevToken, prevAuth := SlackTokenURL, SlackAuthTestURL
	SlackTokenURL, SlackAuthTestURL = tokenSrv.URL, authTestSrv.URL
	t.Cleanup(func() { SlackTokenURL, SlackAuthTestURL = prevToken, prevAuth })

	install, err := ExchangeSlackCode(context.Background(), nil, "CLIENT", "SECRET", "AUTH_CODE", "http://127.0.0.1/cb")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if install.BotToken != "xoxb-fake-token" {
		t.Errorf("bot token: %q", install.BotToken)
	}
	if install.TeamID != "T0001" || install.TeamName != "Acme HQ" {
		t.Errorf("team: %+v", install)
	}
	if install.BotUsername != "watchfire-bot" {
		t.Errorf("bot username: %q", install.BotUsername)
	}
}

func TestExchangeSlackCode_NotOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok": false, "error": "invalid_code"}`))
	}))
	defer srv.Close()
	prev := SlackTokenURL
	SlackTokenURL = srv.URL
	t.Cleanup(func() { SlackTokenURL = prev })

	_, err := ExchangeSlackCode(context.Background(), nil, "C", "S", "BAD", "")
	if err == nil {
		t.Fatal("expected error from not-ok response")
	}
	var tx *ErrTokenExchange
	if !errors.As(err, &tx) {
		t.Fatalf("expected *ErrTokenExchange, got %T", err)
	}
	if tx.Provider != "slack" || !strings.Contains(tx.Message, "invalid_code") {
		t.Fatalf("err contents: %+v", tx)
	}
}
