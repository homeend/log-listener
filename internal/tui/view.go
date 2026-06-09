package tui

import (
	"fmt"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"github.com/homeend/log-listener/internal/keymap"
)

var (
	groupStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6")) // cyan
	fileStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("4")) // blue
	dimStyle   = lipgloss.NewStyle().Faint(true)
	headerBg   = lipgloss.NewStyle().Background(lipgloss.Color("8")).Foreground(lipgloss.Color("15"))
	// Search match styles. matchStyle highlights every visible occurrence;
	// currentMatchStyle marks the row holding the active hit so n/p
	// navigation is visually unambiguous.
	matchStyle        = lipgloss.NewStyle().Background(lipgloss.Color("11")).Foreground(lipgloss.Color("0")) // yellow bg, black fg
	currentMatchStyle = lipgloss.NewStyle().Background(lipgloss.Color("9")).Foreground(lipgloss.Color("15")) // red bg, white fg
)

// hint renders "<keys> <label>" for an action using the model's keymap, e.g.
// "⌃G groups" on macOS or "Ctrl+G groups" elsewhere.
func (m *model) hint(a keymap.Action, label string) string {
	return m.keyDisplay(a) + " " + label
}

// resolvedKM returns m.km, falling back to the built-in default for the
// current OS when a model was constructed via newModel without a keymap
// (only happens in tests; New always sets m.km).
func (m *model) resolvedKM() *keymap.Keymap {
	if m.km == nil {
		return keymap.Default(runtime.GOOS)
	}
	return m.km
}

// keyDisplay returns the per-OS label for an action's keys (e.g. "⌃G"),
// nil-safe so render paths that build a model without an explicit keymap fall
// back to the built-in defaults instead of panicking.
func (m *model) keyDisplay(a keymap.Action) string {
	return m.resolvedKM().Display(a)
}

func (m *model) View() string {
	if m.height == 0 {
		return ""
	}
	hints := []string{
		m.hint(keymap.ActionQuit, "quit"),
		m.hint(keymap.ActionToggleFiles, "files"),
		m.hint(keymap.ActionToggleGroups, "groups"),
		m.hint(keymap.ActionToggleRenderers, "rend"),
		"1-9 grp",
		m.hint(keymap.ActionCollapseAll, "collapse"),
		m.hint(keymap.ActionToggleGroupCol, "grpcol"),
		m.hint(keymap.ActionToggleFileCol, "filecol"),
		m.hint(keymap.ActionClear, "clear"),
		m.hint(keymap.ActionSearch, "search"),
		m.hint(keymap.ActionNextMatch, "next") + "/" + m.hint(keymap.ActionPrevMatch, "prev"),
		m.hint(keymap.ActionFilter, "filter"),
		m.hint(keymap.ActionHelp, "help"),
	}
	header := headerBg.Width(m.width).MaxHeight(1).Render(" log-listener — " + strings.Join(hints, " · ") + " ")
	contentH := m.contentHeight()

	var body string
	switch {
	case m.showHelp:
		body = m.renderHelp(contentH)
	case m.showGroupsPanel:
		body = m.renderGroupsPanel(contentH)
	case m.showRenderersPanel:
		body = m.renderRenderersPanel(contentH)
	case m.showFiles:
		body = m.renderFiles(contentH)
	default:
		body = m.renderStream(contentH)
	}

	footer := m.renderFooter()
	return header + "\n" + body + "\n" + footer
}

