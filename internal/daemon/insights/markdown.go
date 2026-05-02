package insights

import (
	"bytes"
	"embed"
	"fmt"
	"strings"
	"sync"
	"text/template"
	"time"
)

//go:embed templates/*.md.tmpl
var templatesFS embed.FS

// templateCache lazily loads + parses the embedded template files. Each
// template registers the same FuncMap so per-template helpers like
// `durationHuman` resolve identically.
var (
	templateOnce  sync.Once
	templateStore *template.Template
	templateErr   error
)

func loadTemplates() (*template.Template, error) {
	templateOnce.Do(func() {
		root := template.New("insights").Funcs(templateFuncs())
		templateStore, templateErr = root.ParseFS(templatesFS, "templates/*.md.tmpl")
	})
	return templateStore, templateErr
}

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"plural":        plural,
		"durationHuman": durationHuman,
		"timePtr":       timePtrLabel,
		"successCell":   successCell,
		"defaultStr":    defaultStr,
		"windowLabel":   windowLabel,
	}
}

func renderSingleTaskMarkdown(d SingleTaskData) ([]byte, error) {
	return executeTemplate("single_task.md.tmpl", d)
}

func renderProjectMarkdown(d ProjectData) ([]byte, error) {
	return executeTemplate("project.md.tmpl", d)
}

func renderGlobalMarkdown(d GlobalData) ([]byte, error) {
	return executeTemplate("global.md.tmpl", d)
}

func executeTemplate(name string, data interface{}) ([]byte, error) {
	tpl, err := loadTemplates()
	if err != nil {
		return nil, err
	}
	t := tpl.Lookup(name)
	if t == nil {
		return nil, fmt.Errorf("insights: template %s not found", name)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	// Templates trim trailing newlines aggressively; add a single trailing
	// newline so files end cleanly per POSIX convention.
	if len(out) == 0 || out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return out, nil
}

// --- helpers used by templates ---------------------------------------------

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// durationHuman renders a duration in seconds as a friendly "1h 12m" /
// "45s" / "—" string. Used in both per-task tables and KPI strips, so it
// has to handle the zero case (no completion yet) without printing "0s".
func durationHuman(secs int64) string {
	if secs <= 0 {
		return "—"
	}
	d := time.Duration(secs) * time.Second
	h := int(d / time.Hour)
	m := int((d % time.Hour) / time.Minute)
	s := int((d % time.Minute) / time.Second)
	switch {
	case h > 0:
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh %dm", h, m)
	case m > 0:
		if s == 0 {
			return fmt.Sprintf("%dm", m)
		}
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

func timePtrLabel(t *time.Time) string {
	if t == nil {
		return "—"
	}
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

func successCell(b *bool) string {
	switch {
	case b == nil:
		return "—"
	case *b:
		return "✅ true"
	default:
		return "❌ false"
	}
}

func defaultStr(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func windowLabel(start, end time.Time) string {
	if start.IsZero() && end.IsZero() {
		return "—"
	}
	if start.IsZero() {
		return fmt.Sprintf("up to %s", end.Local().Format("Mon, Jan 2 2006"))
	}
	if end.IsZero() {
		return fmt.Sprintf("from %s", start.Local().Format("Mon, Jan 2 2006"))
	}
	return fmt.Sprintf("%s → %s",
		start.Local().Format("Mon, Jan 2"),
		end.Local().Format("Mon, Jan 2 2006"),
	)
}
