package tui

import "strings"

// entryRowSpan returns the inclusive absolute m.lines index span [start,end] of
// the entry that owns row idx (ok=false if idx is out of range). Mirrors the
// accumulation walk in entryIDForLine.
func (m *model) entryRowSpan(idx int) (start, end int, ok bool) {
	if idx < 0 {
		return 0, 0, false
	}
	off := 0
	for _, e := range m.visibleEntries() {
		n := len(m.displayCache[e.ID])
		if idx < off+n {
			return off, off + n - 1, true
		}
		off += n
	}
	return 0, 0, false
}

// rangeSlice returns [lo, lo+1, ..., hi] (inclusive), or nil if hi < lo.
func rangeSlice(lo, hi int) []int {
	if hi < lo {
		return nil
	}
	out := make([]int, 0, hi-lo+1)
	for i := lo; i <= hi; i++ {
		out = append(out, i)
	}
	return out
}

// selectedRows returns the absolute m.lines indices that Y copies, mirroring
// buildReference's precedence EXACTLY:
//  1. search active + hit → the whole owning entry's rows
//  2. explicitly focused block → focusedBlockRange()
//  3. else → the visible viewport (collectVisible)
func (m *model) selectedRows() []int {
	if m.matcher != nil && m.searchHit >= 0 {
		if s, e, ok := m.entryRowSpan(m.searchHit); ok {
			return rangeSlice(s, e)
		}
	}
	if s, e, ok := m.focusedBlockRange(); ok {
		return rangeSlice(s, e)
	}
	return m.collectVisible(m.contentHeight())
}

// textForRows renders the given absolute m.lines rows to plain (no-ANSI)
// displayed text, one per line, skipping out-of-range indices. Reuses
// plainExportLine so output is byte-identical to the save-to-file format.
func (m *model) textForRows(idxs []int) string {
	lines := make([]string, 0, len(idxs))
	for _, i := range idxs {
		if i < 0 || i >= len(m.lines) {
			continue
		}
		lines = append(lines, plainExportLine(m.lines[i]))
	}
	return strings.Join(lines, "\n")
}

// buildSelectionText is the pure seam: the displayed text of the current
// (normal-mode) selection, or "" if nothing resolves.
func buildSelectionText(m *model) string {
	return m.textForRows(m.selectedRows())
}

// copySelectionText OSC-52-copies the normal-mode selection text and returns
// (text, lineCount). Returns ("",0) when there's nothing to copy.
func (m *model) copySelectionText() (string, int) {
	txt := buildSelectionText(m)
	if txt == "" {
		return "", 0
	}
	osc52Copy(txt)
	return txt, strings.Count(txt, "\n") + 1
}
