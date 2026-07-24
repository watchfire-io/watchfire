// Package buildinfo holds version information injected at build time via ldflags.
package buildinfo

var (
	// Version is the application version, injected at build time.
	Version    = "dev"
	Codename   = "unknown"
	CommitHash = "unknown"
	BuildDate  = "unknown"
	PostHogKey = ""
)