// renderFooter assembles the bottom status line. Three modes, in
// priority order:
//
//  1. Search input active ("/") — show "/<typed>_" so the user can see
//     what's being typed.
//  2. Wrap prompt pending — show "No more hits — wrap to top|bottom? (y/n)".
//  3. Normal — events / position / column / group / file counters,
//     plus a "/term" suffix when a committed search term is active.
func (m *model) renderFooter() string {
	if m.visualMode {
		return headerBg.Width(m.width).MaxHeight(1).Render(" VISUAL  ↑↓ move · space anchor · y ref · Y text · s save · esc cancel ")
	}
	if m.searchInput {
		prefix := " /"
		if m.searchRegex {
			prefix = " /(regex) "
		}
		return headerBg.Width(m.width).MaxHeight(1).Render(prefix + m.searchQuery + "_")
	}
	if m.wrapPrompt != 0 {
		text := " No more hits — wrap to top? (y/n) "
		if m.wrapPrompt == 'p' {
			text = " No more hits — wrap to bottom? (y/n) "
		}
		return headerBg.Width(m.width).MaxHeight(1).Render(text)
	}
	if m.flash != "" {
		return headerBg.Width(m.width).MaxHeight(1).Render(" " + m.flash + " ")
	}
	pos := "tail"
	if !m.tailMode {
		pos = fmt.Sprintf("@%d/%d", m.streamTopRow(), len(m.lines))
	}
	cols := ""
	if !m.showGroup {
		cols += " -G"
	}
	if !m.showFile {
		cols += " -F"
	}
	disabled := m.disabledGroupCount()
	groupStat := fmt.Sprintf("groups: %d", len(m.groupOrder))
	if disabled > 0 {
		groupStat += fmt.Sprintf(" (%d off)", disabled)
	}
	rendStat := ""
	if len(m.rendererOrder) > 0 {
		rendStat = fmt.Sprintf(" · rend: %d", len(m.rendererOrder))
		if off := m.disabledRendererCount(); off > 0 {
			rendStat += fmt.Sprintf(" (%d off)", off)
		}
	}
	search := ""
	if m.matcher != nil {
		search = fmt.Sprintf(" · /%s", m.searchQuery)
		if m.filterMode {
			search += " filter"
		}
	}
	colStat := fmt.Sprintf("col: %d", m.horizScroll)
	if m.wordWrap {
		colStat = "wrap"
	}
	return dimStyle.Width(m.width).MaxHeight(1).Render(fmt.Sprintf(" events: %d · %s · %s%s · %s%s · files: %d%s ",
		len(m.lines), pos, colStat, cols, groupStat, rendStat, len(m.files), search))
}

func (m *model) disabledGroupCount() int {
	n := 0
	for _, gid := range m.groupOrder {
		if !m.groupEnabled[gid] {
			n++
		}
	}
	return n
}

func (m *model) disabledRendererCount() int {
	n := 0
	for _, on := range m.rendererEnabled {
		if !on {
			n++
		}
	}
	return n
}

// toggleRenderer flips the i-th renderer's enable state, both in the
// pipeline (via the wired-up callback) and in the TUI's mirror cache,
// then re-renders every scrollback entry so existing lines reflect the
// new state immediately. Out-of-range indices and a nil callback are
// silent no-ops (lets unit tests construct a model without plumbing).
func (m *model) toggleRenderer(i int) {
	if i < 0 || i >= len(m.rendererOrder) {
		return
	}
	m.rendererEnabled[i] = !m.rendererEnabled[i]
	if m.setRendererEnabled != nil {
		m.setRendererEnabled(i, m.rendererEnabled[i])
	}
	m.reRenderAll()
}

func (m *model) renderGroupsPanel(rows int) string {
	out := make([]string, 0, rows)
	out = append(out, headerBg.Width(m.width).MaxHeight(1).Render(" Groups ("+m.keyDisplay(keymap.ActionToggleGroups)+" or "+m.keyDisplay(keymap.ActionCloseOverlay)+" to close · 1-9 to toggle) "))
	if len(m.groupOrder) == 0 {
		out = append(out, m.padRow(dimStyle.Render("  (no groups defined)")))
		for i := 2; i < rows; i++ {
			out = append(out, m.blankRow())
		}
		return strings.Join(out, "\n")
	}
	counts := map[string]int{}
	for _, f := range m.files {
		counts[f.Group]++
	}
	avail := rows - 1
	start := m.groupsScroll
	if start > len(m.groupOrder)-avail {
		start = len(m.groupOrder) - avail
	}
	if start < 0 {
		start = 0
	}
	end := start + avail
	if end > len(m.groupOrder) {
		end = len(m.groupOrder)
	}
	for i := start; i < end; i++ {
		gid := m.groupOrder[i]
		mark := "OFF"
		if m.groupEnabled[gid] {
			mark = "ON "
		}
		key := "[ ]"
		if i < 9 {
			key = fmt.Sprintf("[%d]", i+1)
		}
		out = append(out, m.padRow(fmt.Sprintf("  %s  %s  %s  (%d file%s)",
			key, mark, groupStyle.Render(gid),
			counts[gid], pluralS(counts[gid]))))
	}
	for i := end - start; i < avail; i++ {
		out = append(out, m.blankRow())
	}
	return strings.Join(out, "\n")
}

