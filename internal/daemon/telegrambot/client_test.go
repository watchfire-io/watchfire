package telegrambot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeBotAPI stands in for api.telegram.org. It validates the token in
// the URL path and dispatches per Bot API method, recording the last
// request's form values for assertions.
type fakeBotAPI struct {
	t          *testing.T
	validToken string
	lastForm   map[string]string
	handlers   map[string]func(w http.ResponseWriter, form map[string]string)
}

func newFakeBotAPI(t *testing.T) (*fakeBotAPI, *httptest.Server) {
	t.Helper()
	f := &fakeBotAPI{
		t:          t,
		validToken: "123456:VALID",
		handlers:   map[string]func(http.ResponseWriter, map[string]string){},
	}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)

	prevBase := APIBase
	APIBase = srv.URL
	t.Cleanup(func() { APIBase = prevBase })
	return f, srv
}

func (f *fakeBotAPI) serve(w http.ResponseWriter, r *http.Request) {
	// Path shape: /bot<token>/<method>
	rest, ok := strings.CutPrefix(r.URL.Path, "/bot")
	if !ok {
		http.NotFound(w, r)
		return
	}
	token, method, ok := strings.Cut(rest, "/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		f.t.Errorf("ParseForm: %v", err)
	}
	form := map[string]string{}
	for k := range r.PostForm {
		form[k] = r.PostForm.Get(k)
	}
	f.lastForm = form

	if token != f.validToken {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": false, "error_code": 401, "description": "Unauthorized",
		})
		return
	}
	h, found := f.handlers[method]
	if !found {
		f.t.Errorf("unexpected Bot API method %q", method)
		http.NotFound(w, r)
		return
	}
	h(w, form)
}

func ok(w http.ResponseWriter, result any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}

func TestGetMeHappyPath(t *testing.T) {
	f, _ := newFakeBotAPI(t)
	f.handlers["getMe"] = func(w http.ResponseWriter, _ map[string]string) {
		ok(w, User{ID: 42, IsBot: true, Username: "watchfire_bot"})
	}

	user, err := New().GetMe(context.Background(), f.validToken)
	if err != nil {
		t.Fatalf("GetMe: %v", err)
	}
	if user.Username != "watchfire_bot" || user.ID != 42 || !user.IsBot {
		t.Fatalf("GetMe mismatch: %+v", user)
	}
}

func TestGetMeBadToken(t *testing.T) {
	f, _ := newFakeBotAPI(t)
	_ = f // no handlers needed — auth is rejected before dispatch

	_, err := New().GetMe(context.Background(), "123456:WRONG")
	if err == nil {
		t.Fatal("expected error for bad token")
	}
	if !IsAuth(err) {
		t.Fatalf("expected auth error, got: %v", err)
	}
	if IsNetwork(err) {
		t.Fatalf("auth error misclassified as network: %v", err)
	}
	// The token must never leak into the error text.
	if strings.Contains(err.Error(), "WRONG") {
		t.Fatalf("token leaked into error: %v", err)
	}
}

func TestEmptyTokenIsAuthError(t *testing.T) {
	newFakeBotAPI(t)
	_, err := New().GetMe(context.Background(), "")
	if !IsAuth(err) {
		t.Fatalf("expected auth error for empty token, got: %v", err)
	}
}

func TestSendMessageHappyPath(t *testing.T) {
	f, _ := newFakeBotAPI(t)
	f.handlers["sendMessage"] = func(w http.ResponseWriter, form map[string]string) {
		if form["chat_id"] != "987" {
			t.Errorf("chat_id = %q, want 987", form["chat_id"])
		}
		if form["parse_mode"] != "HTML" {
			t.Errorf("parse_mode = %q, want HTML", form["parse_mode"])
		}
		if form["disable_web_page_preview"] != "true" {
			t.Errorf("disable_web_page_preview = %q, want true", form["disable_web_page_preview"])
		}
		if form["text"] != "<b>hello</b>" {
			t.Errorf("text = %q", form["text"])
		}
		ok(w, Message{MessageID: 555, Chat: Chat{ID: 987, Type: "private"}, Text: "<b>hello</b>"})
	}

	id, err := New().SendMessage(context.Background(), f.validToken, 987, "<b>hello</b>")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if id != 555 {
		t.Fatalf("message_id = %d, want 555", id)
	}
}

func TestSendMessage429RetryAfter(t *testing.T) {
	f, _ := newFakeBotAPI(t)
	f.handlers["sendMessage"] = func(w http.ResponseWriter, _ map[string]string) {
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": false, "error_code": 429,
			"description": "Too Many Requests: retry after 7",
			"parameters":  map[string]any{"retry_after": 7},
		})
	}

	_, err := New().SendMessage(context.Background(), f.validToken, 1, "x")
	if err == nil {
		t.Fatal("expected 429 error")
	}
	if IsAuth(err) || IsNetwork(err) {
		t.Fatalf("429 misclassified: %v", err)
	}
	if got := RetryAfter(err); got != 7*time.Second {
		t.Fatalf("RetryAfter = %v, want 7s", got)
	}
	var te *Error
	if !errors.As(err, &te) || te.Kind != ErrKindAPI || te.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("unexpected error shape: %#v", err)
	}
}

