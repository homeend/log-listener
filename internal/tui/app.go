// Package tui runs the interactive bubbletea UI: a streaming log view with
// bounded scrollback and a Ctrl+I overlay listing effectively-watched files.
package tui

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"log-listener/internal/render"
)

// ansiRE matches CSI / OSC escape sequences emitted by lipgloss. Good enough
// for stripping styling when horizontal scroll is active — we don't need to
// preserve colors in the scrolled view, just the underlying text.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

func runeLen(s string) int { return utf8.RuneCountInString(s) }

// runeSliceLeft returns s with the first n runes dropped.
func runeSliceLeft(s string, n int) string {
	if n <= 0 {
		return s
	}
	for i := range s {
		if n == 0 {
			return s[i:]
		}
		n--
	}
	return ""
}

// runeTruncate returns the first n runes of s.
func runeTruncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}

// Note: an earlier version had init() calls into lipgloss.SetColorProfile
// and SetHasDarkBackground. Removed — those weren't needed (the
// tea.WithEnvironment hint below already pins the profile for bubbletea's
// internal termenv) and Go runs package init synchronously before main,
// so any blocking termenv probe at init time would delay SSE startup too.

const defaultScrollback = 10000

// FileEntry is a single row in the "effectively watched files" panel.
type FileEntry struct {
	Path  string
	Group string
}

// displayLine is one rendered row in the streaming view. The styled prefix
// (`[group] basename:`) is built at View() time so column toggles and
// group enable/disable flip instantly without rebuilding the cache.
//
// isBlock=true lines are continuation rows from JSON/XML pretty-prints —
// they never carry a prefix and always render with their pre-styled body.
//
// bodyWidth is the unstyled visual width of body (in runes). Cached at
// decompose time so the per-render path doesn't have to stripANSI to
// compute the row's pad-to-width amount.
type displayLine struct {
	group     string // unstyled — used for filtering and prefix render
	file      string // basename, unstyled
	body      string // post-prefix content (plain for heads, dim-styled for blocks)
	bodyWidth int    // visual width of body in runes (unstyled)
	isBlock   bool
}

// EventMsg pushes a rendered event into the TUI.
type EventMsg struct{ Event render.Event }

// FileListMsg replaces the file list shown in the Ctrl+I panel.
type FileListMsg struct{ Files []FileEntry }

// QuitMsg requests the program to exit (used by external goroutines so they
// don't have to know about tea.Quit).
type QuitMsg struct{}

// App is a thin wrapper around the bubbletea Program for callers that don't
// want to touch bubbletea directly. Multiple goroutines can call Push*
// concurrently; the bubbletea event loop serializes everything internally.
type App struct {
	prog *tea.Program
	mu   sync.Mutex
	done bool
}

// New creates an App with the given scrollback size, an initial set of
// "watched files" (shown in the Ctrl+I overlay), and the ordered list of
// group IDs (shown in the Ctrl+G panel, addressable via digit keys 1-9).
// scrollback <= 0 uses the default (10000). Files and groups must be
// passed here, not via SetFiles before Run, because bubbletea's internal
// msgs channel is unbuffered — Send before Run deadlocks the main
// goroutine.
func New(scrollback int, initialFiles []FileEntry, groupIDs []string) *App {
	if scrollback <= 0 {
		scrollback = defaultScrollback
	}
	m := newModel(scrollback)
	m.files = append(m.files, initialFiles...)
	m.groupOrder = append(m.groupOrder, groupIDs...)
	for _, gid := range groupIDs {
		m.groupEnabled[gid] = true
	}
	// tea.WithEnvironment hands a controlled env to bubbletea's internal
	// termenv.Output. With COLORTERM=truecolor termenv accepts the
	// profile from env and skips the OSC 11 / CSI 6n probes that hang
	// when a terminal (or pty wrapper) doesn't auto-respond.
	env := []string{
		"COLORTERM=truecolor",
		"CLICOLOR_FORCE=1",
		"TERM=xterm-256color",
	}
	// Note: mouse capture is intentionally NOT enabled. With WithMouseCellMotion
	// the terminal routes mouse events to the TUI and you lose normal text
	// selection / copy-paste. The TUI is fully keyboard-driven (q, Tab/Ctrl+I,
	// arrows, j/k, g/G), so we don't need mouse input.
	prog := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithEnvironment(env),
	)
	return &App{prog: prog}
}

