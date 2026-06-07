package tui

import "github.com/homeend/log-listener/internal/blocks"

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
