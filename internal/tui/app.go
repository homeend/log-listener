// Package tui runs the interactive bubbletea UI: a streaming log view with
// bounded scrollback and a Ctrl+I overlay listing effectively-watched files.
package tui

import (
	"runtime"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

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
const defaultFilenameWidth = 16

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
	TruncateFiles bool                   // tui.truncate_filenames default
	FilenameWidth int                    // tui.filename_width (0 => default)
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
	m.truncateFiles = opts.TruncateFiles
	m.filenameWidth = opts.FilenameWidth
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
	// records the row count per visible ID at the last reconcile (to count how
	// many head rows were evicted since); clearedSeq is the Clear floor (entries
	// with Seq <= clearedSeq are hidden).
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
	//   tailMode == false : viewport locked at the streamTop anchor;
	//                       new events arrive but do NOT shift the view —
	//                       the user is browsing.
	// When the bottom catches up to streamTop (the user scrolls down past
	// the latest event), tailMode flips back to true automatically.
	tailMode   bool
	streamTopA rowAnchor // anchor for the first visible row when !tailMode (see viewanchor.go)

	// Horizontal pan offset (columns clipped off the left).
	horizScroll int

	// Column visibility — toggled with Ctrl+P (group) and Ctrl+L (file).
	showGroup bool
	showFile  bool

	// File column truncation: middle-ellipsis long filenames when enabled.
	// filenameWidth <=0 falls back to defaultFilenameWidth.
	truncateFiles bool
	filenameWidth int

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
	//   searchHitA          : stable anchor for the current search hit
	//                         (zero/sentinel = no hit; resolves to -1 via searchHitRow)
	//   wrapPrompt          : 'n' or 'p' when "wrap around?" is pending;
	//                         0 otherwise. The matching y answer wraps from
	//                         the opposite end of the buffer.
	searchInput bool
	searchQuery string
	searchRegex bool
	matcher     *searchmatch.Matcher
	searchHitA  rowAnchor
	wrapPrompt  rune

	// Visual selection mode (vim-style `v`): visualMode gates the modal key
	// path; visualCursorA is the stable anchor for the moving line (resolves
	// to 0 when evicted, via visualCursorRow); visualAnchorA is the stable
	// anchor for the selection start (zero/sentinel = unset; resolves to -1
	// via visualAnchorRow).
	visualMode    bool
	visualCursorA rowAnchor
	visualAnchorA rowAnchor
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

	// Help overlay state.
	showHelp   bool   // help overlay open (modal)
	helpQuery  string // live filter for the help list (independent of searchQuery)
	helpScroll int    // first visible help row
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

		displayCache: map[string][]displayLine{},
		prevIDLines:  map[string]int{},

		showExceptionMarks: true,
	}
	// Every model owns a buffer so newModel-built models (tests/standalone)
	// can seed via appendEvent. New overrides this with the shared buffer.
	m.buf = linebuf.New(scrollback, tuiDecompose)
	return m
}

// effFilenameWidth is the truncation limit in display columns, applying the
// default when unset — mirroring how Scrollback treats 0 as "use the default".
func (m *model) effFilenameWidth() int {
	if m.filenameWidth > 0 {
		return m.filenameWidth
	}
	return defaultFilenameWidth
}

func (m *model) Init() tea.Cmd { return nil }
