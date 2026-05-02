package metrics

// copilotParser is a stub for v6.0 Ember. GitHub's Copilot CLI doesn't
// emit a stable summary line yet; capturing duration-only metrics
// avoids misreporting until the upstream CLI stabilises. A follow-up
// release will swap this for a real implementation once the format
// settles.
type copilotParser struct{}

func (copilotParser) Parse(string) (*int64, *int64, *float64, error) {
	return nil, nil, nil, nil
}