// rendererShiftChar returns the shifted-digit character that toggles
// the i-th renderer (i in [0, 9)). Mirrors the digit-key mapping
// used by the groups panel.
func rendererShiftChar(i int) string {
	chars := []string{"!", "@", "#", "$", "%", "^", "&", "*", "("}
	if i < 0 || i >= len(chars) {
		return " "
	}
	return chars[i]
}

func (m *model) renderRenderersPanel(rows int) string {
	out := make([]string, 0, rows)
	out = append(out, headerBg.Width(m.width).MaxHeight(1).Render(" Renderers ("+m.keyDisplay(keymap.ActionToggleRenderers)+" or "+m.keyDisplay(keymap.ActionCloseOverlay)+" to close · !-( to toggle) "))
	if len(m.rendererOrder) == 0 {
		out = append(out, m.padRow(dimStyle.Render("  (no renderers defined)")))
		for i := 2; i < rows; i++ {
			out = append(out, m.blankRow())
		}
		return strings.Join(out, "\n")
	}
	avail := rows - 1
	start := m.renderersScroll
	if start > len(m.rendererOrder)-avail {
		start = len(m.rendererOrder) - avail
	}
	if start < 0 {
		start = 0
	}
	end := start + avail
	if end > len(m.rendererOrder) {
		end = len(m.rendererOrder)
	}
	for i := start; i < end; i++ {
		mark := "OFF"
		if m.rendererEnabled[i] {
			mark = "ON "
		}
		key := "[ ]"
		if i < 9 {
			key = "[" + rendererShiftChar(i) + "]"
		}
		out = append(out, m.padRow(fmt.Sprintf("  %s  %s  %s",
			key, mark, groupStyle.Render(m.rendererOrder[i]))))
	}
	for i := end - start; i < avail; i++ {
		out = append(out, m.blankRow())
	}
	return strings.Join(out, "\n")
}

