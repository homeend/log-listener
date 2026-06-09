package tui

// scrollBy moves the browse viewport by delta display rows: negative scrolls up
// (toward older lines), positive scrolls down (toward newer). It owns the
// up/down asymmetry that was previously duplicated across the six scroll
// actions:
//   - Up (delta < 0): leave tail mode (unstickFromTail) and clamp at the top
//     (streamTop never goes below 0).
//   - Down (delta > 0): only meaningful while browsing — tail mode ignores it,
//     since the viewport is already pinned to the bottom; after moving, let
//     maybeReStick re-enter tail mode if the view caught up to the latest line.
//   - delta == 0 is a no-op.
func (m *model) scrollBy(delta int) {
	switch {
	case delta < 0:
		m.unstickFromTail()
		m.setStreamTopRow(m.stepEnabled(m.streamTopRow(), delta))
		if m.streamTopRow() < 0 {
			m.setStreamTopRow(0)
		}
	case delta > 0:
		if m.tailMode {
			return
		}
		m.setStreamTopRow(m.stepEnabled(m.streamTopRow(), delta))
		m.maybeReStick()
	}
}

// stepEnabled returns the m.lines index reached by moving |n| ENABLED display
// lines from idx — up when n<0, down when n>0 — skipping disabled lines
// (group-toggled-off or collapsed continuation rows), clamped to the valid
// range. Browse scrolling must count what the renderer actually shows:
// collectVisible skips disabled lines, so a raw index step stalls the viewport
// on a disabled run (the counter moves but the screen is frozen until the run
// is crossed). Mirrors unstickFromTail's enabled-aware walk.
func (m *model) stepEnabled(idx, n int) int {
	switch {
	case n < 0:
		for steps := -n; idx > 0 && steps > 0; {
			idx--
			if m.lineEnabled(m.lines[idx]) {
				steps--
			}
		}
	case n > 0:
		last := len(m.lines) - 1
		for steps := n; idx < last && steps > 0; {
			idx++
			if m.lineEnabled(m.lines[idx]) {
				steps--
			}
		}
	}
	return idx
}

// scrollFiles moves the file-overlay cursor by delta entries, clamped to the
// file list range [0, len(m.files)-1]. Centralizes the showFiles branches of
// the six scroll actions. Clamp order (high then low) matches the old
// PageDown/FastDown code, so the empty-list edge (len-1 == -1) resolves to 0,
// and the ±1 result equals the old guard-and-skip moves.
func (m *model) scrollFiles(delta int) {
	m.filesScroll += delta
	if m.filesScroll > len(m.files)-1 {
		m.filesScroll = len(m.files) - 1
	}
	if m.filesScroll < 0 {
		m.filesScroll = 0
	}
}

// panBy pans the horizontal view by delta columns, clamped at the left edge
// (horizScroll ≥ 0). There is no right-edge clamp: the renderer clips overlong
// lines, so over-panning right shows blank — matching the old inline
// ScrollRight/FastRight behavior. Centralizes the four pan handlers.
func (m *model) panBy(delta int) {
	if m.wordWrap {
		return
	}
	m.horizScroll += delta
	if m.horizScroll < 0 {
		m.horizScroll = 0
	}
}

const (
	horizStep     = 10            // columns moved per Left/Right keypress
	horizFastStep = 50            // columns moved per Ctrl+Left/Right
	vertFastStep  = 10            // lines moved per Ctrl+Up/Down
	hitMargin     = horizStep / 2 // left-margin columns when panning to a hit
)

// vstep converts a vertical jump of termRows terminal rows into a logical-line
// scroll delta, honoring word wrap. The visible window (collectVisible) already
// accounts for tail vs browse, so the number of lines it returns for termRows
// rows is the matching line delta. Wrap off: termRows unchanged (1 row = 1 line).
func (m *model) vstep(termRows int) int {
	if !m.wordWrap {
		return termRows
	}
	n := len(m.collectVisible(termRows))
	if n < 1 {
		n = 1
	}
	return n
}

// unstickFromTail flips out of tail mode while keeping the visible window
// where it currently is — so the very next render shows exactly the same
// lines as before, but new appends no longer scroll the view. The anchor
// is the absolute index of the first visible event (computed by walking
// backward through ENABLED events for one contentHeight worth of terminal rows).
func (m *model) unstickFromTail() {
	if !m.tailMode {
		return
	}
	m.tailMode = false
	rows := m.contentHeight()
	used := 0
	idx := len(m.lines) - 1
	for ; idx >= 0 && used < rows; idx-- {
		if m.lineEnabled(m.lines[idx]) {
			used += m.visibleRowCost(idx)
		}
	}
	m.setStreamTopRow(idx + 1)
	if m.streamTopRow() < 0 {
		m.setStreamTopRow(0)
	}
}

// maybeReStick re-pins to the tail if the browse window has caught up
// with the latest enabled event. Call after any downward scroll.
func (m *model) maybeReStick() {
	// Sum terminal-row cost of enabled events from streamTop onward; if that
	// fits in one content-height window, we're effectively at the tail.
	rows := m.contentHeight()
	used := 0
	for i := m.streamTopRow(); i < len(m.lines); i++ {
		if m.lineEnabled(m.lines[i]) {
			used += m.visibleRowCost(i)
		}
	}
	if used <= rows {
		m.tailMode = true
	}
}

// contentHeight returns the number of rows available for the body between
// the header (1 row) and the footer (1 row).
func (m *model) contentHeight() int {
	h := m.height - 2
	if h < 1 {
		h = 1
	}
	return h
}
