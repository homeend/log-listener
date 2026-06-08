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