// padRow strips ANSI to measure visible width, then appends spaces to fill
// the terminal row. Used by the side panels (files / groups) where rows
// have arbitrary styling so we don't have a pre-computed width.
func (m *model) padRow(s string) string {
	if m.width <= 0 {
		return s
	}
	w := dispWidth(s)
	if w >= m.width {
		return s
	}
	return s + strings.Repeat(" ", m.width-w)
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// visibleRowCost is how many terminal rows line idx occupies when painted: 1
// unless word wrap is on and the rendered row is wider than the viewport.
func (m *model) visibleRowCost(idx int) int {
	if !m.wordWrap {
		return 1
	}
	_, visW := m.renderVisibleRow(idx)
	if m.width <= 0 || visW <= m.width {
		return 1
	}
	return (visW + m.width - 1) / m.width
}

// collectVisible returns up to rows terminal rows' worth of absolute event
// indices in display order. In tail mode we walk backward from the latest
// event; in browse mode we walk forward from streamTop. Disabled-group lines
// are skipped, so a run of hidden events doesn't leave a gap. When word wrap
// is on, each entry may occupy more than one terminal row (visibleRowCost);
// the loop stops once the accumulated row cost reaches rows.
func (m *model) collectVisible(rows int) []int {
	if rows <= 0 || len(m.lines) == 0 {
		return nil
	}
	if m.filterMode {
		fil := m.filteredIndices()
		if len(fil) == 0 {
			return nil
		}
		start := 0
		for start < len(fil) && fil[start] < m.streamTopRow() {
			start++
		}
		if start >= len(fil) {
			start = len(fil) - 1
		}
		out := make([]int, 0, rows)
		used := 0
		for k := start; k < len(fil) && used < rows; k++ {
			out = append(out, fil[k])
			used += m.visibleRowCost(fil[k])
		}
		return out
	}
	out := make([]int, 0, rows)
	used := 0
	if m.tailMode {
		for i := len(m.lines) - 1; i >= 0 && used < rows; i-- {
			if !m.lineEnabled(m.lines[i]) {
				continue
			}
			out = append(out, i)
			used += m.visibleRowCost(i)
		}
		// reverse (we collected newest→oldest)
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
		return out
	}
	for i := m.streamTopRow(); i < len(m.lines) && used < rows; i++ {
		if !m.lineEnabled(m.lines[i]) {
			continue
		}
		out = append(out, i)
		used += m.visibleRowCost(i)
	}
	return out
}

// publishViewport reports the on-screen entry range (first..last visible entry
// id) to the shared buffer, if a publisher is wired. No-op when the callback is
// nil (tests) or nothing is visible (publishes empty).
func (m *model) publishViewport(visible []int) {
	if m.setViewport == nil {
		return
	}
	if len(visible) == 0 {
		m.setViewport("", "")
		return
	}
	from := m.entryIDForLine(visible[0])
	to := m.entryIDForLine(visible[len(visible)-1])
	m.setViewport(from, to)
}

// renderVisibleRow builds the full terminal row for line idx, including any
// leading gutter bar (visual selection, exception mark, or focus). It is the
// single source of width truth shared by the paint path (renderStream) and the
// wrap height accounting (visibleRowCost).
func (m *model) renderVisibleRow(idx int) (string, int) {
	styled, visW := m.renderDisplayLineAt(idx)
	if m.visualMode {
		if vb, ok := m.visualBar(idx); ok {
			styled = vb + styled
			visW += visualBarWidth
		}
	} else {
		if bar, ok := m.exceptionBar(idx); ok {
			styled = bar + styled
			visW += exceptionBarWidth
		}
		if fb, ok := m.focusBar(idx); ok {
			styled = fb + styled
			visW += focusBarWidth
		}
	}
	return styled, visW
}

func (m *model) renderStream(rows int) string {
	if len(m.lines) == 0 {
		m.publishViewport(nil) // attached TUI, nothing on screen → from/to ""
		return m.blankRows(rows)
	}
	m.ensureBlocks()
	visible := m.collectVisible(rows)
	m.publishViewport(visible)
	if m.wordWrap {
		return m.renderStreamWrapped(visible, rows)
	}
	rendered := make([]string, 0, rows)
	for _, idx := range visible {
		styled, visW := m.renderVisibleRow(idx)
		rendered = append(rendered, m.clipLine(styled, visW))
	}
	missing := rows - len(rendered)
	if missing > 0 {
		blank := m.blankRow()
		for i := 0; i < missing; i++ {
			rendered = append(rendered, blank)
		}
	}
	return strings.Join(rendered, "\n")
}

// renderStreamWrapped paints the visible lines with word wrap on: each line
// expands to ceil(visW/width) terminal rows. When the expanded rows overflow
// the viewport, tail mode bottom-aligns (keeps the newest rows, dropping the
// topmost line's leading rows) and browse mode top-aligns (keeps the oldest,
// dropping the bottom line's trailing rows). Short of a full screen, pad.
func (m *model) renderStreamWrapped(visible []int, rows int) string {
	segs := make([]string, 0, rows+8)
	for _, idx := range visible {
		styled, visW := m.renderVisibleRow(idx)
		segs = append(segs, wrapLine(styled, visW, m.width)...)
	}
	if len(segs) > rows {
		// Bottom-align only in tail mode AND not filtering. collectVisible's
		// filter branch is always top-anchored (forward walk from streamTop),
		// so bottom-aligning while filtering would trim the front and drop
		// visible[0] — the published from/to range — off the top of the screen.
		if m.tailMode && !m.filterMode {
			segs = segs[len(segs)-rows:]
		} else {
			segs = segs[:rows]
		}
	}
	for len(segs) < rows {
		segs = append(segs, m.blankRow())
	}
	return strings.Join(segs, "\n")
}

// blankRow returns a string of spaces exactly m.width long — used to clear
// any leftover content under shorter lines after scrolling.
func (m *model) blankRow() string {
	if m.width <= 0 {
		return ""
	}
	return strings.Repeat(" ", m.width)
}

// blankRows returns n blank rows separated by \n (each row is m.width wide).
func (m *model) blankRows(n int) string {
	if n <= 0 {
		return ""
	}
	blank := m.blankRow()
	rows := make([]string, n)
	for i := range rows {
		rows[i] = blank
	}
	return strings.Join(rows, "\n")
}

// clipLine fits a rendered line into exactly one terminal row of width
// m.width. Two responsibilities:
//
//  1. Expose the horizontal window [horizScroll, horizScroll+width) and
//     truncate anything past the right edge. A row must never exceed the
//     terminal width — an over-wide row wraps, overflows the viewport, and
//     scrolls the header off the top (the vanishing-header glitch, hit most
//     often when a wide rendered-JSON block sits at the top during
//     PgUp/PgDn or search).
//  2. Pad with trailing spaces to exactly m.width so the terminal repaints
//     the whole row — without this, switching to a shorter line during
//     PgUp/PgDn leaves the previous row's tail visible (the "ghost row"
//     glitch the user reported).
//
// Slicing is ANSI-aware: escape sequences (colors, the search-term
// highlight) are zero-width and copied through, so styling survives both
// horizontal scroll and right-edge truncation.
//
// visW is the unstyled visual width of the line. Callers compute it once in
// renderDisplayLine, letting the common case (no scroll, fits the width)
// skip the per-rune ANSI walk entirely.
func (m *model) clipLine(line string, visW int) string {
	if m.width <= 0 {
		return line
	}
	if m.horizScroll == 0 && visW <= m.width {
		return line + strings.Repeat(" ", m.width-visW)
	}
	return clipANSIWindow(line, m.horizScroll, m.width)
}

// clipANSIWindow returns the horizontal window [skip, skip+width) of line,
// measured in display columns, with all ANSI escape sequences preserved.
// Columns (not runes): a wide CJK rune counts as 2. Escape sequences are
// zero-width and copied verbatim wherever they fall, so a styled span that
// straddles the left edge keeps its opening code and one truncated at the
// right edge is closed by a trailing reset (added so an open style can't bleed
// into the trailing pad). A wide rune that would straddle the left edge or
// overflow the right edge is dropped and replaced by a filler space so the
// result is always exactly width columns.
func clipANSIWindow(line string, skip, width int) string {
	if width <= 0 {
		return ""
	}
	spans := ansiRE.FindAllStringIndex(line, -1)
	var sb strings.Builder
	styled := false
	visible, emitted := 0, 0 // display columns consumed from the start / written
	si, i := 0, 0
	for i < len(line) {
		if si < len(spans) && spans[si][0] == i {
			// Escape sequence at the cursor — copy verbatim, zero width.
			sb.WriteString(line[spans[si][0]:spans[si][1]])
			styled = true
			i = spans[si][1]
			si++
			continue
		}
		r, sz := utf8.DecodeRuneInString(line[i:])
		w := runeWidth(r)
		if visible >= skip {
			if emitted+w > width {
				break // would overflow the right edge (incl. a wide rune)
			}
			sb.WriteString(line[i : i+sz])
			emitted += w
		} else if visible+w > skip {
			// A wide rune straddles the left edge — can't show half of it.
			// Emit a filler space so the visible columns stay aligned.
			if emitted < width {
				sb.WriteByte(' ')
				emitted++
			}
		}
		visible += w
		i += sz
	}
	out := sb.String()
	if styled {
		out += "\x1b[0m"
	}
	if emitted < width {
		out += strings.Repeat(" ", width-emitted)
	}
	return out
}

func (m *model) renderFiles(rows int) string {
	out := make([]string, 0, rows)
	out = append(out, headerBg.Width(m.width).MaxHeight(1).Render(" Watched files ("+m.keyDisplay(keymap.ActionToggleFiles)+" or "+m.keyDisplay(keymap.ActionCloseOverlay)+" to close) "))
	if len(m.files) == 0 {
		out = append(out, m.padRow(dimStyle.Render("  (no files yet)")))
		for i := 2; i < rows; i++ {
			out = append(out, m.blankRow())
		}
		return strings.Join(out, "\n")
	}
	avail := rows - 1
	start := m.filesScroll
	if start > len(m.files)-avail {
		start = len(m.files) - avail
	}
	if start < 0 {
		start = 0
	}
	end := start + avail
	if end > len(m.files) {
		end = len(m.files)
	}
	for i := start; i < end; i++ {
		f := m.files[i]
		out = append(out, m.padRow(fmt.Sprintf("  %s  %s",
			groupStyle.Render("["+f.Group+"]"), f.Path)))
	}
	for i := end - start; i < avail; i++ {
		out = append(out, m.blankRow())
	}
	return strings.Join(out, "\n")
}
