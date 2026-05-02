package metrics

import (
	"path/filepath"
	"testing"
)

func fixturePath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("testdata", name)
}

func intValue(p *int64) int64 {
	if p == nil {
		return -1
	}
	return *p
}

func floatValue(p *float64) float64 {
	if p == nil {
		return -1
	}
	return *p
}

func TestClaudeCodeParserHappyPath(t *testing.T) {
	p := claudeCodeParser{}
	in, out, cost, err := p.Parse(fixturePath(t, "claude_code_happy.log"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if intValue(in) != 12345 {
		t.Errorf("tokensIn=%d want 12345", intValue(in))
	}
	if intValue(out) != 6789 {
		t.Errorf("tokensOut=%d want 6789", intValue(out))
	}
	if floatValue(cost) != 0.0421 {
		t.Errorf("costUSD=%v want 0.0421", floatValue(cost))
	}
}

func TestClaudeCodeParserNoSummary(t *testing.T) {
	p := claudeCodeParser{}
	in, out, cost, err := p.Parse(fixturePath(t, "claude_code_no_summary.log"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if in != nil || out != nil || cost != nil {
		t.Errorf("expected all-nil; got in=%v out=%v cost=%v", in, out, cost)
	}
}

func TestClaudeCodeParserMalformed(t *testing.T) {
	p := claudeCodeParser{}
	in, out, cost, err := p.Parse(fixturePath(t, "claude_code_malformed.log"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// Malformed numbers don't match the strict regex — should fall through.
	if in != nil || out != nil {
		t.Errorf("expected nil tokens for malformed; got in=%v out=%v", in, out)
	}
	if cost != nil {
		t.Errorf("expected nil cost for malformed; got cost=%v", cost)
	}
}

func TestClaudeCodeParserMissingFile(t *testing.T) {
	p := claudeCodeParser{}
	in, out, cost, err := p.Parse(fixturePath(t, "does_not_exist.log"))
	if err != nil {
		t.Fatalf("expected nil error for missing file, got %v", err)
	}
	if in != nil || out != nil || cost != nil {
		t.Error("expected all-nil for missing file")
	}
}

func TestCodexParserHappyPath(t *testing.T) {
	p := codexParser{}
	in, out, cost, err := p.Parse(fixturePath(t, "codex_happy.log"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if intValue(in) != 4321 {
		t.Errorf("tokensIn=%d want 4321", intValue(in))
	}
	if intValue(out) != 8765 {
		t.Errorf("tokensOut=%d want 8765", intValue(out))
	}
	if floatValue(cost) != 0.0210 {
		t.Errorf("costUSD=%v want 0.0210", floatValue(cost))
	}
}

func TestCodexParserNoSummary(t *testing.T) {
	p := codexParser{}
	in, out, cost, err := p.Parse(fixturePath(t, "codex_no_summary.log"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if in != nil || out != nil || cost != nil {
		t.Errorf("expected all-nil; got in=%v out=%v cost=%v", in, out, cost)
	}
}

func TestOpencodeParserHappyPath(t *testing.T) {
	p := opencodeParser{}
	in, out, cost, err := p.Parse(fixturePath(t, "opencode_happy.log"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if intValue(in) != 999 {
		t.Errorf("tokensIn=%d want 999", intValue(in))
	}
	if intValue(out) != 111 {
		t.Errorf("tokensOut=%d want 111", intValue(out))
	}
	// opencode never reports cost — must always be nil.
	if cost != nil {
		t.Errorf("opencode parser must not report cost; got %v", cost)
	}
}

func TestOpencodeParserNoSummary(t *testing.T) {
	p := opencodeParser{}
	in, out, cost, err := p.Parse(fixturePath(t, "opencode_no_summary.log"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if in != nil || out != nil || cost != nil {
		t.Errorf("expected all-nil; got in=%v out=%v cost=%v", in, out, cost)
	}
}

func TestGeminiParserHappyPath(t *testing.T) {
	p := geminiParser{}
	in, out, cost, err := p.Parse(fixturePath(t, "gemini_happy.log"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if intValue(in) != 2222 {
		t.Errorf("tokensIn=%d want 2222", intValue(in))
	}
	if intValue(out) != 3333 {
		t.Errorf("tokensOut=%d want 3333", intValue(out))
	}
	// Gemini does not expose cost.
	if cost != nil {
		t.Errorf("gemini parser must not report cost; got %v", cost)
	}
}

func TestGeminiParserNoSummary(t *testing.T) {
	p := geminiParser{}
	in, out, cost, err := p.Parse(fixturePath(t, "gemini_no_summary.log"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if in != nil || out != nil || cost != nil {
		t.Errorf("expected all-nil; got in=%v out=%v cost=%v", in, out, cost)
	}
}

func TestCopilotParserStub(t *testing.T) {
	p := copilotParser{}
	in, out, cost, err := p.Parse(fixturePath(t, "claude_code_happy.log"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if in != nil || out != nil || cost != nil {
		t.Error("copilot stub must always report all-nil for v6.0")
	}
}

func TestNullParser(t *testing.T) {
	p := NullParser()
	in, out, cost, err := p.Parse("/some/path/that/never/exists")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if in != nil || out != nil || cost != nil {
		t.Error("null parser must always report all-nil")
	}
}

func TestGetParserDispatch(t *testing.T) {
	cases := []struct {
		name    string
		agent   string
		wantTyp string
	}{
		{"claude-code lower", "claude-code", "claudeCodeParser"},
		{"claude alias", "claude", "claudeCodeParser"},
		{"claude case-insensitive", "Claude-Code", "claudeCodeParser"},
		{"codex", "codex", "codexParser"},
		{"openai-codex alias", "openai-codex", "codexParser"},
		{"opencode", "opencode", "opencodeParser"},
		{"gemini", "gemini", "geminiParser"},
		{"gemini-cli alias", "gemini-cli", "geminiParser"},
		{"copilot", "copilot", "copilotParser"},
		{"copilot-cli alias", "copilot-cli", "copilotParser"},
		{"unknown falls back to null", "totally-made-up", "nullParser"},
		{"empty string falls back to null", "", "nullParser"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := GetParser(c.agent)
			gotName := typeName(p)
			if gotName != c.wantTyp {
				t.Errorf("GetParser(%q) = %s, want %s", c.agent, gotName, c.wantTyp)
			}
		})
	}
}

func typeName(p Parser) string {
	switch p.(type) {
	case claudeCodeParser:
		return "claudeCodeParser"
	case codexParser:
		return "codexParser"
	case opencodeParser:
		return "opencodeParser"
	case geminiParser:
		return "geminiParser"
	case copilotParser:
		return "copilotParser"
	case nullParser:
		return "nullParser"
	default:
		return "unknown"
	}
}

func TestParseInt64Comma(t *testing.T) {
	cases := []struct {
		in      string
		want    int64
		wantOk  bool
	}{
		{"0", 0, true},
		{"123", 123, true},
		{"12,345", 12345, true},
		{"1,234,567", 1234567, true},
		{"abc", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		got, ok := parseInt64Comma(c.in)
		if got != c.want || ok != c.wantOk {
			t.Errorf("parseInt64Comma(%q) = (%d, %v); want (%d, %v)", c.in, got, ok, c.want, c.wantOk)
		}
	}
}