// Run blocks until the user quits (q or Ctrl+C). Call from the main goroutine.
func (a *App) Run() error {
	_, err := a.prog.Run()
	a.mu.Lock()
	a.done = true
	a.mu.Unlock()
	return err
}

// Push delivers a new rendered event to the TUI. Safe from any goroutine.
// Calls after the program has exited are no-ops (Send to a stopped program
// is internally a no-op in bubbletea, but the done check avoids the
// allocation and the ambiguous semantics).
func (a *App) Push(ev render.Event) {
	a.mu.Lock()
	if a.done {
		a.mu.Unlock()
		return
	}
	prog := a.prog
	a.mu.Unlock()
	prog.Send(EventMsg{Event: ev})
}

// SetFiles updates the file panel contents. Safe from any goroutine.
func (a *App) SetFiles(files []FileEntry) {
	a.mu.Lock()
	if a.done {
		a.mu.Unlock()
		return
	}
	prog := a.prog
	a.mu.Unlock()
	prog.Send(FileListMsg{Files: files})
}

// Quit asks the TUI to exit. Safe from any goroutine.
func (a *App) Quit() {
	a.mu.Lock()
	if a.done {
		a.mu.Unlock()
		return
	}
	prog := a.prog
	a.mu.Unlock()
	prog.Send(QuitMsg{})
}

// model is the bubbletea state. Exported only via App; tests construct it
// directly via newModel.
type model struct {
	events      []displayLine // each event becomes 1 head + N block lines
	scrollback  int
	width       int
	height      int
	showFiles   bool
	files       []FileEntry
	filesScroll int

	// Vertical position in the stream.
	//   tailMode == true  : viewport pinned to the bottom; new events visible
	//                       as they arrive. This is the default.
	//   tailMode == false : viewport locked at absolute index streamTop;
	//                       new events arrive but do NOT shift the view —
	//                       the user is browsing.
	// When the bottom catches up to streamTop (the user scrolls down past
	// the latest event), tailMode flips back to true automatically.
	tailMode  bool
	streamTop int // absolute index of the first visible row when !tailMode

	// Horizontal pan offset (columns clipped off the left).
	horizScroll int

	// Column visibility — toggled with Ctrl+P (group) and Ctrl+L (file).
	showGroup bool
	showFile  bool

	// Group enable/disable — toggled with digit keys 1-9 (mapped to the
	// first 9 entries of groupOrder). A disabled group's events stay in
	// m.events but are skipped during the renderStream window walk.
	groupOrder      []string
	groupEnabled    map[string]bool
	showGroupsPanel bool
	groupsScroll    int

	// Search state.
	//   searchInput == true : user is typing the query after "/"
	//   searchQuery         : characters typed so far (display + commit source)
	//   searchTerm          : committed lowercase substring; empty = inactive
	//   searchHit           : absolute index into m.events of the current hit
	//                         (-1 when no hit is current)
	//   wrapPrompt          : 'n' or 'p' when "wrap around?" is pending;
	//                         0 otherwise. The matching y answer wraps from
	//                         the opposite end of the buffer.
	searchInput bool
	searchQuery string
	searchTerm  string
	searchHit   int
	wrapPrompt  rune
}

const (
	horizStep      = 10 // columns moved per Left/Right keypress
	horizFastStep  = 50 // columns moved per Ctrl+Left/Right
	vertFastStep   = 10 // lines moved per Ctrl+Up/Down
)

func newModel(scrollback int) *model {
	return &model{
		scrollback:   scrollback,
		tailMode:     true,
		showGroup:    true,
		showFile:     true,
		groupEnabled: map[string]bool{},
		searchHit:    -1,
	}
}

