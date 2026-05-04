package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestServer_SlackCallback(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"ok": true,
			"access_token": "xoxb-fake",
			"bot_user_id": "U99",
			"team": {"id": "T1", "name": "Team1"}
		}`))
	}))
	defer tokenSrv.Close()
	authTestSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"user":"watchfire"}`))
	}))
	defer authTestSrv.Close()

	prevToken, prevAuth := SlackTokenURL, SlackAuthTestURL
	SlackTokenURL, SlackAuthTestURL = tokenSrv.URL, authTestSrv.URL
	t.Cleanup(func() { SlackTokenURL, SlackAuthTestURL = prevToken, prevAuth })

	store := NewStateStore()
	srv, err := NewServer(store, http.DefaultClient, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go srv.Run(ctx)

	state, _ := store.Begin("slack", "CLIENT", "SECRET", srv.RedirectURI("slack"), "#general")

	cbURL := srv.RedirectURI("slack") + "?" + url.Values{
		"code":  []string{"AUTH"},
		"state": []string{state},
	}.Encode()

	go func() {
		// give server a moment to start
		time.Sleep(50 * time.Millisecond)
		_, _ = http.Get(cbURL)
	}()

	res, err := srv.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res.Err != nil {
		t.Fatalf("callback err: %v", res.Err)
	}
	if res.SlackInstall == nil || res.SlackInstall.BotToken != "xoxb-fake" {
		t.Fatalf("unexpected install: %+v", res)
	}
	if res.DefaultChannel != "#general" {
		t.Errorf("channel: %q", res.DefaultChannel)
	}
}

func TestServer_BadState(t *testing.T) {
	store := NewStateStore()
	srv, err := NewServer(store, http.DefaultClient, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go srv.Run(ctx)

	cbURL := srv.RedirectURI("slack") + "?" + url.Values{
		"code":  []string{"AUTH"},
		"state": []string{"BOGUS"},
	}.Encode()

	go func() {
		time.Sleep(50 * time.Millisecond)
		_, _ = http.Get(cbURL)
	}()

	res, err := srv.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res.Err == nil {
		t.Fatal("expected an error from unknown state callback")
	}
}
