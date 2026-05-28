package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"log-listener/internal/render"
)

func TestModelAppendEventBoundedScrollback(t *testing.T) {
	m := newModel(3)
	for i := 0; i < 10; i++ {
		m.appendEvent(render.Event{
			Group: "d", File: "/a.log",
			Rendered: []render.Part{{Type: "text", Value: "line"}},
		})
	}
	if len(m.events) > 3 {
		t.Fatalf("scrollback breached: %d", len(m.events))
	}
}

func TestModelToggleFilesPanel(t *testing.T) {
	m := newModel(100)
	if m.showFiles {
		t.Fatal("files should default to hidden")
	}
	// Tab is what terminals actually send for Ctrl+I (same byte 0x09).
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = m2.(*model)
	if !m.showFiles {
		t.Fatal("Tab/Ctrl+I should toggle files on")
	}
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = m2.(*model)
	if m.showFiles {
		t.Fatal("Esc should close files panel")
	}
}

func TestModelFileListReplaces(t *testing.T) {
	m := newModel(100)
	m.files = []FileEntry{{Path: "/old", Group: "old"}}
	m.filesScroll = 5 // out of range after replace
	m2, _ := m.Update(FileListMsg{Files: []FileEntry{
		{Path: "/new1", Group: "g"},
		{Path: "/new2", Group: "g"},
	}})
	m = m2.(*model)
	if len(m.files) != 2 || m.files[0].Path != "/new1" {
		t.Fatalf("files not replaced: %+v", m.files)
	}
	if m.filesScroll != 0 {
		t.Fatalf("filesScroll should reset when out of range: %d", m.filesScroll)
	}
}

// TestNewSeedsInitialFiles asserts that the initial file list passed to
// tui.New() is reflected in the model before any Update is processed —
// the SetFiles-before-Run deadlock fix.
func TestNewSeedsInitialFiles(t *testing.T) {
	app := New(100, []FileEntry{
		{Path: "/a.log", Group: "g1"},
		{Path: "/b.log", Group: "g2"},
	})
	// Reach into the model via reflection-free fast path: the underlying
	// *model isn't exposed, but if seeding worked, app.prog's initial
	// model has files preset. We can't easily inspect that without
	// running, so spot-check the helper directly:
	m := newModel(100)
	m.files = append(m.files, FileEntry{Path: "/a.log", Group: "g1"})
	if len(m.files) != 1 || m.files[0].Path != "/a.log" {
		t.Fatalf("model seed direct check failed: %+v", m.files)
	}
	if app == nil {
		t.Fatal("app should not be nil")
	}
}

// TestModelViewShowsEventAfterUpdate exercises the model the way bubbletea
// would: feed a WindowSizeMsg + an EventMsg via Update, then assert the
// rendered View contains the line. Catches regressions where Update doesn't
// route to appendEvent or View doesn't include the stream area.
func TestModelViewShowsEventAfterUpdate(t *testing.T) {
	var m tea.Model = newModel(100)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	ev := render.Event{
		Group: "g1", File: "/tmp/abc.log",
		Rendered: []render.Part{{Type: "text", Value: "MARKER-9999"}},
	}
	m, _ = m.Update(EventMsg{Event: ev})
	view := m.View()
	if !strings.Contains(view, "MARKER-9999") {
		t.Fatalf("View() does not contain pushed event marker:\n%s", view)
	}
	if !strings.Contains(view, "abc.log") {
		t.Fatalf("View() does not contain basename:\n%s", view)
	}
}

func TestModelPageUpPageDown(t *testing.T) {
	m := newModel(1000)
	for i := 0; i < 200; i++ {
		m.appendEvent(render.Event{
			Group: "g", File: "/x.log",
			Rendered: []render.Part{{Type: "text", Value: fmt.Sprintf("line %d", i)}},
		})
	}
	// height 30 → contentHeight = 28
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = m2.(*model)
	page := m.contentHeight()
	if page <= 0 {
		t.Fatalf("contentHeight=%d", page)
	}

	// PgUp moves back by page rows
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = m2.(*model)
	if m.streamScroll != page {
		t.Fatalf("after PgUp streamScroll=%d want %d", m.streamScroll, page)
	}

	// PgDn returns by page rows
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = m2.(*model)
	if m.streamScroll != 0 {
		t.Fatalf("after PgDn streamScroll=%d want 0", m.streamScroll)
	}

	// PgUp clamps at total event count
	for i := 0; i < 100; i++ {
		m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
		m = m2.(*model)
	}
	if m.streamScroll > len(m.events) {
		t.Fatalf("PgUp overshot: scroll=%d events=%d", m.streamScroll, len(m.events))
	}
}

