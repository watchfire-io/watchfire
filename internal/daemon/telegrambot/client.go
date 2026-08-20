// Package telegrambot is the v10.0 Torch thin client for Telegram's
// Bot API. Mirrors `internal/daemon/slackbot` / `internal/daemon/
// discordbot` — stdlib net/http only, stateless, the bot token is
// passed per call so one Client serves any number of bots.
//
// Telegram's Bot API differs from Slack/Discord in two ways this
// package has to care about:
//
//  1. The token rides in the URL path (`/bot<token>/<method>`), not an
//     Authorization header — error paths must never echo the URL.
//  2. `getUpdates` long-polls: the server holds the request open up to
//     `timeout` seconds (max 50). Long-poll calls therefore use a
//     dedicated HTTP client whose timeout exceeds the poll window,
//     while every other method keeps the usual 10-second budget.
//
// All errors are returned as *Error, which distinguishes network
// failures, Telegram API rejections (`ok:false` + description, with
// `retry_after` surfaced for 429s), and auth failures (401/403 —
// i.e. a bad or revoked token).
package telegrambot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// APIBase is the Bot API origin. Overridable for tests (httptest).
var APIBase = "https://api.telegram.org"

// DefaultPollTimeout is the getUpdates long-poll window used when the
// caller passes 0. Telegram allows up to 50s; 45s leaves margin under
// the poll HTTP client's own timeout.
const DefaultPollTimeout = 45 * time.Second

// ErrorKind classifies a *Error.
type ErrorKind int

const (
	// ErrKindNetwork — the HTTP round-trip itself failed (DNS, refused
	// connection, context cancellation, body read).
	ErrKindNetwork ErrorKind = iota
	// ErrKindAPI — Telegram answered `ok:false` (or a non-2xx status
	// other than 401/403). Description + RetryAfter carry the detail.
	ErrKindAPI
	// ErrKindAuth — HTTP 401/403: the bot token is invalid or revoked.
	ErrKindAuth
)

// Error is the consistent error type every Client method returns.
type Error struct {
	Kind        ErrorKind
	Method      string        // Bot API method name ("getMe", "sendMessage", ...)
	StatusCode  int           // HTTP status (0 on network errors)
	Description string        // Telegram's `description` field when available
	RetryAfter  time.Duration // Non-zero on 429 (`parameters.retry_after`)
	Err         error         // Underlying error for ErrKindNetwork
}

func (e *Error) Error() string {
	switch e.Kind {
	case ErrKindNetwork:
		return fmt.Sprintf("telegrambot: %s: %v", e.Method, e.Err)
	case ErrKindAuth:
		return fmt.Sprintf("telegrambot: %s: unauthorized (HTTP %d): %s", e.Method, e.StatusCode, e.Description)
	default:
		if e.RetryAfter > 0 {
			return fmt.Sprintf("telegrambot: %s: API error (HTTP %d, retry after %s): %s", e.Method, e.StatusCode, e.RetryAfter, e.Description)
		}
		return fmt.Sprintf("telegrambot: %s: API error (HTTP %d): %s", e.Method, e.StatusCode, e.Description)
	}
}

func (e *Error) Unwrap() error { return e.Err }

// IsAuth reports whether err is a telegrambot auth failure (bad token).
func IsAuth(err error) bool {
	var te *Error
	return errors.As(err, &te) && te.Kind == ErrKindAuth
}

// IsNetwork reports whether err is a telegrambot network failure.
func IsNetwork(err error) bool {
	var te *Error
	return errors.As(err, &te) && te.Kind == ErrKindNetwork
}

// RetryAfter returns the 429 backoff hint carried by err (0 if none).
func RetryAfter(err error) time.Duration {
	var te *Error
	if errors.As(err, &te) {
		return te.RetryAfter
	}
	return 0
}

// User is the Bot API User object (subset).
type User struct {
	ID       int64  `json:"id"`
	IsBot    bool   `json:"is_bot"`
	Username string `json:"username"`
}

// Chat is the Bot API Chat object (subset).
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"` // "private", "group", "supergroup", "channel"
}

// Message is the Bot API Message object (subset).
type Message struct {
	MessageID int64  `json:"message_id"`
	From      *User  `json:"from,omitempty"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text,omitempty"`
}