// unstickFromTail flips out of tail mode while keeping the visible window
// where it currently is — so the very next render shows exactly the same
// lines as before, but new appends no longer scroll the view. The anchor
// is the absolute index of the first visible event (computed by walking
// backward through ENABLED events for one contentHeight worth).
func (m *model) unstickFromTail() {
	if !m.tailMode {
		return
	}
	m.tailMode = false
	rows := m.contentHeight()
	count := 0
	idx := len(m.events) - 1
	for ; idx >= 0 && count < rows; idx-- {
		if m.lineEnabled(m.events[idx]) {
			count++
		}
	}
	m.streamTop = idx + 1
	if m.streamTop < 0 {
		m.streamTop = 0
	}
}

// maybeReStick re-pins to the tail if the browse window has caught up
// with the latest enabled event. Call after any downward scroll.
func (m *model) maybeReStick() {
	// Count enabled events from streamTop onward; if that fits in one
	// content-height window, we're effectively at the tail.
	rows := m.contentHeight()
	enabled := 0
	for i := m.streamTop; i < len(m.events); i++ {
		if m.lineEnabled(m.events[i]) {
			enabled++
		}
	}
	if enabled <= rows {
		m.tailMode = true
	}
}

func (m *model) Init() tea.Cmd { return nil }

var (
	groupStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6")) // cyan
	fileStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("4")) // blue
	dimStyle   = lipgloss.NewStyle().Faint(true)
	headerBg   = lipgloss.NewStyle().Background(lipgloss.Color("8")).Foreground(lipgloss.Color("15"))
	// Search match styles. matchStyle highlights every visible occurrence;
	// currentMatchStyle marks the row holding the active hit so n/p
	// navigation is visually unambiguous.
	matchStyle        = lipgloss.NewStyle().Background(lipgloss.Color("11")).Foreground(lipgloss.Color("0"))  // yellow bg, black fg
	currentMatchStyle = lipgloss.NewStyle().Background(lipgloss.Color("9")).Foreground(lipgloss.Color("15")) // red bg, white fg
)

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		// Modal key paths take priority — search input swallows almost
		// everything, and a pending wrap prompt swallows y/n/Esc before
		// the normal dispatcher sees them.
		if m.searchInput {
			return m.handleSearchInputKey(msg), nil
		}
		if m.wrapPrompt != 0 {
			return m.handleWrapPromptKey(msg), nil
		}
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "ctrl+i", "tab":
			// Ctrl+I and Tab share byte 0x09 in terminals; bubbletea
			// usually surfaces it as "tab". Accept both so the binding
			// works regardless of terminal handling.
			m.showFiles = !m.showFiles
			if m.showFiles {
				m.showGroupsPanel = false
			}
			m.filesScroll = 0
		case "ctrl+g":
			m.showGroupsPanel = !m.showGroupsPanel
			if m.showGroupsPanel {
				m.showFiles = false
			}
			m.groupsScroll = 0
		case "esc":
			if m.showFiles {
				m.showFiles = false
			}
			if m.showGroupsPanel {
				m.showGroupsPanel = false
			}
			// Esc with no overlay open clears any active search results
			// — term goes away, highlights vanish, hit pointer resets.
			if !m.showFiles && !m.showGroupsPanel && m.searchTerm != "" {
				m.clearSearch()
			}
		case "/":
			m.searchInput = true
			m.searchQuery = ""
		case "n":
			m.searchNext()
		case "p":
			m.searchPrev()
		case "ctrl+p":
			m.showGroup = !m.showGroup
		case "ctrl+l":
			m.showFile = !m.showFile
		case "ctrl+r":
			// Clear the TUI's scrollback. The watcher / sinks / SSE hub
			// keep running; only the in-memory view is reset. Re-enter
			// tail mode so the next event appears immediately at the top.
			m.events = nil
			m.streamTop = 0
			m.tailMode = true
			m.horizScroll = 0
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			idx := int(msg.String()[0] - '1')
			if idx < len(m.groupOrder) {
				gid := m.groupOrder[idx]
				m.groupEnabled[gid] = !m.groupEnabled[gid]
			}
		// Vertical: one row
		case "up", "k":
			if m.showFiles {
				if m.filesScroll > 0 {
					m.filesScroll--
				}
			} else {
				m.unstickFromTail()
				m.streamTop--
				if m.streamTop < 0 {
					m.streamTop = 0
				}
			}
		case "down", "j":
			if m.showFiles {
				if m.filesScroll < len(m.files)-1 {
					m.filesScroll++
				}
			} else if !m.tailMode {
				m.streamTop++
				m.maybeReStick()
			}

		// Vertical: one page
		case "pgup", "ctrl+b":
			page := m.contentHeight()
			if m.showFiles {
				m.filesScroll -= page
				if m.filesScroll < 0 {
					m.filesScroll = 0
				}
			} else {
				m.unstickFromTail()
				m.streamTop -= page
				if m.streamTop < 0 {
					m.streamTop = 0
				}
			}
		case "pgdown", "ctrl+f", " ":
			page := m.contentHeight()
			if m.showFiles {
				m.filesScroll += page
				if m.filesScroll > len(m.files)-1 {
					m.filesScroll = len(m.files) - 1
				}
				if m.filesScroll < 0 {
					m.filesScroll = 0
				}
			} else if !m.tailMode {
				m.streamTop += page
				m.maybeReStick()
			}

		// Vertical: fast (Ctrl/Shift)
		case "ctrl+up", "shift+up":
			if m.showFiles {
				m.filesScroll -= vertFastStep
				if m.filesScroll < 0 {
					m.filesScroll = 0
				}
			} else {
				m.unstickFromTail()
				m.streamTop -= vertFastStep
				if m.streamTop < 0 {
					m.streamTop = 0
				}
			}
		case "ctrl+down", "shift+down":
			if m.showFiles {
				m.filesScroll += vertFastStep
				if m.filesScroll > len(m.files)-1 {
					m.filesScroll = len(m.files) - 1
				}
				if m.filesScroll < 0 {
					m.filesScroll = 0
				}
			} else if !m.tailMode {
				m.streamTop += vertFastStep
				m.maybeReStick()
			}

		// Jump to extremes — Home/g = first line (oldest); End/G = tail.
		case "home", "g":
			if m.showFiles {
				m.filesScroll = 0
			} else {
				m.tailMode = false
				m.streamTop = 0
			}
		case "end", "G":
			if m.showFiles {
				m.filesScroll = len(m.files) - 1
				if m.filesScroll < 0 {
					m.filesScroll = 0
				}
			} else {
				m.tailMode = true // re-stick to the latest, even when new events arrive
			}

		// Horizontal pan
		case "left", "h":
			m.horizScroll -= horizStep
			if m.horizScroll < 0 {
				m.horizScroll = 0
			}
		case "right", "l":
			m.horizScroll += horizStep
		case "ctrl+left", "shift+left":
			m.horizScroll -= horizFastStep
			if m.horizScroll < 0 {
				m.horizScroll = 0
			}
		case "ctrl+right", "shift+right":
			m.horizScroll += horizFastStep
		case "0":
			m.horizScroll = 0
		case "$":
			widest := 0
			for _, dl := range m.events {
				_, w := m.renderDisplayLine(dl)
				if w > widest {
					widest = w
				}
			}
			target := widest - m.width + 10
			if target < 0 {
				target = 0
			}
			m.horizScroll = target
		}
	case EventMsg:
		m.appendEvent(msg.Event)
	case FileListMsg:
		m.files = msg.Files
		if m.filesScroll >= len(m.files) {
			m.filesScroll = 0
		}
	case QuitMsg:
		return m, tea.Quit
	}
	return m, nil
}

