package metrics

import (
	"bufio"
	"errors"
	"os"
	"regexp"
)

// opencodeParser is partial: opencode prints token counts but not cost.
// We surface the tokens and leave costUSD nil so the rollup's
// "TasksMissingCost" counter still ticks for opencode rows.
type opencodeParser struct{}

var opencodeTokensRe = regexp.MustCompile(`(?i)tokens?\s*[:=]?\s*in\s*=\s*([0-9][0-9,]*)\s*[, ]\s*out\s*=\s*([0-9][0-9,]*)`)

func (opencodeParser) Parse(sessionLogPath string) (*int64, *int64, *float64, error) {
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
		if m := opencodeTokensRe.FindStringSubmatch(line); m != nil {
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
