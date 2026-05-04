package oauth

import "errors"

// ErrUnknownState is returned when a callback presents a state value
// that was never issued (or has been consumed / expired).
var ErrUnknownState = errors.New("oauth: unknown state")

// ErrProviderMismatch is returned when a callback's provider does not
// match the provider the state was issued for. Defends against a
// hijacked redirect URI cross-firing between Slack and Discord.
var ErrProviderMismatch = errors.New("oauth: provider mismatch")

// ErrMissingCode is returned when a callback URL contains no `code`
// parameter. Usually means the upstream provider redirected with an
// `error=...` query param instead — surfaced separately as
// `ErrUserDenied` when the upstream signals the user clicked "deny".
var ErrMissingCode = errors.New("oauth: missing code in callback")

// ErrUserDenied is returned when the upstream provider redirects with
// `error=access_denied` (Slack) / `error=access_denied` (Discord) —
// the user clicked "Cancel" or the workspace admin denied consent.
var ErrUserDenied = errors.New("oauth: user denied authorization")

// ErrTokenExchange wraps the upstream "exchange code for token" call's
// error response. The underlying message carries the provider's
// error string verbatim so the UI can surface it.
type ErrTokenExchange struct {
	Provider string
	Message  string
}

func (e *ErrTokenExchange) Error() string {
	return "oauth: " + e.Provider + " token exchange failed: " + e.Message
}
