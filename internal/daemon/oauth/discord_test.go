package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildDiscordAuthURL(t *testing.T) {
	url := BuildDiscordAuthURL("CLIENT", "http://127.0.0.1:1234/oauth/discord/callback", "STATE_TOKEN", DiscordPermissions)
	if !strings.HasPrefix(url, "https://discord.com/oauth2/authorize?") {
		t.Fatalf("unexpected prefix: %s", url)
	}
	for _, want := range []string{
		"client_id=CLIENT",
		"state=STATE_TOKEN",
		"scope=bot",
		"permissions=" + DiscordPermissions,
	} {
		if !strings.Contains(url, want) {
			t.Errorf("authorize URL missing %q: %s", want, url)
		}
	}
}

func TestExchangeDiscordCode_Success(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if got := r.PostForm.Get("code"); got != "AUTH_CODE" {
			t.Errorf("code: got %q", got)
		}
		if got := r.PostForm.Get("grant_type"); got != "authorization_code" {
			t.Errorf("grant_type: got %q", got)
		}
		_, _ = w.Write([]byte(`{
			"access_token": "user_access_token",
			"guild": {"id": "G_42", "name": "Acme Guild"}
		}`))
	}))
	defer tokenSrv.Close()

	infoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer user_access_token" {
			t.Errorf("/applications/@me missing bearer: %s", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{
			"name": "Watchfire",
			"bot": {"username": "Watchfire", "discriminator": "0042"}
		}`))
	}))
	defer infoSrv.Close()

	prevToken, prevInfo := DiscordTokenURL, DiscordBotInfoURL
	DiscordTokenURL, DiscordBotInfoURL = tokenSrv.URL, infoSrv.URL
	t.Cleanup(func() { DiscordTokenURL, DiscordBotInfoURL = prevToken, prevInfo })

	install, err := ExchangeDiscordCode(context.Background(), nil, "CLIENT", "SECRET", "AUTH_CODE", "http://127.0.0.1/cb")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if install.GuildID != "G_42" || install.GuildName != "Acme Guild" {
		t.Errorf("guild: %+v", install)
	}
	if install.Username != "Watchfire" || install.Discriminator != "0042" {
		t.Errorf("bot user: %+v", install)
	}
}

func TestExchangeDiscordCode_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_request"}`))
	}))
	defer srv.Close()
	prev := DiscordTokenURL
	DiscordTokenURL = srv.URL
	t.Cleanup(func() { DiscordTokenURL = prev })

	_, err := ExchangeDiscordCode(context.Background(), nil, "C", "S", "BAD", "http://127.0.0.1/cb")
	if err == nil {
		t.Fatal("expected error on 400")
	}
	if !strings.Contains(err.Error(), "discord") {
		t.Errorf("err: %v", err)
	}
}
