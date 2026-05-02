package metrics

import (
	"bufio"
	"errors"
	"os"
	"regexp"
)

// geminiParser handles the Gemini CLI's metadata footer:
//
//	[metadata] tokens prompt=1234 output=5678
//
// Gemini does not currently expose cost — costUSD stays nil.
type geminiParser struct{}

var geminiTokensRe = regexp.MustCompile(`(?i)tokens?\s*(?:prompt|input)\s*=\s*([0-9][0-9,]*)\s*(?:[, ]\s*)?output\s*=\s*([0-9][0-9,]*)`)

func (geminiParser) Parse(sessionLogPath string) (*int64, *int64, *float64, error) {
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
	)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if m := geminiTokensRe.FindStringSubmatch(line); m != nil {
			if v, ok := parseInt64Comma(m[1]); ok {
				tokensIn = &v
			}
			if v, ok := parseInt64Comma(m[2]); ok {
				tokensOut = &v
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, nil, err
	}
	return tokensIn, tokensOut, nil, nil
}
