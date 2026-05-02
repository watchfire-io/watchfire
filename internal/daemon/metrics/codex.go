package metrics

import (
	"bufio"
	"errors"
	"os"
	"regexp"
	"strconv"
)

// codexParser handles OpenAI Codex's session-end summary. Recent builds
// emit a stderr footer of the form:
//
//	tokens: input=1234 output=5678 (cost: $0.0210)
//
// Older Codex builds may print a slightly different shape — fall through
// to all-nil instead of misreporting.
type codexParser struct{}

var (
	codexTokensRe = regexp.MustCompile(`(?i)tokens?\s*[:=]?\s*input\s*=\s*([0-9][0-9,]*)\s+output\s*=\s*([0-9][0-9,]*)`)
	codexCostRe   = regexp.MustCompile(`(?i)cost\s*[:=]?\s*\$?\s*([0-9]+(?:\.[0-9]+)?)`)
)

func (codexParser) Parse(sessionLogPath string) (*int64, *int64, *float64, error) {
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
		if m := codexTokensRe.FindStringSubmatch(line); m != nil {
			if v, ok := parseInt64Comma(m[1]); ok {
				tokensIn = &v
			}
			if v, ok := parseInt64Comma(m[2]); ok {
				tokensOut = &v
			}
		}
		if m := codexCostRe.FindStringSubmatch(line); m != nil {
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
