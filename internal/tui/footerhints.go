package tui

import (
	"fmt"
	"strings"

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

// fitHints renders the left side (optional label + hints joined by " · ")
// within budget display columns. When the full list overflows it drops hint
// entries from the right end (lowest priority) and appends "…" until it fits;
// the label is always kept.
func fitHints(label string, hints []string, budget int) string {
	prefix := ""
	if label != "" {
		prefix = label + "  "
	}
	full := prefix + strings.Join(hints, " · ")
	if dispWidth(full) <= budget {
		return full
	}
	for n := len(hints) - 1; n >= 1; n-- {
		s := prefix + strings.Join(hints[:n], " · ") + " · …"
		if dispWidth(s) <= budget {
			return s
		}
	}
	return prefix + "…"
}

// composeFooterBar lays out the bottom bar: fitted context hints on the left,
// the compact status tail right-aligned to m.width. Mode contexts (non-empty
// label) use the headerBg fill, matching the existing visual-mode bar; the
// default tail context uses dimStyle, matching the old status line.
func (m *model) composeFooterBar(label string, hints []string) string {
	right := m.compactStatus()
	style := dimStyle
	if label != "" {
		style = headerBg
	}
	if m.width <= 0 {
		return style.MaxHeight(1).Render(" " + strings.Join(hints, " · ") + " ")
	}
	// Reserve: 1 leading space + 1 trailing space + at least 1 gap + status.
	budget := m.width - dispWidth(right) - 3
	if budget < 0 {
		budget = 0
	}
	left := fitHints(label, hints, budget)
	gap := m.width - dispWidth(left) - dispWidth(right) - 2 // 1 lead + 1 trail
	if gap < 1 {
		gap = 1
	}
	text := " " + left + strings.Repeat(" ", gap) + right + " "
	return style.Width(m.width).MaxHeight(1).Render(text)
}
