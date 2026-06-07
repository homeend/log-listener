package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// visualCaretStyle/visualSelStyle: bright caret for the cursor row, accent bar
// for the rest of the selection.
var (
	visualCaretStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("11")) // bright yellow
	visualSelStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("14")) // cyan
	// Both prefixes MUST render to the same display width so clipLine accounts
	// for them uniformly. Measured; ▶ and ┃ are East-Asian ambiguous.
	visualBarWidth = dispWidth("▶ ")
)

// visualBar returns the gutter prefix and true for rows in visual mode: a caret
// on the cursor row, a selection bar on rows within the (anchored) selection.
func (m *model) visualBar(idx int) (string, bool) {
	if !m.visualMode {
		return "", false
	}
	if idx == m.visualCursor {
		return visualCaretStyle.Render("▶") + " ", true
	}
	if m.visualAnchor >= 0 {
		lo, hi := m.visualAnchor, m.visualCursor
		if lo > hi {
			lo, hi = hi, lo
		}
		if idx >= lo && idx <= hi {
			return visualSelStyle.Render("┃") + " ", true
		}
	}
	return "", false
}

// enterVisual starts visual selection mode with the cursor on the top visible
// row and no anchor yet. Leaves tail mode so the cursor is stable.
func (m *model) enterVisual() {
	if len(m.lines) == 0 {
		return
	}
	m.unstickFromTail()
	m.tailMode = false
	m.visualMode = true
	m.visualAnchor = -1
	if vis := m.collectVisible(m.contentHeight()); len(vis) > 0 {
		m.visualCursor = vis[0]
	} else {
		m.visualCursor = m.streamTop
	}
	if m.visualCursor < 0 {
		m.visualCursor = 0
	}
}

// exitVisual leaves visual mode and clears the anchor.
func (m *model) exitVisual() {
	m.visualMode = false
	m.visualAnchor = -1
}

// ensureVisualVisible scrolls streamTop so visualCursor stays on screen.
func (m *model) ensureVisualVisible() {
	h := m.contentHeight()
	if h <= 0 {
		return
	}
	if m.visualCursor < m.streamTop {
		m.streamTop = m.visualCursor
	} else if m.visualCursor >= m.streamTop+h {
		m.streamTop = m.visualCursor - h + 1
	}
	if m.streamTop < 0 {
		m.streamTop = 0
	}
}

// buildVisualRef is the pure seam: the range over the inclusive line span
// [min(anchor,cursor), max], as range:<entryID(min)>..<entryID(max)>, or "" if
// either endpoint can't be resolved.
func buildVisualRef(m *model) string {
	lo, hi := m.visualAnchor, m.visualCursor
	if lo > hi {
		lo, hi = hi, lo
	}
	a, b := m.entryIDForLine(lo), m.entryIDForLine(hi)
	if a == "" || b == "" {
		return ""
	}
	return fmt.Sprintf("range:%s..%s", a, b)
}

// copyVisualSelection copies the current selection's reference (OSC 52) and
// flashes it.
func (m *model) copyVisualSelection() {
	ref := buildVisualRef(m)
	if ref == "" {
		return
	}
	osc52Copy(ref)
	m.flash = "copied " + ref
}

// handleVisualKey processes keys while in visual mode. Only up/down (arrows +
// j/k), space, and esc act; any other key is ignored (stays in visual mode).
func (m *model) handleVisualKey(msg tea.KeyMsg) *model {
	switch msg.String() {
	case "up", "k":
		if m.visualCursor > 0 {
			m.visualCursor--
		}
		m.ensureVisualVisible()
	case "down", "j":
		if m.visualCursor < len(m.lines)-1 {
			m.visualCursor++
		}
		m.ensureVisualVisible()
	case " ":
		if m.visualAnchor < 0 {
			m.visualAnchor = m.visualCursor
		} else {
			m.copyVisualSelection()
			m.exitVisual()
		}
	case "esc":
		m.exitVisual()
	}
	return m
}
