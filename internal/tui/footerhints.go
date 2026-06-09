package tui

import "fmt"

// compactStatus is the abbreviated right-hand tail of the bottom bar: event
// count, scroll position (tail or @top/total), and the committed search term.
func (m *model) compactStatus() string {
	pos := "tail"
	if !m.tailMode {
		pos = fmt.Sprintf("@%d/%d", m.streamTopRow(), len(m.lines))
	}
	s := fmt.Sprintf("ev %d · %s", len(m.lines), pos)
	if m.matcher != nil {
		s += " · /" + m.searchQuery
	}
	return s
}
