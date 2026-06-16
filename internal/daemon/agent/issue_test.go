package agent

import (
	"testing"
	"time"
)

func TestDetectAuthError(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		expected bool
	}{
		{
			name:     "API 401 error",
			line:     "API Error: 401 {\"type\":\"error\",\"error\":{\"type\":\"authentication_error\",\"message\":\"OAuth token has expired\"}}",
			expected: true,
		},
		{
			name:     "OAuth expired",
			line:     "Your OAuth token has expired. Please run /login to re-authenticate.",
			expected: true,
		},
		{
			name:     "Please run login",
			line:     "Authentication required. Please run /login",
			expected: true,
		},
		{
			name:     "Invalid API key",
			line:     "Error: invalid API key",
			expected: true,
		},
		{
			name:     "OAuth token expired (bare)",
			line:     "Your OAuth token has expired",
			expected: true,
		},
		{
			name:     "Normal output",
			line:     "Building project...",
			expected: false,
		},
		{
			name:     "Empty line",
			line:     "",
			expected: false,
		},
		// False-positive guards: ordinary output that mentions tokens must
		// NOT be flagged as an auth error (regression test for the bare
		// `invalid.*token` / `token.*expired` patterns that silently halted
		// the wildfire chain on any project whose code touches tokens).
		{
			name:     "Code returning an invalid-token error string",
			line:     "if token == \"\" { return errors.New(\"invalid token provided\") }",
			expected: false,
		},
		{
			name:     "Comment about a possibly-expired token",
			line:     "// the cached token may have expired; refresh before use",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DetectAuthError(tt.line)
			if result != tt.expected {
				t.Errorf("DetectAuthError(%q) = %v, want %v", tt.line, result, tt.expected)
			}
		})
	}
}

func TestDetectRateLimit(t *testing.T) {
	tests := []struct {
		name           string
		line           string
		expectDetected bool
		expectHasReset bool
	}{
		{
			name:           "Rate limit with reset time",
			line:           "You've hit your limit · resets 4am (Europe/Lisbon)",
			expectDetected: true,
			expectHasReset: true,
		},
		{
			name:           "Rate limit simple",
			line:           "You've hit your limit, please wait",
			expectDetected: true,
			expectHasReset: false,
		},
		{
			name:           "Usage limit reached",
			line:           "Claude usage limit reached. Try again later.",
			expectDetected: true,
			expectHasReset: false,
		},
		{
			name:           "API 429 error",
			line:           "API Error: 429 Rate limit exceeded",
			expectDetected: true,
			expectHasReset: false,
		},
		{
			name:           "Normal output",
			line:           "Compiling main.go...",
			expectDetected: false,
			expectHasReset: false,
		},
		// False-positive guards: ordinary output mentioning rate limiting
		// must NOT halt the chain (regression test for the bare `rate limit`
		// / `too many requests` patterns — a project whose own code or task
		// titles mention rate limiting would otherwise silently stop wildfire).
		{
			name:           "Code implementing rate limiting",
			line:           "Implemented rate limiting with a token-bucket backoff",
			expectDetected: false,
			expectHasReset: false,
		},
		{
			name:           "Task title mentioning rate limiting",
			line:           "Centralized polite-fetch layer: rate limiting, backoff",
			expectDetected: false,
			expectHasReset: false,
		},
		{
			name:           "Comment referencing a 429 status",
			line:           "// upstream returns 429 when the quota is exhausted",
			expectDetected: false,
			expectHasReset: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detected, resetTime := DetectRateLimit(tt.line)
			if detected != tt.expectDetected {
				t.Errorf("DetectRateLimit(%q) detected = %v, want %v", tt.line, detected, tt.expectDetected)
			}
			hasReset := resetTime != nil
			if hasReset != tt.expectHasReset {
				t.Errorf("DetectRateLimit(%q) hasReset = %v, want %v", tt.line, hasReset, tt.expectHasReset)
			}
		})
	}
}

func TestParseResetTime(t *testing.T) {
	// Get current time for comparison
	now := time.Now()

	tests := []struct {
		name      string
		timeStr   string
		tzStr     string
		expectNil bool
	}{
		{
			name:      "4am with timezone",
			timeStr:   "4am",
			tzStr:     "Europe/Lisbon",
			expectNil: false,
		},
		{
			name:      "4pm no timezone",
			timeStr:   "4pm",
			tzStr:     "",
			expectNil: false,
		},
		{
			name:      "12:30pm",
			timeStr:   "12:30pm",
			tzStr:     "",
			expectNil: false,
		},
		{
			name:      "24-hour format",
			timeStr:   "14:00",
			tzStr:     "",
			expectNil: false,
		},
		{
			name:      "Invalid time",
			timeStr:   "invalid",
			tzStr:     "",
			expectNil: true,
		},
		{
			name:      "Empty string",
			timeStr:   "",
			tzStr:     "",
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseResetTime(tt.timeStr, tt.tzStr)
			if tt.expectNil && result != nil {
				t.Errorf("ParseResetTime(%q, %q) = %v, want nil", tt.timeStr, tt.tzStr, result)
			}
			if !tt.expectNil && result == nil {
				t.Errorf("ParseResetTime(%q, %q) = nil, want non-nil", tt.timeStr, tt.tzStr)
			}
			// If we got a time, it should be in the future
			if result != nil && result.Before(now) {
				t.Errorf("ParseResetTime(%q, %q) = %v, expected time in the future", tt.timeStr, tt.tzStr, result)
			}
		})
	}
}

func TestDetectIssue(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		expectType AgentIssueType
	}{
		{
			name:       "Auth error",
			line:       "API Error: 401 authentication_error",
			expectType: AgentIssueAuth,
		},
		{
			name:       "Rate limit",
			line:       "You've hit your limit · resets 4am",
			expectType: AgentIssueRateLimit,
		},
		{
			name:       "Normal output",
			line:       "Everything is fine",
			expectType: AgentIssueNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issue := DetectIssue(tt.line)
			if tt.expectType == AgentIssueNone {
				if issue != nil {
					t.Errorf("DetectIssue(%q) = %v, want nil", tt.line, issue)
				}
			} else {
				if issue == nil {
					t.Errorf("DetectIssue(%q) = nil, want issue of type %s", tt.line, tt.expectType)
				} else if issue.Type != tt.expectType {
					t.Errorf("DetectIssue(%q).Type = %s, want %s", tt.line, issue.Type, tt.expectType)
				}
			}
		})
	}
}
