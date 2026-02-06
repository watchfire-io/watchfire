package agent

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// AgentIssueType identifies the type of issue detected.
type AgentIssueType string

const (
	AgentIssueNone      AgentIssueType = ""
	AgentIssueAuth      AgentIssueType = "auth_required"
	AgentIssueRateLimit AgentIssueType = "rate_limited"
)

// AgentIssue represents a detected issue with the agent.
type AgentIssue struct {
	Type          AgentIssueType
	DetectedAt    time.Time
	Message       string     // Original error message
	ResetAt       *time.Time // Parsed reset time (rate limits)
	CooldownUntil *time.Time // When to auto-resume
}

// Pattern detection for auth errors
var authPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)API Error:\s*401.*authentication_error`),
	regexp.MustCompile(`(?i)OAuth token has expired`),
	regexp.MustCompile(`(?i)Please run /login`),
	regexp.MustCompile(`(?i)authentication_error.*OAuth token`),
	regexp.MustCompile(`(?i)invalid.*token`),
	regexp.MustCompile(`(?i)token.*expired`),
}

// Pattern detection for rate limits
var rateLimitPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)You've hit your limit`),
	regexp.MustCompile(`(?i)rate limit`),
	regexp.MustCompile(`(?i)too many requests`),
	regexp.MustCompile(`(?i)API Error:\s*429`),
}

// rateLimitResetPattern extracts reset time from rate limit messages
var rateLimitResetPattern = regexp.MustCompile(`(?i)resets?\s+(\d+(?::\d+)?(?:\s*(?:am|pm))?)\s*(?:\(([^)]+)\))?`)

// DetectAuthError checks if a line contains an authentication error.
func DetectAuthError(line string) bool {
	for _, pattern := range authPatterns {
		if pattern.MatchString(line) {
			return true
		}
	}
	return false
}

// DetectRateLimit checks if a line contains a rate limit error.
// Returns whether a rate limit was detected and the parsed reset time if available.
func DetectRateLimit(line string) (detected bool, resetTime *time.Time) {
	for _, pattern := range rateLimitPatterns {
		if pattern.MatchString(line) {
			detected = true
			// Try to extract reset time
			if matches := rateLimitResetPattern.FindStringSubmatch(line); len(matches) >= 2 {
				resetTime = ParseResetTime(matches[1], matches[2])
			}
			return
		}
	}
	return false, nil
}

// ParseResetTime parses a reset time string like "4am" or "4:00 PM" with optional timezone.
// Returns nil if parsing fails.
func ParseResetTime(timeStr string, tzStr string) *time.Time {
	if timeStr == "" {
		return nil
	}

	timeStr = strings.TrimSpace(timeStr)
	tzStr = strings.TrimSpace(tzStr)

	// Determine location
	loc := time.Local
	if tzStr != "" {
		// Try to parse as timezone location
		if parsedLoc, err := time.LoadLocation(tzStr); err == nil {
			loc = parsedLoc
		} else {
			// Try common timezone abbreviations/city names
			tzMappings := map[string]string{
				"Europe/Lisbon": "Europe/Lisbon",
				"Lisbon":        "Europe/Lisbon",
				"PT":            "America/Los_Angeles",
				"PST":           "America/Los_Angeles",
				"PDT":           "America/Los_Angeles",
				"EST":           "America/New_York",
				"EDT":           "America/New_York",
				"UTC":           "UTC",
				"GMT":           "UTC",
			}
			if mappedTz, ok := tzMappings[tzStr]; ok {
				if parsedLoc, err := time.LoadLocation(mappedTz); err == nil {
					loc = parsedLoc
				}
			}
		}
	}

	now := time.Now().In(loc)

	// Try various time formats
	formats := []string{
		"3pm",
		"3:04pm",
		"3PM",
		"3:04PM",
		"3 pm",
		"3:04 pm",
		"15:04",
		"15",
	}

	timeStr = strings.ToLower(timeStr)

	for _, format := range formats {
		if t, err := time.ParseInLocation(format, timeStr, loc); err == nil {
			// Construct full datetime using today's date
			result := time.Date(
				now.Year(), now.Month(), now.Day(),
				t.Hour(), t.Minute(), 0, 0, loc,
			)
			// If the time is before now, it's probably tomorrow
			if result.Before(now) {
				result = result.Add(24 * time.Hour)
			}
			return &result
		}
	}

	// Try parsing just hours (e.g., "4")
	if hours, err := strconv.Atoi(timeStr); err == nil && hours >= 0 && hours <= 23 {
		result := time.Date(
			now.Year(), now.Month(), now.Day(),
			hours, 0, 0, 0, loc,
		)
		if result.Before(now) {
			result = result.Add(24 * time.Hour)
		}
		return &result
	}

	return nil
}

// DetectIssue checks a line for any known issues and returns an AgentIssue if found.
func DetectIssue(line string) *AgentIssue {
	// Check auth errors first (more critical)
	if DetectAuthError(line) {
		return &AgentIssue{
			Type:       AgentIssueAuth,
			DetectedAt: time.Now(),
			Message:    line,
		}
	}

	// Check rate limits
	if detected, resetTime := DetectRateLimit(line); detected {
		issue := &AgentIssue{
			Type:       AgentIssueRateLimit,
			DetectedAt: time.Now(),
			Message:    line,
			ResetAt:    resetTime,
		}
		// Set cooldown until reset time if available
		if resetTime != nil {
			issue.CooldownUntil = resetTime
		}
		return issue
	}

	return nil
}
