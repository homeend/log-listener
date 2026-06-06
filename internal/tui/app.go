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

// ansiRE matches CSI / OSC escape sequences emitted by lipgloss. Used both
// to strip styling (stripANSI) and to walk it while preserving it during
// horizontal-scroll slicing (clipANSIWindow).
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

func runeLen(s string) int { return utf8.RuneCountInString(s) }

// dispWidth is the terminal cell width of s — wide (CJK) runes count as 2,
// zero-width/combining as 0 — with any ANSI stripped. Width/clip math must use
// this, not runeLen: a rune is not always one column, and counting it as one
// makes a row of wide characters overflow and wrap, corrupting the layout.
func dispWidth(s string) int { return lipgloss.Width(s) }

// runeWidth is the cell width of a single rune (0, 1, or 2).
func runeWidth(r rune) int { return lipgloss.Width(string(r)) }

// expandTabs replaces tabs with spaces to 8-column tab stops so a body's rune
// count equals its terminal display width. Without this a tab (1 rune, up to 8
// columns) makes the width math underestimate, and the row overflows and wraps
// in the terminal — pushing the header off-screen and corrupting the layout
// (Java stack-trace frames start with a tab). Fast-returns when there's no tab.
func expandTabs(s string) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	const tabStop = 8
	var b strings.Builder
	col := 0
	for _, r := range s {
		if r == '\t' {
			n := tabStop - col%tabStop
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteRune(r)
		col++
	}
	return b.String()
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

// ReloadMsg replaces the renderer/group/file panels after a live config
// reload. Toggle state is reset to the supplied StartOff defaults and existing
// scrollback is re-rendered under the new renderers. The caller must have
// swapped the pipeline (which renderFn reads) BEFORE sending this — the
// message carries panel state, not the pipeline itself.
type ReloadMsg struct {
	Groups    []GroupInfo
	Renderers []RendererInfo
	Files     []FileEntry
}

// App is a thin wrapper around the bubbletea Program for callers that don't
// want to touch bubbletea directly. Multiple goroutines can call Push*
// concurrently; the bubbletea event loop serializes everything internally.
type App struct {
	prog *tea.Program
	mu   sync.Mutex
	done bool
}

// GroupInfo is one entry in Options.Groups: ID + soft-off seed.
type GroupInfo struct {
	ID       string
	StartOff bool
}

// RendererInfo is one entry in Options.Renderers: name + soft-off seed.
type RendererInfo struct {
	Name     string
	StartOff bool
}

// Options bundles everything tui.New needs. Most fields are optional —
// nil callbacks turn the corresponding feature into a no-op so tests
// can construct an App without plumbing the pipeline.
type Options struct {
	Scrollback        int
	InitialFiles      []FileEntry
	Groups            []GroupInfo
	Renderers         []RendererInfo
	SetRendererOn     func(idx int, on bool) // called when shift+digit toggles a renderer
	RenderFn          RenderFunc             // called per scrollback entry when toggling triggers re-render
}

// New creates an App from Options. Files and groups must be passed
// here, not via SetFiles before Run, because bubbletea's internal
// msgs channel is unbuffered — Send before Run deadlocks the main
// goroutine.
func New(opts Options) *App {
	scrollback := opts.Scrollback
	if scrollback <= 0 {
		scrollback = defaultScrollback
	}
	m := newModel(scrollback)
	m.files = append(m.files, opts.InitialFiles...)
	for _, g := range opts.Groups {
		m.groupOrder = append(m.groupOrder, g.ID)
		m.groupEnabled[g.ID] = !g.StartOff
	}
	for _, r := range opts.Renderers {
		m.rendererOrder = append(m.rendererOrder, r.Name)
		m.rendererEnabled = append(m.rendererEnabled, !r.StartOff)
	}
	m.setRendererEnabled = opts.SetRendererOn
	m.renderFn = opts.RenderFn
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

// Reload reseeds the panels and re-renders scrollback after a config reload.
// Safe from any goroutine.
func (a *App) Reload(groups []GroupInfo, renderers []RendererInfo, files []FileEntry) {
	a.mu.Lock()
	if a.done {
		a.mu.Unlock()
		return
	}
	prog := a.prog
	a.mu.Unlock()
	prog.Send(ReloadMsg{Groups: groups, Renderers: renderers, Files: files})
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
// scrollbackEvent holds the source data for one rendered emission
// (one log line that came out of the pipeline) plus the displayLines
// it currently decomposes into. The source fields (group, file, raw)
// survive any number of re-renders; the lines field is regenerated
// whenever a renderer toggle changes how the line should look.
type scrollbackEvent struct {
	group, file, raw string
	lines            []displayLine
}

type model struct {
	// entries is the source of truth: one per pipeline emission. lines
	// is a derived flat cache (concat of every entry's lines) — kept in
	// sync by appendEvent / trimToCap / reRenderAll. Everything on the
	// hot path (View, search, collectVisible, streamTop/searchHit
	// indexing) reads from m.lines, so the cached layout means no
	// per-render walk of m.entries.
	entries     []scrollbackEvent
	lines       []displayLine
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
	// m.lines but are skipped during the renderStream window walk.
	groupOrder      []string
	groupEnabled    map[string]bool
	showGroupsPanel bool
	groupsScroll    int

	// Search state.
	//   searchInput == true : user is typing the query after "/"
	//   searchQuery         : characters typed so far (display + commit source)
	//   searchTerm          : committed lowercase substring; empty = inactive
	//   searchHit           : absolute index into m.lines of the current hit
	//                         (-1 when no hit is current)
	//   wrapPrompt          : 'n' or 'p' when "wrap around?" is pending;
	//                         0 otherwise. The matching y answer wraps from
	//                         the opposite end of the buffer.
	searchInput bool
	searchQuery string
	searchTerm  string
	searchHit   int
	wrapPrompt  rune
	// lastQuery is the most recently committed query (original case),
	// preserved across clears so "/"+Enter repeats it. filterMode is the
	// `t` "show only matching entries" toggle (used by later tasks).
	lastQuery  string
	filterMode bool

	// Renderer enable/disable — toggled with the shifted-digit chars
	// (!@#$%^&*( for 1-9). Ctrl+E opens the renderers panel. The
	// authoritative on/off state lives in the pipeline (atomic.Bool per
	// renderer); rendererEnabled is the TUI's cached mirror used for
	// panel rendering and footer counts. setRendererEnabled wraps both
	// the pipeline call and the cache update, and triggers a re-render
	// of the whole scrollback.
	rendererOrder      []string
	rendererEnabled    []bool
	showRenderersPanel bool
	renderersScroll    int
	setRendererEnabled func(idx int, on bool) // pipeline-side flip
	renderFn           RenderFunc             // re-render a single source

	// collapseMultiline hides continuation rows in the stream view —
	// a line whose body starts with whitespace, or any pretty-printed
	// JSON/XML block row. Heads with hidden continuations get a "[...]"
	// suffix appended at render time so the user can tell more exists.
	// Toggled with the `m` key. TUI-only; stdout/SSE still emit full
	// content.
	collapseMultiline bool
}

// RenderFunc runs a single (group, file, raw) tuple through the
// pipeline using its current renderer-enable state, returning the
// resulting Event. ok=false means the pipeline dropped the line
// (drop_unmatched + no matching renderer); the entry stays in
// scrollback so a later toggle can resurrect it, but contributes
// zero display lines. main.go provides this; the TUI handles
// decompose internally (displayLine is unexported).
type RenderFunc func(group, file, raw string) (ev render.Event, ok bool)

const (
	horizStep      = 10 // columns moved per Left/Right keypress
	horizFastStep  = 50 // columns moved per Ctrl+Left/Right
	vertFastStep   = 10 // lines moved per Ctrl+Up/Down
	hitMargin      = horizStep / 2 // left-margin columns when panning to a hit
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
	idx := len(m.lines) - 1
	for ; idx >= 0 && count < rows; idx-- {
		if m.lineEnabled(m.lines[idx]) {
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
	for i := m.streamTop; i < len(m.lines); i++ {
		if m.lineEnabled(m.lines[i]) {
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
				m.showRenderersPanel = false
			}
			m.filesScroll = 0
		case "ctrl+g":
			m.showGroupsPanel = !m.showGroupsPanel
			if m.showGroupsPanel {
				m.showFiles = false
				m.showRenderersPanel = false
			}
			m.groupsScroll = 0
		case "esc":
			if m.showFiles {
				m.showFiles = false
			}
			if m.showGroupsPanel {
				m.showGroupsPanel = false
			}
			if m.showRenderersPanel {
				m.showRenderersPanel = false
			}
			// Esc with no overlay open clears any active search results
			// — term goes away, highlights vanish, hit pointer resets.
			if !m.showFiles && !m.showGroupsPanel && !m.showRenderersPanel && m.searchTerm != "" {
				m.clearSearch()
			}
		case "/":
			m.searchInput = true
			m.searchQuery = ""
		case "n":
			m.searchNext()
		case "p":
			m.searchPrev()
		case "t":
			if m.searchTerm != "" {
				m.filterMode = !m.filterMode
				if m.filterMode {
					m.tailMode = false
				}
			}
		case "ctrl+p":
			m.showGroup = !m.showGroup
		case "ctrl+l":
			m.showFile = !m.showFile
		case "ctrl+r":
			// Clear the TUI's scrollback. The watcher / sinks / SSE hub
			// keep running; only the in-memory view is reset. Re-enter
			// tail mode so the next event appears immediately at the top.
			m.entries = nil
			m.lines = nil
			m.streamTop = 0
			m.tailMode = true
			m.horizScroll = 0
			m.searchHit = -1
			// Filtering an emptied buffer would render blank; drop it.
			m.filterMode = false
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
			} else if m.searchTerm != "" {
				m.searchPrev()
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
			} else if m.searchTerm != "" {
				m.searchNext()
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
		// Renderer toggles: shifted digits 1-9 → !@#$%^&*( .
		// $ used to be "jump-to-widest-line" — removed in favor of this.
		case "!":
			m.toggleRenderer(0)
		case "@":
			m.toggleRenderer(1)
		case "#":
			m.toggleRenderer(2)
		case "$":
			m.toggleRenderer(3)
		case "%":
			m.toggleRenderer(4)
		case "^":
			m.toggleRenderer(5)
		case "&":
			m.toggleRenderer(6)
		case "*":
			m.toggleRenderer(7)
		case "(":
			m.toggleRenderer(8)
		case "m":
			// Collapse multiline entries (continuation rows hidden behind
			// a "[...]" marker on the head). Toggles repeatedly.
			m.collapseMultiline = !m.collapseMultiline
		case "ctrl+e":
			m.showRenderersPanel = !m.showRenderersPanel
			if m.showRenderersPanel {
				m.showFiles = false
				m.showGroupsPanel = false
			}
			m.renderersScroll = 0
		}
	case EventMsg:
		m.appendEvent(msg.Event)
	case FileListMsg:
		m.files = msg.Files
		if m.filesScroll >= len(m.files) {
			m.filesScroll = 0
		}
	case ReloadMsg:
		m.applyReload(msg)
	case QuitMsg:
		return m, tea.Quit
	}
	return m, nil
}

// applyReload swaps in the new config's panels and toggle state, then
// re-renders existing scrollback through renderFn (which now reads the
// reloaded pipeline). Scrollback source entries are preserved; only their
// rendered lines are rebuilt. Toggle state is reset to the new config's
// StartOff defaults — the renderer set may have changed, so preserving old
// indices would be ambiguous.
func (m *model) applyReload(msg ReloadMsg) {
	// Reset to nil (the newModel idiom) rather than slice[:0] — the renderer
	// set may shrink, and nil avoids retaining/aliasing the old backing array.
	m.groupOrder = nil
	m.groupEnabled = map[string]bool{}
	for _, g := range msg.Groups {
		m.groupOrder = append(m.groupOrder, g.ID)
		m.groupEnabled[g.ID] = !g.StartOff
	}
	m.rendererOrder = nil
	m.rendererEnabled = nil
	for _, r := range msg.Renderers {
		m.rendererOrder = append(m.rendererOrder, r.Name)
		m.rendererEnabled = append(m.rendererEnabled, !r.StartOff)
	}
	m.files = msg.Files
	if m.filesScroll >= len(m.files) {
		m.filesScroll = 0
	}
	if m.groupsScroll >= len(m.groupOrder) {
		m.groupsScroll = 0
	}
	if m.renderersScroll >= len(m.rendererOrder) {
		m.renderersScroll = 0
	}
	m.reRenderAll()
}

func (m *model) appendEvent(ev render.Event) {
	lines := decomposeEvent(ev)
	m.entries = append(m.entries, scrollbackEvent{
		group: ev.Group,
		file:  ev.File,
		raw:   ev.Raw,
		lines: lines,
	})
	m.lines = append(m.lines, lines...)
	m.trimToCap()
}

// appendStored pushes a pre-built scrollbackEvent (used when re-running
// the pipeline on existing scrollback isn't applicable — e.g. tests
// that bypass the pipeline). lines may be empty.
func (m *model) appendStored(e scrollbackEvent) {
	m.entries = append(m.entries, e)
	m.lines = append(m.lines, e.lines...)
	m.trimToCap()
}

// trimToCap enforces the scrollback line-count cap by evicting WHOLE
// entries from the head of m.entries until the flat-line count fits.
// Whole-entry eviction keeps m.entries and m.lines in lockstep — no
// half-evicted event whose head row is gone but blocks remain.
//
// When the user is browsing (!tailMode), streamTop and searchHit are
// dragged down by exactly the number of lines evicted so the absolute
// rows they reference don't drift.
func (m *model) trimToCap() {
	if m.scrollback <= 0 || len(m.lines) <= m.scrollback {
		return
	}
	dropLines := 0
	dropEntries := 0
	for dropEntries < len(m.entries) && len(m.lines)-dropLines > m.scrollback {
		dropLines += len(m.entries[dropEntries].lines)
		dropEntries++
	}
	if dropEntries == 0 {
		return
	}
	m.entries = m.entries[dropEntries:]
	m.lines = m.lines[dropLines:]
	if !m.tailMode {
		m.streamTop -= dropLines
		if m.streamTop < 0 {
			m.streamTop = 0
		}
	}
	if m.searchHit >= 0 {
		m.searchHit -= dropLines
		if m.searchHit < 0 {
			m.searchHit = -1 // hit scrolled off-screen
		}
	}
}

// reRenderAll walks every stored entry through renderFn and rebuilds
// m.lines from the resulting display lines. Called when a renderer
// toggle changes how the pipeline dispatches lines. Index anchors
// (streamTop, searchHit) are clamped to the new flat-line range —
// the viewport may visibly jump if a long stack-trace block collapsed
// into a single raw line, which is the correct UX for "this is what
// this line looks like now."
//
// If renderFn is nil (no pipeline plumbed — early bootstrap, tests
// that bypass main.go) reRenderAll is a no-op.
func (m *model) reRenderAll() {
	if m.renderFn == nil {
		return
	}
	totalLines := 0
	for i := range m.entries {
		ev, ok := m.renderFn(m.entries[i].group, m.entries[i].file, m.entries[i].raw)
		var lines []displayLine
		if ok {
			lines = decomposeEvent(ev)
		}
		m.entries[i].lines = lines
		totalLines += len(lines)
	}
	flat := make([]displayLine, 0, totalLines)
	for i := range m.entries {
		flat = append(flat, m.entries[i].lines...)
	}
	m.lines = flat
	// Clamp anchors to the new line count.
	if m.streamTop > len(m.lines) {
		m.streamTop = len(m.lines)
	}
	if m.streamTop < 0 {
		m.streamTop = 0
	}
	if m.searchHit >= len(m.lines) {
		m.searchHit = -1
	}
}

// decomposeEvent splits one render.Event into the per-line display rows
// used by the model. The text body becomes a head row plus one dim block row
// per embedded newline (a template "\n" literal), then zero-or-more
// pre-dim-styled block rows for JSON/XML pretty-prints. Splitting on newlines
// keeps the "one displayLine = one terminal row" invariant. The styled prefix
// is NOT baked in here so column toggles take effect without rebuilding the
// cache.
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
	// A text part may carry embedded newlines (a template "\n" literal). Each
	// physical line must be its own displayLine so the "one displayLine = one
	// terminal row" invariant holds — otherwise the row wraps and breaks the
	// layout. The first line is the head (keeps the [group] file: prefix);
	// the rest render as block continuation rows, exactly like JSON/XML lines.
	textLines := strings.Split(text, "\n")
	head := expandTabs(textLines[0])
	out := []displayLine{{
		group: ev.Group, file: base,
		body:      head,
		bodyWidth: dispWidth(head),
	}}
	for _, ln := range textLines[1:] {
		ln = expandTabs(ln)
		out = append(out, displayLine{
			group:     ev.Group,
			file:      base,
			body:      dimStyle.Render(ln),
			bodyWidth: dispWidth(ln),
			isBlock:   true,
		})
	}
	for _, b := range blocks {
		for _, ln := range strings.Split(b, "\n") {
			out = append(out, displayLine{
				group:     ev.Group,
				file:      base,
				body:      dimStyle.Render(ln),
				bodyWidth: dispWidth(ln),
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
// row holds the current search hit and append the "[...]" suffix when
// collapsed-multiline mode is hiding continuation rows after this one.
// Falls through to the plain core otherwise.
func (m *model) renderDisplayLineAt(idx int) (string, int) {
	dl := m.lines[idx]
	isCurrent := m.searchTerm != "" && idx == m.searchHit
	if m.collapseMultiline && idx+1 < len(m.lines) && isContinuation(m.lines[idx+1]) {
		// Mutate the local copy so the marker shows on this row only.
		// dimStyle wraps the marker in ANSI; runeLen on the unstyled
		// text yields the correct visible width.
		const marker = " [...]"
		dl.body = dl.body + dimStyle.Render(marker)
		dl.bodyWidth += dispWidth(marker)
	}
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
		visW += dispWidth(dl.group) + 3 // "[" + id + "]" + " "
	}
	if m.showFile {
		sb.WriteString(fileStyle.Render(dl.file))
		sb.WriteString(": ")
		visW += dispWidth(dl.file) + 2 // ": "
	}
	sb.WriteString(body)
	return sb.String(), visW
}

// groupEnabledLine reports whether dl's group is enabled (ignores the
// collapse-multiline toggle). Used by the search filter, which shows whole
// matching entries including their block lines.
func (m *model) groupEnabledLine(dl displayLine) bool {
	if dl.group == "" {
		return true
	}
	enabled, known := m.groupEnabled[dl.group]
	if !known {
		return true // unknown groups (shouldn't happen) default to visible
	}
	return enabled
}

// lineEnabled reports whether dl appears in the normal stream window given
// the per-group toggles AND the multiline-collapse toggle.
func (m *model) lineEnabled(dl displayLine) bool {
	if m.collapseMultiline && isContinuation(dl) {
		return false
	}
	return m.groupEnabledLine(dl)
}

// filteredIndices returns the absolute m.lines indices shown when the search
// filter is active: every group-enabled line of every entry that has at
// least one line containing the term. Whole entries are kept so a matched
// JSON/XML block appears in full alongside its head line. Returns nil when no
// term is set. Collapse-multiline is intentionally ignored here.
func (m *model) filteredIndices() []int {
	if m.searchTerm == "" {
		return nil
	}
	var out []int
	off := 0
	for _, e := range m.entries {
		n := len(e.lines)
		matched := false
		for _, dl := range e.lines {
			if strings.Contains(strings.ToLower(matchHaystack(dl)), m.searchTerm) {
				matched = true
				break
			}
		}
		if matched {
			for k := 0; k < n; k++ {
				idx := off + k
				if m.groupEnabledLine(m.lines[idx]) {
					out = append(out, idx)
				}
			}
		}
		off += n
	}
	return out
}

// isContinuation reports whether dl looks like a follow-on row of a
// multiline log entry — either a JSON/XML pretty-print block row, or
// a line whose body starts with whitespace (the convention Python
// tracebacks and many other multi-line log formats use). Empty bodies
// don't count.
func isContinuation(dl displayLine) bool {
	if dl.isBlock {
		return true
	}
	if len(dl.body) == 0 {
		return false
	}
	first := dl.body[0]
	return first == ' ' || first == '\t'
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
	header := headerBg.Width(m.width).MaxHeight(1).Render(" log-listener — q quit · Tab files · Ctrl+G groups · Ctrl+E rend · 1-9 grp · m collapse · Ctrl+P/L cols · Ctrl+R clear · / search · n/p · t filter ")
	contentH := m.contentHeight()

	var body string
	switch {
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
	if m.searchInput {
		return headerBg.Width(m.width).MaxHeight(1).Render(" /" + m.searchQuery + "_")
	}
	if m.wrapPrompt != 0 {
		text := " No more hits — wrap to top? (y/n) "
		if m.wrapPrompt == 'p' {
			text = " No more hits — wrap to bottom? (y/n) "
		}
		return headerBg.Width(m.width).MaxHeight(1).Render(text)
	}
	pos := "tail"
	if !m.tailMode {
		pos = fmt.Sprintf("@%d/%d", m.streamTop, len(m.lines))
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
	if m.searchTerm != "" {
		search = fmt.Sprintf(" · /%s", m.searchQuery)
		if m.filterMode {
			search += " filter"
		}
	}
	return dimStyle.Width(m.width).MaxHeight(1).Render(fmt.Sprintf(" events: %d · %s · col: %d%s · %s%s · files: %d%s ",
		len(m.lines), pos, m.horizScroll, cols, groupStat, rendStat, len(m.files), search))
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
	out = append(out, headerBg.Width(m.width).MaxHeight(1).Render(" Groups (Ctrl+G or Esc to close · 1-9 to toggle) "))
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
	out = append(out, headerBg.Width(m.width).MaxHeight(1).Render(" Renderers (Ctrl+E or Esc to close · !-( to toggle) "))
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

// collectVisible returns up to rows absolute event indices in display
// order. In tail mode we walk backward from the latest event; in
// browse mode we walk forward from streamTop. Disabled-group lines
// are skipped, so a run of hidden events doesn't leave a gap.
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
		for start < len(fil) && fil[start] < m.streamTop {
			start++
		}
		if start >= len(fil) {
			start = len(fil) - 1
		}
		end := start + rows
		if end > len(fil) {
			end = len(fil)
		}
		return append([]int(nil), fil[start:end]...)
	}
	out := make([]int, 0, rows)
	if m.tailMode {
		for i := len(m.lines) - 1; i >= 0 && len(out) < rows; i-- {
			if !m.lineEnabled(m.lines[i]) {
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
	for i := m.streamTop; i < len(m.lines) && len(out) < rows; i++ {
		if !m.lineEnabled(m.lines[i]) {
			continue
		}
		out = append(out, i)
	}
	return out
}

func (m *model) renderStream(rows int) string {
	if len(m.lines) == 0 {
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
	out = append(out, headerBg.Width(m.width).MaxHeight(1).Render(" Watched files (Ctrl+I or Esc to close) "))
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
