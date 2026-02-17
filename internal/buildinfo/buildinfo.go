// Package buildinfo holds version information injected at build time via ldflags.
package buildinfo

var (
	Version    = "dev"
	Codename   = "unknown"
	CommitHash = "unknown"
	BuildDate  = "unknown"
)