func (m *model) appendEvent(ev render.Event) {
	for _, dl := range decomposeEvent(ev) {
		m.events = append(m.events, dl)
	}
	// trim ring buffer; when the user is browsing (!tailMode) we must drag
	// streamTop down by the same amount so the absolute lines they're
	// looking at stay anchored.
	if len(m.events) > m.scrollback {
		drop := len(m.events) - m.scrollback
		m.events = m.events[drop:]
		if !m.tailMode {
			m.streamTop -= drop
			if m.streamTop < 0 {
				m.streamTop = 0
			}
		}
	}
}

// decomposeEvent splits one render.Event into the per-line display rows
// used by the model. Each event becomes a single head row carrying the
// plain text body, plus zero-or-more pre-dim-styled block rows for
// JSON/XML pretty-prints. The styled prefix is NOT baked in here so
// column toggles take effect without rebuilding the cache.
func decomposeEvent(ev render.Event) []displayLine {
	var textBuf strings.Builder
	var blocks []string
	for _, p := range ev.Rendered {
		switch p.Type {
		case "text":
			textBuf.WriteString(p.Value.(string))
		case "json":
			b, err := json.MarshalIndent(p.Value, "", "  ")
			if err == nil {
				blocks = append(blocks, string(b))
			}
		case "xml":
			blocks = append(blocks, p.Value.(string))
		}
	}
	base := filepath.Base(ev.File)
	text := strings.TrimRight(textBuf.String(), "\n")
	out := []displayLine{{
		group: ev.Group, file: base,
		body:      text,
		bodyWidth: runeLen(text),
	}}
	for _, b := range blocks {
		for _, ln := range strings.Split(b, "\n") {
			out = append(out, displayLine{
				group:     ev.Group,
				file:      base,
				body:      dimStyle.Render(ln),
				bodyWidth: runeLen(ln),
				isBlock:   true,
			})
		}
	}
	return out
}

