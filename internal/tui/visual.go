package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/homeend/log-listener/internal/keymap"
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
	if idx == m.visualCursorRow() {
		return visualCaretStyle.Render("▶") + " ", true
	}
	if m.visualAnchorRow() >= 0 {
		lo, hi := m.selectionBounds()
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
	m.showFiles = false
	m.showGroupsPanel = false
	m.showRenderersPanel = false
	m.visualMode = true
	m.setVisualAnchorRow(-1)
	if vis := m.collectVisible(m.contentHeight()); len(vis) > 0 {
		m.setVisualCursorRow(vis[0])
	} else {
		m.setVisualCursorRow(m.streamTopRow())
	}
	if m.visualCursorRow() < 0 {
		m.setVisualCursorRow(0)
	}
}

// exitVisual leaves visual mode and clears the anchor.
func (m *model) exitVisual() {
	m.visualMode = false
	m.setVisualAnchorRow(-1)
}

// selectionBounds returns the inclusive [lo, hi] row span of the current visual
// selection. With an anchor set it is the ordered (anchor, cursor) pair; with
// no anchor (visualAnchor < 0) it is the caret row alone (lo == hi ==
// visualCursor). Centralizes the order-the-pair idiom previously copied in
// visualBar, buildVisualText, and buildVisualRef.
func (m *model) selectionBounds() (lo, hi int) {
	lo, hi = m.visualCursorRow(), m.visualCursorRow()
	if m.visualAnchorRow() >= 0 {
		lo, hi = m.visualAnchorRow(), m.visualCursorRow()
		if lo > hi {
			lo, hi = hi, lo
		}
	}
	return lo, hi
}

// ensureVisualVisible scrolls streamTop so visualCursor stays on screen.
func (m *model) ensureVisualVisible() {
	h := m.contentHeight()
	if h <= 0 {
		return
	}
	if m.visualCursorRow() < m.streamTopRow() {
		m.setStreamTopRow(m.visualCursorRow())
	} else if m.visualCursorRow() >= m.streamTopRow()+h {
		m.setStreamTopRow(m.visualCursorRow() - h + 1)
	}
	if m.streamTopRow() < 0 {
		m.setStreamTopRow(0)
	}
}

// moveVisualCursor moves the visual caret by delta rows, clamped to the line
// range [0, len(m.lines)-1], then scrolls to keep it on screen. Centralizes the
// up/down cursor-move cases in handleVisualKey.
//
// Safe at the lower clamp because visualMode ⇒ len(m.lines) > 0: enterVisual
// refuses to start on an empty buffer, and eviction shifts the window but never
// empties m.lines. Without that invariant len(m.lines)-1 would be -1 and the
// clamp would set visualCursor = -1.
func (m *model) moveVisualCursor(delta int) {
	m.setVisualCursorRow(m.visualCursorRow() + delta)
	if m.visualCursorRow() < 0 {
		m.setVisualCursorRow(0)
	}
	if m.visualCursorRow() > len(m.lines)-1 {
		m.setVisualCursorRow(len(m.lines) - 1)
	}
	m.ensureVisualVisible()
}

// buildVisualText renders the inclusive visual span [min(anchor,cursor),max] to
// plain displayed text. With no anchor (visualAnchor < 0) it is just the caret
// row. "" if the span resolves to nothing.
func buildVisualText(m *model) string {
	lo, hi := m.selectionBounds()
	return m.textForRows(rangeSlice(lo, hi))
}

// copyVisualText copies the visual span's text (OSC 52) and flashes a count.
func (m *model) copyVisualText() {
	txt := buildVisualText(m)
	if txt == "" {
		return
	}
	osc52Copy(txt)
	m.flash = fmt.Sprintf("copied %d lines", strings.Count(txt, "\n")+1)
}

// buildVisualRef is the reference seam: the visual span as line:<id> (single
// owning entry) or range:<a>..<b>. With no anchor it is the caret row.
func buildVisualRef(m *model) string {
	lo, hi := m.selectionBounds()
	a, b := m.entryIDForLine(lo), m.entryIDForLine(hi)
	if a == "" || b == "" {
		return ""
	}
	if a == b {
		return "line:" + a
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

// handleVisualKey processes keys while in visual mode. Movement (up/down/j/k),
// space (set the selection start), and esc (cancel) are handled directly; the
// copy actions y/Y and save action s are resolved through the keymap so they
// stay remappable, then exit visual mode. Any other key is ignored (stays in
// visual mode).
func (m *model) handleVisualKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Copy/save keys resolve through the keymap so y/Y/s stay remappable even
	// though visual mode otherwise bypasses the main keymap dispatch. Only the
	// copy/save actions return here; every other key (incl. j/k/space/esc,
	// which may also be keymap-bound) falls through to the hardcoded movement
	// switch below.
	// NOTE: add returning cases here with care — movement/esc intentionally
	// fall through to the switch below and must not be swallowed.
	if act, ok := m.resolvedKM().Lookup(msg.String()); ok {
		switch act {
		case keymap.ActionCopyReference:
			m.copyVisualSelection()
			m.exitVisual()
			return m, nil
		case keymap.ActionCopyText:
			m.copyVisualText()
			m.exitVisual()
			return m, nil
		case keymap.ActionSaveViewport:
			lines := m.snapshotSelection()
			m.exitVisual()
			return m, m.saveCmd(lines)
		}
	}
	switch msg.String() {
	case "up", "k":
		m.moveVisualCursor(-1)
	case "down", "j":
		m.moveVisualCursor(1)
	case " ":
		m.setVisualAnchorRow(m.visualCursorRow()) // set/re-set the selection start
	case "esc":
		m.exitVisual()
	}
	return m, nil
}
