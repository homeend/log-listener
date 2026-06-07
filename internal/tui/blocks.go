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

// navAnchor is the index block navigation measures from: the current top when
// browsing, or len(m.lines) when pinned to the tail (so "previous" walks back
// from the end and "next" finds nothing).
func (m *model) navAnchor() int {
	if m.tailMode {
		return len(m.lines)
	}
	return m.streamTop
}

// blockHeadEnabled reports whether a block's head row is currently visible
// (group enabled, not collapsed away). Navigation skips hidden heads.
func (m *model) blockHeadEnabled(b blocks.Block) bool {
	if b.Start < 0 || b.Start >= len(m.lines) {
		return false
	}
	return m.lineEnabled(m.lines[b.Start])
}

// jumpToBlockHead moves the viewport so the block head at line idx is the top
// row, leaving tail mode. Mirrors search-hit navigation's "anchor at top".
func (m *model) jumpToBlockHead(idx int) {
	m.unstickFromTail()
	m.tailMode = false
	m.streamTop = idx
	if m.streamTop < 0 {
		m.streamTop = 0
	}
}

// isNavTarget reports whether b is a destination for block navigation. Marked
// nav (}/{) targets processor-matched blocks. Plain nav (]/[) targets only
// MULTI-LINE blocks (End > Start) — single-line log entries are each their own
// degenerate block, but stepping through every line is not "jump to the next
// block"; the user wants to hop between the multi-row structures (stack traces,
// indented config dumps, JSON/XML), exceptions or not.
func (m *model) isNavTarget(b blocks.Block, markedOnly bool) bool {
	if markedOnly {
		return b.Processed()
	}
	return b.End > b.Start
}

// gotoNextBlock moves to the next block head after the anchor. markedOnly limits
// the search to processor-matched (Processed) blocks; otherwise to multi-line
// blocks. No-op if none.
func (m *model) gotoNextBlock(markedOnly bool) {
	m.ensureBlocks()
	anchor := m.navAnchor()
	for _, b := range m.blocks {
		if b.Start <= anchor {
			continue
		}
		if !m.isNavTarget(b, markedOnly) {
			continue
		}
		if !m.blockHeadEnabled(b) {
			continue
		}
		m.jumpToBlockHead(b.Start)
		return
	}
}

// gotoPrevBlock moves to the last block head before the anchor.
func (m *model) gotoPrevBlock(markedOnly bool) {
	m.ensureBlocks()
	anchor := m.navAnchor()
	for i := len(m.blocks) - 1; i >= 0; i-- {
		b := m.blocks[i]
		if b.Start >= anchor {
			continue
		}
		if !m.isNavTarget(b, markedOnly) {
			continue
		}
		if !m.blockHeadEnabled(b) {
			continue
		}
		m.jumpToBlockHead(b.Start)
		return
	}
}
