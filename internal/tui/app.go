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

// New creates an App with the given scrollback size and an initial set of
// "watched files" shown in the Ctrl+I overlay. scrollback <= 0 uses the
// default (10000). The initial files list must be passed here (not via
// SetFiles before Run) because bubbletea's internal msgs channel is
// unbuffered — calling Send before Run deadlocks the main goroutine.
func New(scrollback int, initialFiles []FileEntry) *App {
	if scrollback <= 0 {
		scrollback = defaultScrollback
	}
	m := newModel(scrollback)
	m.files = append(m.files, initialFiles...)
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
	events      []string // pre-rendered lines for the stream
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
}

const (
	horizStep      = 10 // columns moved per Left/Right keypress
	horizFastStep  = 50 // columns moved per Ctrl+Left/Right
	vertFastStep   = 10 // lines moved per Ctrl+Up/Down
)

func newModel(scrollback int) *model {
	return &model{scrollback: scrollback, tailMode: true}
}

// unstickFromTail flips out of tail mode while keeping the visible window
// where it currently is — so the very next render shows exactly the same
// lines as before, but new appends no longer scroll the view.
func (m *model) unstickFromTail() {
	if !m.tailMode {
		return
	}
	m.tailMode = false
	m.streamTop = len(m.events) - m.contentHeight()
	if m.streamTop < 0 {
		m.streamTop = 0
	}
}

// maybeReStick re-pins to the tail if streamTop has run off the bottom of
// the buffer (or past it). Call after any downward scroll.
func (m *model) maybeReStick() {
	maxTop := len(m.events) - m.contentHeight()
	if m.streamTop >= maxTop {
		m.streamTop = maxTop
		if m.streamTop < 0 {
			m.streamTop = 0
		}
		m.tailMode = true
	}
}

func (m *model) Init() tea.Cmd { return nil }

var (
	groupStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6")) // cyan
	fileStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("4")) // blue
	dimStyle   = lipgloss.NewStyle().Faint(true)
	headerBg   = lipgloss.NewStyle().Background(lipgloss.Color("8")).Foreground(lipgloss.Color("15"))
)

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "ctrl+i", "tab":
			// Ctrl+I and Tab share byte 0x09 in terminals; bubbletea
			// usually surfaces it as "tab". Accept both so the binding
			// works regardless of terminal handling.
			m.showFiles = !m.showFiles
			m.filesScroll = 0
		case "esc":
			if m.showFiles {
				m.showFiles = false
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
			for _, ev := range m.events {
				if w := runeLen(stripANSI(ev)); w > widest {
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
	for _, line := range renderEventLines(ev) {
		m.events = append(m.events, line)
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

// renderEventLines flattens a render.Event into one or more display lines.
// The first line is the "[<group>] <basename>: <text>" header; JSON/XML
// blocks follow on their own lines.
func renderEventLines(ev render.Event) []string {
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
	prefix := fmt.Sprintf("%s %s: ",
		groupStyle.Render("["+ev.Group+"]"),
		fileStyle.Render(filepath.Base(ev.File)))
	text := strings.TrimRight(textBuf.String(), "\n")
	first := prefix + text
	lines := []string{first}
	for _, b := range blocks {
		for _, ln := range strings.Split(b, "\n") {
			lines = append(lines, dimStyle.Render(ln))
		}
	}
	return lines
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
	header := headerBg.Width(m.width).Render(" log-listener — q quit · Tab files · ↑/↓ scroll · ←/→ pan · PgUp/PgDn page ")
	contentH := m.contentHeight()

	body := m.renderStream(contentH)
	if m.showFiles {
		body = m.renderFiles(contentH)
	}

	pos := "tail"
	if !m.tailMode {
		pos = fmt.Sprintf("@%d/%d", m.streamTop, len(m.events))
	}
	footer := dimStyle.Render(fmt.Sprintf(" events: %d · %s · col: %d · files: %d ",
		len(m.events), pos, m.horizScroll, len(m.files)))

	return header + "\n" + body + "\n" + footer
}

func (m *model) renderStream(rows int) string {
	if len(m.events) == 0 {
		return strings.Repeat("\n", rows-1)
	}
	var start, end int
	if m.tailMode {
		end = len(m.events)
		start = end - rows
		if start < 0 {
			start = 0
		}
	} else {
		start = m.streamTop
		if start < 0 {
			start = 0
		}
		if start > len(m.events) {
			start = len(m.events)
		}
		end = start + rows
		if end > len(m.events) {
			end = len(m.events)
		}
	}
	visible := m.events[start:end]
	rendered := make([]string, len(visible))
	for i, line := range visible {
		rendered[i] = m.clipLine(line)
	}
	out := strings.Join(rendered, "\n")
	missing := rows - len(rendered)
	if missing > 0 {
		out += strings.Repeat("\n", missing)
	}
	return out
}

// clipLine applies horizontal scroll + width truncation to a single rendered
// line. At horizScroll == 0 we return the line as-is — the terminal wraps
// over-wide lines (long-established log-tailer behaviour) and we avoid the
// stripANSI regex on the hot path. When horizScroll > 0 we strip ANSI, slice
// runewise, and emit plain text — scrolling and colorized styling don't
// easily coexist with naive slicing.
func (m *model) clipLine(line string) string {
	if m.horizScroll == 0 || m.width <= 0 {
		return line
	}
	plain := stripANSI(line)
	plain = runeSliceLeft(plain, m.horizScroll)
	plain = runeTruncate(plain, m.width)
	return plain
}

func (m *model) renderFiles(rows int) string {
	var b strings.Builder
	b.WriteString(headerBg.Width(m.width).Render(" Watched files (Ctrl+I or Esc to close) "))
	b.WriteString("\n")
	if len(m.files) == 0 {
		b.WriteString(dimStyle.Render("  (no files yet)"))
		b.WriteString(strings.Repeat("\n", rows-2))
		return b.String()
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
		row := fmt.Sprintf("  %s  %s",
			groupStyle.Render("["+f.Group+"]"),
			f.Path)
		b.WriteString(row)
		b.WriteString("\n")
	}
	for i := end - start; i < avail; i++ {
		b.WriteString("\n")
	}
	return b.String()
}
