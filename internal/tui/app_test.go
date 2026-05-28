package tui

import (
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
