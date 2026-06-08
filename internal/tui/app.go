// Package tui runs the interactive bubbletea UI: a streaming log view with
// bounded scrollback and a Ctrl+I overlay listing effectively-watched files.
package tui

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/homeend/log-listener/internal/blocks"
	"github.com/homeend/log-listener/internal/keymap"
	"github.com/homeend/log-listener/internal/linebuf"
	"github.com/homeend/log-listener/internal/render"
	"github.com/homeend/log-listener/internal/searchmatch"
)

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
	Scrollback    int
	InitialFiles  []FileEntry
	Groups        []GroupInfo
	Renderers     []RendererInfo
	Keymap        *keymap.Keymap         // resolved key bindings; nil → built-in for runtime.GOOS
	SetRendererOn func(idx int, on bool) // called when shift+digit toggles a renderer
	RenderFn      RenderFunc             // called per scrollback entry when toggling triggers re-render
	InitialEvents []render.Event         // seeded into scrollback before Run (preload)
	SetViewport   func(from, to string)  // publishes the on-screen entry range (TUI mode only)
	Buffer        *linebuf.Buffer        // shared record store; nil → an owned buffer (tests/standalone)
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
	m.km = opts.Keymap
	if m.km == nil {
		m.km = keymap.Default(runtime.GOOS)
	}
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
	m.setViewport = opts.SetViewport
	if opts.Buffer != nil {
		m.buf = opts.Buffer // shared store; replaces newModel's owned buffer
	}
	for _, ev := range opts.InitialEvents {
		m.appendEvent(ev)
	}
	m.reconcile() // seed m.lines from the buffer (preload may already be present)
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
	id               string
	group, file, raw string
	lines            []displayLine
}

type model struct {
	km *keymap.Keymap
	// entries is the source of truth: one per pipeline emission. lines
	// is a derived flat cache (concat of every entry's lines) — kept in
	// sync by appendEvent / trimToCap / reRenderAll. Everything on the
	// hot path (View, search, collectVisible, streamTop/searchHit
	// indexing) reads from m.lines, so the cached layout means no
	// per-render walk of m.entries.
	lines []displayLine

	// Shared-buffer sourcing (slice 5-1). buf is the authoritative record
	// store (shared with MCP in TUI mode; an owned buffer in tests).
	// displayCache memoizes each entry's display rows by ID; prevIDLines
	// records the row count per visible ID at the last reconcile (for the
	// eviction index-drag); clearedSeq is the Clear floor (entries with
	// Seq <= clearedSeq are hidden).
	buf          *linebuf.Buffer
	displayCache map[string][]displayLine
	lastGen      uint64
	prevIDLines  map[string]int
	clearedSeq   uint64
	// window is the ordered set of entries currently in m.lines, captured by
	// the same reconcile that built m.lines/displayCache — so readers index
	// against a consistent snapshot and never re-snapshot the (concurrently
	// mutated) buffer themselves.
	window      []*linebuf.Entry
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
	//   matcher             : compiled smart-case predicate; nil = inactive
	//   searchHit           : absolute index into m.lines of the current hit
	//                         (-1 when no hit is current)
	//   wrapPrompt          : 'n' or 'p' when "wrap around?" is pending;
	//                         0 otherwise. The matching y answer wraps from
	//                         the opposite end of the buffer.
	searchInput bool
	searchQuery string
	searchRegex bool
	matcher     *searchmatch.Matcher
	searchHit   int
	wrapPrompt  rune

	// Visual selection mode (vim-style `v`): visualMode gates the modal key
	// path; visualCursor is the moving line; visualAnchor is the selection
	// start (-1 until the first space sets it).
	visualMode   bool
	visualCursor int
	visualAnchor int
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
	setViewport        func(from, to string)  // publishes on-screen entry range to the shared buffer

	// collapseMultiline hides continuation rows in the stream view —
	// a line whose body starts with whitespace, or any pretty-printed
	// JSON/XML block row. Heads with hidden continuations get a "[...]"
	// suffix appended at render time so the user can tell more exists.
	// Toggled with the `m` key. TUI-only; stdout/SSE still emit full
	// content.
	collapseMultiline bool

	// flash is a transient status line (e.g. a save confirmation) shown in the
	// footer until the next key event. saveDir overrides the export directory
	// (default "" = cwd); it is a test seam, never set in production.
	flash   string
	saveDir string

	// blocks is the cached segmentation of m.lines (see internal/blocks).
	// blocksDirty is set by every m.lines mutator; ensureBlocks recomputes
	// when dirty. showExceptionMarks toggles the renderException left bar.
	blocks             []blocks.Block
	blocksDirty        bool
	showExceptionMarks bool

	// blockFocused is true when the user explicitly navigated to a multi-line
	// block via block-nav keys (]/[/}/{). Set by jumpToBlockHead; cleared by
	// any vertical scroll, esc, or cap eviction. Gates the focusBar indicator
	// and the block-copy rule in buildReference.
	blockFocused bool
}

// RenderFunc runs a single (group, file, raw) tuple through the
// pipeline using its current renderer-enable state, returning the
// resulting Event. ok=false means the pipeline dropped the line
// (drop_unmatched + no matching renderer); the entry stays in
// scrollback so a later toggle can resurrect it, but contributes
// zero display lines. main.go provides this; the TUI handles
// decompose internally (displayLine is unexported).
type RenderFunc func(group, file, raw string) (ev render.Event, ok bool)

func newModel(scrollback int) *model {
	m := &model{
		scrollback:   scrollback,
		tailMode:     true,
		showGroup:    true,
		showFile:     true,
		groupEnabled: map[string]bool{},
		searchHit:    -1,
		visualAnchor: -1,

		displayCache: map[string][]displayLine{},
		prevIDLines:  map[string]int{},

		showExceptionMarks: true,
	}
	// Every model owns a buffer so newModel-built models (tests/standalone)
	// can seed via appendEvent. New overrides this with the shared buffer.
	m.buf = linebuf.New(scrollback, tuiDecompose)
	return m
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
	}
	header := headerBg.Width(m.width).MaxHeight(1).Render(" log-listener — " + strings.Join(hints, " · ") + " ")
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
	if m.visualMode {
		return headerBg.Width(m.width).MaxHeight(1).Render(" VISUAL  ↑↓ move · space anchor · y ref · Y text · esc cancel ")
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
	if m.matcher != nil {
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

func (m *model) renderStream(rows int) string {
	if len(m.lines) == 0 {
		m.publishViewport(nil) // attached TUI, nothing on screen → from/to ""
		return m.blankRows(rows)
	}
	m.ensureBlocks()
	visible := m.collectVisible(rows)
	m.publishViewport(visible)
	rendered := make([]string, 0, rows)
	for _, idx := range visible {
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