func TestGetUpdatesOffsetAndTimeout(t *testing.T) {
	f, _ := newFakeBotAPI(t)
	f.handlers["getUpdates"] = func(w http.ResponseWriter, form map[string]string) {
		if form["offset"] != "100" {
			t.Errorf("offset = %q, want 100", form["offset"])
		}
		if form["timeout"] != "45" {
			t.Errorf("timeout = %q, want 45 (default)", form["timeout"])
		}
		ok(w, []Update{
			{UpdateID: 100, Message: &Message{MessageID: 1, Chat: Chat{ID: 5, Type: "private"}, From: &User{ID: 5, Username: "nuno"}, Text: "/status"}},
			{UpdateID: 101, CallbackQuery: &CallbackQuery{ID: "cb1", From: User{ID: 5}, Data: "use:proj-1"}},
		})
	}

	updates, err := New().GetUpdates(context.Background(), f.validToken, 100, 0)
	if err != nil {
		t.Fatalf("GetUpdates: %v", err)
	}
	if len(updates) != 2 {
		t.Fatalf("got %d updates, want 2", len(updates))
	}
	if updates[0].Message == nil || updates[0].Message.Text != "/status" {
		t.Fatalf("update 0 mismatch: %+v", updates[0])
	}
	if updates[1].CallbackQuery == nil || updates[1].CallbackQuery.Data != "use:proj-1" {
		t.Fatalf("update 1 mismatch: %+v", updates[1])
	}
}

func TestGetUpdatesTimeoutCappedAt50s(t *testing.T) {
	f, _ := newFakeBotAPI(t)
	f.handlers["getUpdates"] = func(w http.ResponseWriter, form map[string]string) {
		if form["timeout"] != "50" {
			t.Errorf("timeout = %q, want capped 50", form["timeout"])
		}
		ok(w, []Update{})
	}
	if _, err := New().GetUpdates(context.Background(), f.validToken, 0, 2*time.Minute); err != nil {
		t.Fatalf("GetUpdates: %v", err)
	}
}

func TestEditMessageText(t *testing.T) {
	f, _ := newFakeBotAPI(t)
	f.handlers["editMessageText"] = func(w http.ResponseWriter, form map[string]string) {
		if form["chat_id"] != "9" || form["message_id"] != "12" {
			t.Errorf("chat_id/message_id = %q/%q", form["chat_id"], form["message_id"])
		}
		ok(w, Message{MessageID: 12, Chat: Chat{ID: 9}})
	}
	if err := New().EditMessageText(context.Background(), f.validToken, 9, 12, "updated"); err != nil {
		t.Fatalf("EditMessageText: %v", err)
	}
}

func TestSetMyCommands(t *testing.T) {
	f, _ := newFakeBotAPI(t)
	f.handlers["setMyCommands"] = func(w http.ResponseWriter, form map[string]string) {
		var cmds []BotCommand
		if err := json.Unmarshal([]byte(form["commands"]), &cmds); err != nil {
			t.Errorf("commands not valid JSON: %v", err)
		}
		if len(cmds) != 2 || cmds[0].Command != "status" {
			t.Errorf("commands mismatch: %+v", cmds)
		}
		ok(w, true)
	}
	cmds := []BotCommand{
		{Command: "status", Description: "Project status"},
		{Command: "tasks", Description: "Top active tasks"},
	}
	if err := New().SetMyCommands(context.Background(), f.validToken, cmds); err != nil {
		t.Fatalf("SetMyCommands: %v", err)
	}
}

func TestAnswerCallbackQuery(t *testing.T) {
	f, _ := newFakeBotAPI(t)
	f.handlers["answerCallbackQuery"] = func(w http.ResponseWriter, form map[string]string) {
		if form["callback_query_id"] != "cb-7" || form["text"] != "done" {
			t.Errorf("form mismatch: %v", form)
		}
		ok(w, true)
	}
	if err := New().AnswerCallbackQuery(context.Background(), f.validToken, "cb-7", "done"); err != nil {
		t.Fatalf("AnswerCallbackQuery: %v", err)
	}
}

func TestNetworkErrorClassifiedAndTokenScrubbed(t *testing.T) {
	prev := APIBase
	// Closed port — connection refused immediately.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close()
	APIBase = srv.URL
	t.Cleanup(func() { APIBase = prev })

	_, err := New().GetMe(context.Background(), "123456:SECRETTOKEN")
	if err == nil {
		t.Fatal("expected network error")
	}
	if !IsNetwork(err) {
		t.Fatalf("expected network classification, got: %v", err)
	}
	if strings.Contains(err.Error(), "SECRETTOKEN") {
		t.Fatalf("token leaked into network error: %v", err)
	}
}