// CallbackQuery is the Bot API CallbackQuery object (subset) — fired
// when the user taps an inline-keyboard button.
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data,omitempty"`
}

// Update is one getUpdates entry (subset).
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message,omitempty"`
	EditedMessage *Message       `json:"edited_message,omitempty"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
}

// BotCommand is one setMyCommands entry.
type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// InlineKeyboardButton is one button of an inline keyboard
// (reply_markup). Only callback buttons are supported — every button
// tap comes back as a CallbackQuery carrying CallbackData (≤64 bytes,
// per the Bot API).
type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
}

// Client wraps two http.Clients: HTTP for ordinary calls and PollHTTP
// for getUpdates long-polls (its timeout must exceed the poll window).
// Stateless — the bot token is passed per call.
type Client struct {
	HTTP     *http.Client
	PollHTTP *http.Client
}

// New returns a Client with a 10-second per-request timeout for
// ordinary calls and a 60-second timeout for long-polls.
func New() *Client {
	return &Client{
		HTTP:     &http.Client{Timeout: 10 * time.Second},
		PollHTTP: &http.Client{Timeout: 60 * time.Second},
	}
}

// GetMe validates the token and returns the bot's identity. The
// username is what pairing deep links (`https://t.me/<username>?start=`)
// are built from.
func (c *Client) GetMe(ctx context.Context, token string) (*User, error) {
	var user User
	if err := c.call(ctx, token, "getMe", nil, &user, false); err != nil {
		return nil, err
	}
	return &user, nil
}

// GetUpdates long-polls for updates at offset. `timeout` is the server
// hold window (0 → DefaultPollTimeout; capped at Telegram's 50s max).
// Pass the highest seen update_id + 1 as offset to acknowledge.
func (c *Client) GetUpdates(ctx context.Context, token string, offset int64, timeout time.Duration) ([]Update, error) {
	if timeout <= 0 {
		timeout = DefaultPollTimeout
	}
	if timeout > 50*time.Second {
		timeout = 50 * time.Second
	}
	params := url.Values{}
	params.Set("timeout", strconv.Itoa(int(timeout/time.Second)))
	if offset != 0 {
		params.Set("offset", strconv.FormatInt(offset, 10))
	}
	var updates []Update
	if err := c.call(ctx, token, "getUpdates", params, &updates, true); err != nil {
		return nil, err
	}
	return updates, nil
}

// SendMessage sends an HTML-formatted message to chatID and returns the
// new message_id (needed for later EditMessageText growth in watch
// mode). Link previews are disabled — event messages carry deep links
// whose unfurls would drown the text.
func (c *Client) SendMessage(ctx context.Context, token string, chatID int64, text string) (int64, error) {
	return c.SendMessageWithKeyboard(ctx, token, chatID, text, nil)
}

// SendMessageWithKeyboard is SendMessage plus an inline keyboard
// (rows of callback buttons). A nil/empty keyboard sends a plain
// message.
func (c *Client) SendMessageWithKeyboard(ctx context.Context, token string, chatID int64, text string, keyboard [][]InlineKeyboardButton) (int64, error) {
	params := url.Values{}
	params.Set("chat_id", strconv.FormatInt(chatID, 10))
	params.Set("text", text)
	params.Set("parse_mode", "HTML")
	params.Set("disable_web_page_preview", "true")
	if len(keyboard) > 0 {
		encoded, err := json.Marshal(map[string]any{"inline_keyboard": keyboard})
		if err != nil {
			return 0, &Error{Kind: ErrKindNetwork, Method: "sendMessage", Err: err}
		}
		params.Set("reply_markup", string(encoded))
	}
	var msg Message
	if err := c.call(ctx, token, "sendMessage", params, &msg, false); err != nil {
		return 0, err
	}
	return msg.MessageID, nil
}

// SendChatAction sets the chat's transient status line (e.g. action
// "typing" renders "typing…"). The Bot API clears it after ~5 seconds
// or when the next message arrives, so callers re-send it periodically
// while work is in flight.
func (c *Client) SendChatAction(ctx context.Context, token string, chatID int64, action string) error {
	params := url.Values{}
	params.Set("chat_id", strconv.FormatInt(chatID, 10))
	params.Set("action", action)
	var ok bool
	return c.call(ctx, token, "sendChatAction", params, &ok, false)
}

