// Package vtansi renders a vt10x terminal emulator's cell grid as
// ANSI-coloured text. Shared by the daemon (broadcasting screen
// snapshots) and the TUI (driving its own emulator from the raw PTY
// stream so it can layer scrollback on top).
package vtansi

import (
	"fmt"
	"strings"

	"github.com/hinshun/vt10x"
)

// vt10x attribute bits (the package keeps them unexported).
const (
	attrReverse   = 1
	attrUnderline = 2
	attrBold      = 4
	attrItalic    = 16
)

// RenderScreen emits the visible grid as `rows` lines separated by
// `\r\n`, fully coloured via SGR. Trailing default-colour spaces are
// stripped per row to avoid bleeding past the viewport.
func RenderScreen(view vt10x.View, rows, cols int) string {
	var sb strings.Builder
	sb.Grow(cols * rows * 3)
	for row := 0; row < rows; row++ {
		if row > 0 {
			sb.WriteString("\r\n")
		}
		appendRow(&sb, view, row, cols)
	}
	return sb.String()
}

// RenderRow emits a single row of the grid as ANSI. No trailing newline.
func RenderRow(view vt10x.View, row, cols int) string {
	var sb strings.Builder
	sb.Grow(cols * 3)
	appendRow(&sb, view, row, cols)
	return sb.String()
}

func appendRow(sb *strings.Builder, view vt10x.View, row, cols int) {
	lastCol := cols - 1
	for lastCol >= 0 {
		g := view.Cell(lastCol, row)
		ch := g.Char
		if ch == 0 {
			ch = ' '
		}
		if ch != ' ' || g.FG != vt10x.DefaultFG || g.BG != vt10x.DefaultBG || g.Mode != 0 {
			break
		}
		lastCol--
	}

	var lastFG vt10x.Color = vt10x.DefaultFG
	var lastBG vt10x.Color = vt10x.DefaultBG
	var lastBold, lastItalic, lastUnderline, lastReverse bool
	inSGR := false

	for col := 0; col <= lastCol; col++ {
		g := view.Cell(col, row)

		ch := g.Char
		if ch == 0 {
			ch = ' '
		}

		fg := g.FG
		bg := g.BG
		bold := g.Mode&attrBold != 0
		italic := g.Mode&attrItalic != 0
		underline := g.Mode&attrUnderline != 0
		reverse := g.Mode&attrReverse != 0

		if fg != lastFG || bg != lastBG || bold != lastBold || italic != lastItalic || underline != lastUnderline || reverse != lastReverse {
			sb.WriteString("\033[0")
			if bold {
				sb.WriteString(";1")
			}
			if italic {
				sb.WriteString(";3")
			}
			if underline {
				sb.WriteString(";4")
			}
			if reverse {
				sb.WriteString(";7")
			}
			if fg != vt10x.DefaultFG {
				writeSGRColor(sb, fg, true)
			}
			if bg != vt10x.DefaultBG {
				writeSGRColor(sb, bg, false)
			}
			sb.WriteByte('m')
			inSGR = true

			lastFG = fg
			lastBG = bg
			lastBold = bold
			lastItalic = italic
			lastUnderline = underline
			lastReverse = reverse
		}

		sb.WriteRune(ch)
	}

	if inSGR {
		sb.WriteString("\033[0m")
	}
}

func writeSGRColor(sb *strings.Builder, c vt10x.Color, isFG bool) {
	idx := uint32(c)

	// Sentinel values (DefaultFG, DefaultBG, DefaultCursor) have bit 24 set — skip
	if idx >= 1<<24 {
		return
	}

	switch {
	case idx < 8:
		if isFG {
			fmt.Fprintf(sb, ";%d", 30+idx)
		} else {
			fmt.Fprintf(sb, ";%d", 40+idx)
		}
	case idx < 16:
		if isFG {
			fmt.Fprintf(sb, ";%d", 90+idx-8)
		} else {
			fmt.Fprintf(sb, ";%d", 100+idx-8)
		}
	case idx < 256:
		if isFG {
			fmt.Fprintf(sb, ";38;5;%d", idx)
		} else {
			fmt.Fprintf(sb, ";48;5;%d", idx)
		}
	default:
		r := (idx >> 16) & 0xFF
		g := (idx >> 8) & 0xFF
		b := idx & 0xFF
		if isFG {
			fmt.Fprintf(sb, ";38;2;%d;%d;%d", r, g, b)
		} else {
			fmt.Fprintf(sb, ";48;2;%d;%d;%d", r, g, b)
		}
	}
}

// AltScreenActive reports whether the view is currently on its alt
// screen (set by DECSET 1047 / 1049 / 47). Callers that maintain
// scrollback should suppress capture while this is true.
func AltScreenActive(view vt10x.View) bool {
	return view.Mode()&vt10x.ModeAltScreen != 0
}