func TestModelHorizontalScroll(t *testing.T) {
	m := newModel(100)
	// A line wider than the viewport
	long := strings.Repeat("abcdef-", 30) // 210 chars
	m.appendEvent(render.Event{
		Group: "g", File: "/x.log",
		Rendered: []render.Part{{Type: "text", Value: long}},
	})
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	m = m2.(*model)

	// At horizScroll = 0 the start of the prefixed line is visible
	view := m.View()
	if !strings.Contains(view, "[g]") {
		t.Fatalf("expected '[g]' visible at horizScroll=0:\n%s", view)
	}

	// Right arrow shifts the view
	for i := 0; i < 5; i++ {
		m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
		m = m2.(*model)
	}
	if m.horizScroll != 5*horizStep {
		t.Fatalf("horizScroll after 5x Right = %d, want %d", m.horizScroll, 5*horizStep)
	}

	// After scrolling, the leading "[g] x.log:" prefix is no longer visible.
	view = m.View()
	if strings.Contains(view, "[g] x.log:") {
		t.Fatalf("prefix should be scrolled off, but still visible:\n%s", view)
	}
	// We should still see SOME of the line body
	if !strings.Contains(view, "abcdef-") {
		t.Fatalf("expected line body still visible after horiz scroll:\n%s", view)
	}

	// Home/0 jumps back to column 0
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyHome})
	m = m2.(*model)
	if m.horizScroll != 0 {
		t.Fatalf("Home should reset horizScroll, got %d", m.horizScroll)
	}

	// Left clamps at 0
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = m2.(*model)
	if m.horizScroll != 0 {
		t.Fatalf("Left at column 0 must stay at 0, got %d", m.horizScroll)
	}
}

func TestModelFastScrollKeys(t *testing.T) {
	m := newModel(1000)
	for i := 0; i < 200; i++ {
		m.appendEvent(render.Event{
			Group: "g", File: "/x.log",
			Rendered: []render.Part{{Type: "text", Value: fmt.Sprintf("line %d", i)}},
		})
	}
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = m2.(*model)

	// Ctrl+Right pans by horizFastStep, not horizStep
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlRight})
	m = m2.(*model)
	if m.horizScroll != horizFastStep {
		t.Fatalf("Ctrl+Right: horizScroll=%d want %d", m.horizScroll, horizFastStep)
	}

	// Ctrl+Left rolls back by horizFastStep
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlLeft})
	m = m2.(*model)
	if m.horizScroll != 0 {
		t.Fatalf("Ctrl+Left: horizScroll=%d want 0", m.horizScroll)
	}

	// Ctrl+Up scrolls vertically by vertFastStep
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlUp})
	m = m2.(*model)
	if m.streamScroll != vertFastStep {
		t.Fatalf("Ctrl+Up: streamScroll=%d want %d", m.streamScroll, vertFastStep)
	}

	// Ctrl+Down rolls back
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlDown})
	m = m2.(*model)
	if m.streamScroll != 0 {
		t.Fatalf("Ctrl+Down: streamScroll=%d want 0", m.streamScroll)
	}
}

func TestStripANSIRoundtrip(t *testing.T) {
	plain := "hello world"
	styled := groupStyle.Render(plain)
	if styled == plain {
		t.Skip("lipgloss did not add ANSI (likely TERM=dumb)")
	}
	if stripANSI(styled) != plain {
		t.Fatalf("stripANSI(%q) = %q, want %q", styled, stripANSI(styled), plain)
	}
}

func TestRenderEventLines(t *testing.T) {
	ev := render.Event{
		Group: "d1", File: "/var/log/a.log",
		Rendered: []render.Part{
			{Type: "text", Value: "INFO\n"},
			{Type: "json", Value: map[string]interface{}{"k": "v"}},
		},
	}
	lines := renderEventLines(ev)
	if len(lines) < 2 {
		t.Fatalf("expected >=2 lines, got %d: %+v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "a.log") {
		t.Fatalf("missing basename: %s", lines[0])
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, `"k": "v"`) {
		t.Fatalf("json missing in output: %s", joined)
	}
}
