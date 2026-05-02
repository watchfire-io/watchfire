package metrics

import (
	"bufio"
	"errors"
	"os"
	"regexp"
	"strconv"
)

// claudeCodeParser scans Claude Code's session-end summary line. The
// format used by recent CLI builds is:
//
//	Total tokens: in=12345 out=6789, cost=$0.0421
//
// Older/alternate phrasings are tolerated via a permissive regex; if no
// match is found the parser returns all-nil so the capture pipeline
// records duration-only metrics.
type claudeCodeParser struct{}

var (
	claudeTotalRe = regexp.MustCompile(`(?i)total\s+tokens?\s*[:=]?\s*in\s*=\s*([0-9][0-9,]*)\s*[, ]\s*out\s*=\s*([0-9][0-9,]*)`)
	claudeCostRe  = regexp.MustCompile(`(?i)cost\s*[:=]?\s*\$?\s*([0-9]+(?:\.[0-9]+)?)`)
)

func (claudeCodeParser) Parse(sessionLogPath string) (*int64, *int64, *float64, error) {
	f, err := os.Open(sessionLogPath) //nolint:gosec // path comes from daemon-controlled session log dir
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil, nil
		}
		return nil, nil, nil, err
	}
	defer func() { _ = f.Close() }()

	var (
		tokensIn  *int64
		tokensOut *int64
		costUSD   *float64
	)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if m := claudeTotalRe.FindStringSubmatch(line); m != nil {
			if v, ok := parseInt64Comma(m[1]); ok {
				tokensIn = &v
			}
			if v, ok := parseInt64Comma(m[2]); ok {
				tokensOut = &v
			}
		}
		if m := claudeCostRe.FindStringSubmatch(line); m != nil {
			if v, err := strconv.ParseFloat(m[1], 64); err == nil {
				costUSD = &v
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, nil, err
	}
	return tokensIn, tokensOut, costUSD, nil
}

// parseInt64Comma strips comma group separators ("12,345") and parses an
// int64. Returns (0, false) on parse failure.
func parseInt64Comma(s string) (int64, bool) {
	cleaned := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			continue
		}
		cleaned = append(cleaned, s[i])
	}
	v, err := strconv.ParseInt(string(cleaned), 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
