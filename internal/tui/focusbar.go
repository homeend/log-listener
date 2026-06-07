package tui

import "github.com/charmbracelet/lipgloss"

// focusBarStyle renders the focused-block bar in an accent colour (cyan),
// distinct from the red exception bar (colour "9").
var focusBarStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))

// focusBarWidth is the display-column width of the "│ " prefix, MEASURED (the
// box-drawing │ U+2502 is East-Asian ambiguous, like the exception ▌), so a
// barred row's width accounting stays exact.
var focusBarWidth = dispWidth("│ ")

// focusBar returns the styled "│ " prefix and true when the row at idx belongs
// to the FOCUSED block — the multi-line block (End > Start) containing
// cursorIndex(), i.e. exactly the block `y` would copy. Suppressed in visual
// mode (the selection margin owns the gutter then) and when not focused on a
// multi-line block.
func (m *model) focusBar(idx int) (string, bool) {
	if m.visualMode {
		return "", false
	}
	cur := m.cursorIndex()
	if cur < 0 {
		return "", false
	}
	m.ensureBlocks()
	for _, b := range m.blocks {
		if cur < b.Start {
			break // blocks are ordered; no later block contains cur
		}
		if cur <= b.End {
			if b.End > b.Start && idx >= b.Start && idx <= b.End {
				return focusBarStyle.Render("│") + " ", true
			}
			return "", false
		}
	}
	return "", false
}
