// Package tui runs the interactive bubbletea UI: a streaming log view with
// bounded scrollback and a Ctrl+I overlay listing effectively-watched files.
package tui

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"log-listener/internal/render"
)

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

// New creates an App with the given scrollback size. scrollback <= 0 uses the
// default (10000).
func New(scrollback int) *App {
	if scrollback <= 0 {
		scrollback = defaultScrollback
	}
	m := newModel(scrollback)
	prog := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
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
func (a *App) Push(ev render.Event) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.done {
		return
	}
	a.prog.Send(EventMsg{Event: ev})
}

// SetFiles updates the file panel contents. Safe from any goroutine.
func (a *App) SetFiles(files []FileEntry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.done {
		return
	}
	a.prog.Send(FileListMsg{Files: files})
}

// Quit asks the TUI to exit. Safe from any goroutine.
func (a *App) Quit() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.done {
		return
	}
	a.prog.Send(QuitMsg{})
}

// model is the bubbletea state. Exported only via App; tests construct it
// directly via newModel.
type model struct {
	events     []string // pre-rendered lines for the stream
	scrollback int
	width      int
	height     int
	showFiles  bool
	files      []FileEntry
	filesScroll int
	streamScroll int // 0 = pinned to bottom; positive = scrolled back N lines
}

func newModel(scrollback int) *model {
	return &model{scrollback: scrollback}
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
		case "up", "k":
			if m.showFiles {
				if m.filesScroll > 0 {
					m.filesScroll--
				}
			} else {
				m.streamScroll++
			}
		case "down", "j":
			if m.showFiles {
				if m.filesScroll < len(m.files)-1 {
					m.filesScroll++
				}
			} else if m.streamScroll > 0 {
				m.streamScroll--
			}
		case "g":
			if m.showFiles {
				m.filesScroll = 0
			} else {
				m.streamScroll = len(m.events)
			}
		case "G":
			if m.showFiles {
				m.filesScroll = len(m.files) - 1
				if m.filesScroll < 0 {
					m.filesScroll = 0
				}
			} else {
				m.streamScroll = 0
			}
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
	// trim ring buffer
	if len(m.events) > m.scrollback {
		m.events = m.events[len(m.events)-m.scrollback:]
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

func (m *model) View() string {
	if m.height == 0 {
		return ""
	}
	header := headerBg.Width(m.width).Render(" log-listener — q quit · Ctrl+I files · ↑/↓ scroll ")
	footerHeight := 1
	contentHeight := m.height - 1 - footerHeight
	if contentHeight < 1 {
		contentHeight = 1
	}

	body := m.renderStream(contentHeight)
	if m.showFiles {
		body = m.renderFiles(contentHeight)
	}

	footer := dimStyle.Render(fmt.Sprintf(" events: %d · scroll: %d · files: %d ",
		len(m.events), m.streamScroll, len(m.files)))

	return header + "\n" + body + "\n" + footer
}

func (m *model) renderStream(rows int) string {
	if len(m.events) == 0 {
		return strings.Repeat("\n", rows-1)
	}
	end := len(m.events) - m.streamScroll
	if end < 0 {
		end = 0
	}
	start := end - rows
	if start < 0 {
		start = 0
	}
	visible := m.events[start:end]
	out := strings.Join(visible, "\n")
	// pad to fill height
	missing := rows - len(visible)
	if missing > 0 {
		out += strings.Repeat("\n", missing)
	}
	return out
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
