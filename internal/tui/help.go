package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// handleHelpKey processes keys while the help overlay is open. It is fully
// modal: esc/? close, j/k/arrows scroll, backspace trims the filter, and any
// other printable rune extends the filter. Everything else is ignored.
func (m *model) handleHelpKey(msg tea.KeyMsg) *model {
	switch msg.String() {
	case "esc", "?":
		m.showHelp = false
		m.helpQuery = ""
		return m
	case "up", "k":
		m.helpScroll--
		if m.helpScroll < 0 {
			m.helpScroll = 0
		}
		return m
	case "down", "j":
		m.helpScroll++
		return m
	case "backspace":
		if r := []rune(m.helpQuery); len(r) > 0 {
			m.helpQuery = string(r[:len(r)-1])
			m.helpScroll = 0
		}
		return m
	}
	if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 {
		m.helpQuery += string(msg.Runes)
		m.helpScroll = 0
	}
	return m
}
