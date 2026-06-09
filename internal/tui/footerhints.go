package tui

import (
	"fmt"

	"github.com/homeend/log-listener/internal/keymap"
)

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

// hintPair renders "<key1>·<key2> <label>" for a forward/back action pair.
func (m *model) hintPair(a1, a2 keymap.Action, label string) string {
	return m.keyDisplay(a1) + "·" + m.keyDisplay(a2) + " " + label
}

// contextHints selects the active context by precedence (visual > block >
// search/filter > browse > tail) and returns its short uppercase label
// (empty for the default tail context) plus the ordered hint strings. Every
// key is resolved through keyDisplay, so per-OS forms and overrides apply.
func (m *model) contextHints() (label string, hints []string) {
	switch {
	case m.visualMode:
		return "VISUAL", []string{
			"space anchor",
			m.hint(keymap.ActionCopyReference, "ref"),
			m.hint(keymap.ActionCopyText, "text"),
			m.hint(keymap.ActionSaveViewport, "save"),
			m.hint(keymap.ActionCloseOverlay, "cancel"),
		}
	case m.blockFocused:
		return "BLOCK", []string{
			m.hintPair(keymap.ActionNextBlock, keymap.ActionPrevBlock, "next·prev"),
			m.hintPair(keymap.ActionNextMarkedBlock, keymap.ActionPrevMarkedBlock, "marked"),
			m.hint(keymap.ActionToggleExceptionMarks, "marks"),
			m.hint(keymap.ActionCopyReference, "copy"),
			m.hint(keymap.ActionCloseOverlay, "esc"),
		}
	case m.matcher != nil:
		word, lbl := "filter", "SEARCH"
		if m.filterMode {
			word, lbl = "unfilter", "FILTER"
		}
		return lbl, []string{
			m.hintPair(keymap.ActionNextMatch, keymap.ActionPrevMatch, "next·prev"),
			m.hint(keymap.ActionFilter, word),
			m.hintPair(keymap.ActionNextBlock, keymap.ActionPrevBlock, "blocks"),
			m.hint(keymap.ActionCloseOverlay, "clear"),
		}
	case !m.tailMode:
		return "BROWSE", []string{
			m.hint(keymap.ActionBottom, "tail"),
			m.hint(keymap.ActionTop, "top"),
			m.hintPair(keymap.ActionScrollUp, keymap.ActionScrollDown, "scroll"),
			m.hintPair(keymap.ActionPageUp, keymap.ActionPageDown, "page"),
			m.hint(keymap.ActionVisualSelect, "select"),
		}
	default:
		return "", []string{
			m.hint(keymap.ActionSearch, "search"),
			m.hint(keymap.ActionVisualSelect, "select"),
			m.hintPair(keymap.ActionNextBlock, keymap.ActionPrevBlock, "blocks"),
			m.hint(keymap.ActionCollapseAll, "collapse"),
			m.hint(keymap.ActionHelp, "help"),
		}
	}
}