// renderDisplayLine assembles one terminal row from a displayLine using
// the model's current column toggles. Block lines never carry a prefix.
// Returns the styled string AND its visual width (runes) so clipLine can
// pad to terminal width without re-stripping ANSI.
//
// This variant takes no event index — it cannot apply the "current hit"
// background — and is used by the `$` widest-line walk and tests.
func (m *model) renderDisplayLine(dl displayLine) (string, int) {
	return m.renderDisplayLineCore(dl, false)
}

// renderDisplayLineAt is the on-screen variant that knows the line's
// absolute index, so it can apply the active-hit background when the
// row holds the current search hit. Falls through to the plain core
// when no search is active.
func (m *model) renderDisplayLineAt(idx int) (string, int) {
	dl := m.events[idx]
	isCurrent := m.searchTerm != "" && idx == m.searchHit
	return m.renderDisplayLineCore(dl, isCurrent)
}

func (m *model) renderDisplayLineCore(dl displayLine, isCurrent bool) (string, int) {
	body := dl.body
	bodyWidth := dl.bodyWidth
	// When a search term is active, swap out the body for one with
	// highlighted matches. Block lines carry pre-styled ANSI so we
	// strip first; head lines are plain text already.
	if m.searchTerm != "" {
		plain := body
		if dl.isBlock {
			plain = stripANSI(body)
		}
		style := matchStyle.Render
		if isCurrent {
			style = currentMatchStyle.Render
		}
		newBody, newW := highlightMatches(plain, m.searchTerm, style)
		if newW != bodyWidth || newBody != plain {
			body = newBody
			bodyWidth = newW
		} else if dl.isBlock {
			// No match in a block: keep the original dim styling.
			body = dl.body
		}
	}
	if dl.isBlock {
		return body, bodyWidth
	}
	var sb strings.Builder
	visW := bodyWidth
	if m.showGroup {
		sb.WriteString(groupStyle.Render("[" + dl.group + "]"))
		sb.WriteByte(' ')
		visW += runeLen(dl.group) + 3 // "[" + id + "]" + " "
	}
	if m.showFile {
		sb.WriteString(fileStyle.Render(dl.file))
		sb.WriteString(": ")
		visW += runeLen(dl.file) + 2 // ": "
	}
	sb.WriteString(body)
	return sb.String(), visW
}

// lineEnabled reports whether dl should appear in the stream window
// given the current per-group toggles. Block lines inherit their head's
// group, so they're filtered consistently.
func (m *model) lineEnabled(dl displayLine) bool {
	if dl.group == "" {
		return true
	}
	enabled, known := m.groupEnabled[dl.group]
	if !known {
		return true // unknown groups (shouldn't happen) default to visible
	}
	return enabled
}

// contentHeight returns the number of rows available for the body between
// the header (1 row) and the footer (1 row).
func (m *model) contentHeight() int {
	h := m.height - 2
	if h < 1 {
		h = 1
	}
	return h
}

