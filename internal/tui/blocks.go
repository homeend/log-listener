package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/homeend/log-listener/internal/blocks"
)

// blockLines adapts m.lines into the neutral blocks.Line slice: ANSI stripped,
// with the render-block flag carried through.
func (m *model) blockLines() []blocks.Line {
	out := make([]blocks.Line, len(m.lines))
	for i, dl := range m.lines {
		out[i] = blocks.Line{Text: stripANSI(dl.body), IsRenderBlock: dl.isBlock}
	}
	return out
}

// ensureBlocks recomputes the block cache when dirty. Single recompute path —
// every m.lines mutator sets blocksDirty, so the cache is current wherever it
// is read (renderStream, navigation).
func (m *model) ensureBlocks() {
	if !m.blocksDirty {
		return
	}
	m.blocks = blocks.Segment(m.blockLines())
	m.blocksDirty = false
}

// inExceptionBlock reports whether the line at absolute index idx belongs to a
// block the exception processor flagged. Callers must have called ensureBlocks.
func (m *model) inExceptionBlock(idx int) bool {
	for _, b := range m.blocks {
		if idx < b.Start {
			return false // blocks are ordered; no later block can contain idx
		}
		if idx <= b.End {
			return b.Exception != nil
		}
	}
	return false
}

// exceptionBarStyle renders the left-bar glyph in an alert color.
var exceptionBarStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9")) // red

// exceptionBarWidth is the display-column width of the bar prefix "▌ ",
// MEASURED with the same dispWidth the renderer uses (▌ U+258C is East-Asian
// ambiguous — its width varies by locale, so a hardcoded 2 could be wrong and
// re-introduce the row-overflow/wrap bug). Computed once at init.
var exceptionBarWidth = dispWidth("▌ ")

// exceptionBar returns the styled bar prefix and true when the line at idx
// should be barred (marks on AND the line is in an exception block). The
// returned width (exceptionBarWidth) MUST be added to the row's visW so
// clipLine pads/clips against the true width.
func (m *model) exceptionBar(idx int) (string, bool) {
	if !m.showExceptionMarks {
		return "", false
	}
	if !m.inExceptionBlock(idx) {
		return "", false
	}
	return exceptionBarStyle.Render("▌") + " ", true
}
