package insights

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

// canonicalDate is the YYYY-MM-DD stamp used in every export filename. The
// stamp is the *report* date — not the window-end — so two exports of the
// same window produced on different days don't collide.
func canonicalDate(at time.Time) string {
	return at.Local().Format("2006-01-02")
}

// SingleTaskFilename returns `watchfire-task-<n>-<YYYY-MM-DD>.<ext>`.
func SingleTaskFilename(taskNumber int, format Format, at time.Time) string {
	return fmt.Sprintf("watchfire-task-%d-%s.%s", taskNumber, canonicalDate(at), formatExt(format))
}

// ProjectFilename returns `watchfire-project-<slug>-<YYYY-MM-DD>.<ext>`. The
// slug is derived from the project name with non-alphanumerics collapsed
// into hyphens — a paste-friendly identifier for files dropped into a Slack
// channel or status doc.
func ProjectFilename(projectName string, format Format, at time.Time) string {
	return fmt.Sprintf("watchfire-project-%s-%s.%s", slugify(projectName), canonicalDate(at), formatExt(format))
}

// GlobalFilename returns `watchfire-global-<YYYY-MM-DD>.<ext>`.
func GlobalFilename(format Format, at time.Time) string {
	return fmt.Sprintf("watchfire-global-%s.%s", canonicalDate(at), formatExt(format))
}

func formatExt(f Format) string {
	if f == FormatCSV {
		return "csv"
	}
	return "md"
}

// MimeType maps a Format to the IANA media type the GUI uses when triggering
// a Blob URL download. text/markdown is registered (RFC 7763) so most OSes
// will pick a sensible viewer.
func MimeType(f Format) string {
	if f == FormatCSV {
		return "text/csv"
	}
	return "text/markdown"
}

// slugify lower-cases, replaces runs of non-alphanumeric runes with single
// hyphens, and trims leading/trailing hyphens. Empty input → "project".
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "project"
	}
	var b strings.Builder
	prevHyphen := true
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevHyphen = false
			continue
		}
		if !prevHyphen {
			b.WriteByte('-')
			prevHyphen = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "project"
	}
	return out
}