func (m *model) View() string {
	if m.height == 0 {
		return ""
	}
	header := headerBg.Width(m.width).Render(" log-listener — q quit · Tab files · Ctrl+G groups · 1-9 toggle · Ctrl+P/L cols · Ctrl+R clear · / search · n/p next/prev ")
	contentH := m.contentHeight()

	var body string
	switch {
	case m.showGroupsPanel:
		body = m.renderGroupsPanel(contentH)
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
	if m.searchInput {
		return headerBg.Width(m.width).Render(" /" + m.searchQuery + "_")
	}
	if m.wrapPrompt != 0 {
		text := " No more hits — wrap to top? (y/n) "
		if m.wrapPrompt == 'p' {
			text = " No more hits — wrap to bottom? (y/n) "
		}
		return headerBg.Width(m.width).Render(text)
	}
	pos := "tail"
	if !m.tailMode {
		pos = fmt.Sprintf("@%d/%d", m.streamTop, len(m.events))
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
	search := ""
	if m.searchTerm != "" {
		search = fmt.Sprintf(" · /%s", m.searchQuery)
	}
	return dimStyle.Width(m.width).Render(fmt.Sprintf(" events: %d · %s · col: %d%s · %s · files: %d%s ",
		len(m.events), pos, m.horizScroll, cols, groupStat, len(m.files), search))
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

func (m *model) renderGroupsPanel(rows int) string {
	out := make([]string, 0, rows)
	out = append(out, headerBg.Width(m.width).Render(" Groups (Ctrl+G or Esc to close · 1-9 to toggle) "))
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

// padRow strips ANSI to measure visible width, then appends spaces to fill
// the terminal row. Used by the side panels (files / groups) where rows
// have arbitrary styling so we don't have a pre-computed width.
func (m *model) padRow(s string) string {
	if m.width <= 0 {
		return s
	}
	w := runeLen(stripANSI(s))
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

// collectVisible returns up to rows absolute event indices in display
// order. In tail mode we walk backward from the latest event; in
// browse mode we walk forward from streamTop. Disabled-group lines
// are skipped, so a run of hidden events doesn't leave a gap.
func (m *model) collectVisible(rows int) []int {
	if rows <= 0 || len(m.events) == 0 {
		return nil
	}
	out := make([]int, 0, rows)
	if m.tailMode {
		for i := len(m.events) - 1; i >= 0 && len(out) < rows; i-- {
			if !m.lineEnabled(m.events[i]) {
				continue
			}
			out = append(out, i)
		}
		// reverse (we collected newest→oldest)
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
		return out
	}
	for i := m.streamTop; i < len(m.events) && len(out) < rows; i++ {
		if !m.lineEnabled(m.events[i]) {
			continue
		}
		out = append(out, i)
	}
	return out
}

func (m *model) renderStream(rows int) string {
	if len(m.events) == 0 {
		return m.blankRows(rows)
	}
	visible := m.collectVisible(rows)
	rendered := make([]string, 0, rows)
	for _, idx := range visible {
		styled, visW := m.renderDisplayLineAt(idx)
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

// clipLine applies horizontal scroll + width clamping to a single rendered
// line. Two responsibilities, in this order:
//
//  1. If horizScroll > 0, strip ANSI and slice runewise to expose the
//     scrolled-right portion. Otherwise the styled line is kept verbatim.
//  2. Pad with trailing spaces to exactly m.width so the terminal repaints
//     the entire row — without this, switching to a shorter line during
//     PgUp/PgDn leaves the previous row's tail visible (the "ghost row"
//     glitch the user reported).
//
// visW is the unstyled visual width of the line. Callers compute it once
// in renderDisplayLine so we don't need stripANSI on the hot path.
func (m *model) clipLine(line string, visW int) string {
	if m.width <= 0 {
		return line
	}
	if m.horizScroll == 0 {
		if visW >= m.width {
			return line
		}
		return line + strings.Repeat(" ", m.width-visW)
	}
	plain := stripANSI(line)
	plain = runeSliceLeft(plain, m.horizScroll)
	plain = runeTruncate(plain, m.width)
	if w := runeLen(plain); w < m.width {
		plain += strings.Repeat(" ", m.width-w)
	}
	return plain
}

func (m *model) renderFiles(rows int) string {
	out := make([]string, 0, rows)
	out = append(out, headerBg.Width(m.width).Render(" Watched files (Ctrl+I or Esc to close) "))
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
