// Package relay holds the outbound delivery framework for v7.0 Relay.
//
// In task 0065 only the GitHub PR-body template lives here; the dispatcher,
// HMAC signer, and the four channel adapters (webhook / Slack / Discord /
// generic outbound bus) ship in the sibling tasks 0062-0064.
//
// The package is kept intentionally thin so embedding it from `internal/daemon/git`
// (which renders the PR body via `text/template`) does not pull a dispatcher
// dependency into the merge path.
package relay

import _ "embed"

// PRBodyTemplate is the Go text/template source rendered into a GitHub
// pull-request body when the v7.0 auto-PR flow opens a PR. Consumers must
// register the `rfc3339` function (formats a `time.Time` to RFC3339) before
// parsing.
//
//go:embed templates/pr_body.md.tmpl
var PRBodyTemplate string