// EditMessageText replaces the text of a previously sent message.
func (c *Client) EditMessageText(ctx context.Context, token string, chatID, messageID int64, text string) error {
	params := url.Values{}
	params.Set("chat_id", strconv.FormatInt(chatID, 10))
	params.Set("message_id", strconv.FormatInt(messageID, 10))
	params.Set("text", text)
	params.Set("parse_mode", "HTML")
	params.Set("disable_web_page_preview", "true")
	// Telegram returns the edited Message (or `true` for inline
	// messages); we don't need either.
	var raw json.RawMessage
	return c.call(ctx, token, "editMessageText", params, &raw, false)
}

// SetMyCommands registers the bot's command list for Telegram's
// client-side autocomplete menu.
func (c *Client) SetMyCommands(ctx context.Context, token string, commands []BotCommand) error {
	encoded, err := json.Marshal(commands)
	if err != nil {
		return &Error{Kind: ErrKindNetwork, Method: "setMyCommands", Err: err}
	}
	params := url.Values{}
	params.Set("commands", string(encoded))
	var ok bool
	return c.call(ctx, token, "setMyCommands", params, &ok, false)
}

// AnswerCallbackQuery acknowledges an inline-keyboard tap. `text` is
// optional (shown as a toast when non-empty).
func (c *Client) AnswerCallbackQuery(ctx context.Context, token, callbackQueryID, text string) error {
	params := url.Values{}
	params.Set("callback_query_id", callbackQueryID)
	if text != "" {
		params.Set("text", text)
	}
	var ok bool
	return c.call(ctx, token, "answerCallbackQuery", params, &ok, false)
}

// apiEnvelope is the Bot API's universal response wrapper.
type apiEnvelope struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// call POSTs a Bot API method as form-encoded params and decodes the
// envelope's result into out. longPoll selects the PollHTTP client.
func (c *Client) call(ctx context.Context, token, method string, params url.Values, out any, longPoll bool) error {
	if c == nil || c.HTTP == nil || c.PollHTTP == nil {
		c = New()
	}
	if token == "" {
		return &Error{Kind: ErrKindAuth, Method: method, Description: "empty token"}
	}

	endpoint := fmt.Sprintf("%s/bot%s/%s", APIBase, token, method)
	var body io.Reader
	if params != nil {
		body = strings.NewReader(params.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return &Error{Kind: ErrKindNetwork, Method: method, Err: err}
	}
	if params != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	httpClient := c.HTTP
	if longPoll {
		httpClient = c.PollHTTP
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		// Never echo err verbatim upward without wrapping: url.Error
		// includes the full URL — token and all. Strip to the bare
		// underlying error.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return &Error{Kind: ErrKindNetwork, Method: method, Err: err}
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return &Error{Kind: ErrKindNetwork, Method: method, Err: readErr}
	}

	var env apiEnvelope
	if uErr := json.Unmarshal(respBody, &env); uErr != nil {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return &Error{Kind: ErrKindAuth, Method: method, StatusCode: resp.StatusCode, Description: truncate(string(respBody), 200)}
		}
		return &Error{Kind: ErrKindAPI, Method: method, StatusCode: resp.StatusCode, Description: fmt.Sprintf("unparseable response: %s", truncate(string(respBody), 200))}
	}
	if !env.OK {
		e := &Error{Kind: ErrKindAPI, Method: method, StatusCode: resp.StatusCode, Description: env.Description}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			e.Kind = ErrKindAuth
		}
		if env.Parameters != nil && env.Parameters.RetryAfter > 0 {
			e.RetryAfter = time.Duration(env.Parameters.RetryAfter) * time.Second
		}
		return e
	}
	if out != nil && len(env.Result) > 0 {
		if uErr := json.Unmarshal(env.Result, out); uErr != nil {
			return &Error{Kind: ErrKindAPI, Method: method, StatusCode: resp.StatusCode, Description: fmt.Sprintf("unparseable result: %v", uErr)}
		}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
