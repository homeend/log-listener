package render

import (
	"encoding/json"
	"strings"
)

// DisplayLine is one physical row produced from an Event's rendered parts.
// Text is plain (no styling) and tab-expanded. IsCont marks continuation
// rows (everything after the head — embedded newlines, JSON/XML blocks).
type DisplayLine struct {
	Text   string
	IsCont bool
}

// DecomposeLines splits an Event's rendered parts into physical rows: the
// first text line is the head; subsequent text lines and every JSON/XML block
// row are continuations. This is the single source of truth shared by the TUI
// (which adds styling) and internal/linebuf (which stores plain text), so the
// rows — and therefore the IDs — can never diverge between what the user sees
// and what an agent resolves.
func DecomposeLines(ev Event) []DisplayLine {
	var textBuf strings.Builder
	var blocks []string
	for _, p := range ev.Rendered {
		switch p.Type {
		case "text":
			textBuf.WriteString(p.Value.(string))
		case "json":
			if b, err := json.MarshalIndent(p.Value, "", "  "); err == nil {
				blocks = append(blocks, string(b))
			}
		case "xml":
			if s, ok := p.Value.(string); ok {
				blocks = append(blocks, s)
			}
		}
	}
	text := strings.TrimRight(textBuf.String(), "\n")
	textLines := strings.Split(text, "\n")

	out := []DisplayLine{{Text: expandTabs(textLines[0]), IsCont: false}}
	for _, ln := range textLines[1:] {
		out = append(out, DisplayLine{Text: expandTabs(ln), IsCont: true})
	}
	for _, b := range blocks {
		for _, ln := range strings.Split(b, "\n") {
			out = append(out, DisplayLine{Text: expandTabs(ln), IsCont: true})
		}
	}
	return out
}

// expandTabs replaces tabs with spaces to 8-column tab stops so a body's rune
// count equals its terminal display width. Without this a tab (1 rune, up to 8
// columns) makes the width math underestimate, and the row overflows and wraps
// in the terminal — pushing the header off-screen and corrupting the layout
// (Java stack-trace frames start with a tab). Fast-returns when there's no tab.
// Moved here from internal/tui so the shared decomposer and the TUI expand identically.
func expandTabs(s string) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	const tabStop = 8
	var b strings.Builder
	col := 0
	for _, r := range s {
		if r == '\t' {
			n := tabStop - col%tabStop
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteRune(r)
		col++
	}
	return b.String()
}
