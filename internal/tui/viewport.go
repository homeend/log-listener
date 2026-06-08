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
		m.streamTop += delta
		if m.streamTop < 0 {
			m.streamTop = 0
		}
	case delta > 0:
		if m.tailMode {
			return
		}
		m.streamTop += delta
		m.maybeReStick()
	}
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
	m.horizScroll += delta
	if m.horizScroll < 0 {
		m.horizScroll = 0
	}
}
