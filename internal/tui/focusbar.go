package tui

import "github.com/charmbracelet/lipgloss"

// focusBarStyle renders the focused-block bar in an accent colour (cyan),
// distinct from the red exception bar (colour "9").
var focusBarStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))

// focusBarWidth is the display-column width of the "│ " prefix, MEASURED (the
// box-drawing │ U+2502 is East-Asian ambiguous, like the exception ▌), so a
// barred row's width accounting stays exact.
var focusBarWidth = dispWidth("│ ")

// focusedBlockRange returns the [start,end] line span of the explicitly focused
// block (set by block navigation), or ok=false when there is no active block
// focus, the model is tailing, or the anchored row is not in a multi-line block.
func (m *model) focusedBlockRange() (start, end int, ok bool) {
	if !m.blockFocused || m.tailMode {
		return 0, 0, false
	}
	ref := m.streamTopRow()
	if ref < 0 || ref >= len(m.lines) {
		return 0, 0, false
	}
	m.ensureBlocks()
	for _, b := range m.blocks {
		if ref < b.Start {
			break // blocks are ordered; no later block contains ref
		}
		if ref <= b.End {
			if b.End > b.Start {
				return b.Start, b.End, true
			}
			return 0, 0, false
		}
	}
	return 0, 0, false
}

// focusBar returns the styled "│ " prefix and true when the row at idx belongs
// to the EXPLICITLY focused block (set via block navigation). Suppressed in
// visual mode. Returns the measured focusBarWidth so renderStream's width math
// stays exact.
func (m *model) focusBar(idx int) (string, bool) {
	if m.visualMode {
		return "", false
	}
	s, e, ok := m.focusedBlockRange()
	if !ok {
		return "", false
	}
	if idx >= s && idx <= e {
		return focusBarStyle.Render("│") + " ", true
	}
	return "", false
}
