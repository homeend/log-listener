package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/keymap"
	"github.com/homeend/log-listener/internal/render"
	"github.com/homeend/log-listener/internal/searchmatch"
)

func newFooterModel(t *testing.T) *model {
	t.Helper()
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 12})
	return m2.(*model)
}

func TestCompactStatusTailMode(t *testing.T) {
	m := newFooterModel(t)
	m.lines = make([]displayLine, 7)
	m.tailMode = true
	got := m.compactStatus()
	if !strings.Contains(got, "ev 7") || !strings.Contains(got, "tail") {
		t.Fatalf("compactStatus tail = %q, want ev 7 + tail", got)
	}
}

func TestCompactStatusBrowseWithSearch(t *testing.T) {
	// Seed via appendEvent+reconcile so the anchor system (window/displayCache)
	// is populated; raw m.lines= assignment can't drive anchor-based streamTopRow.
	m := newFooterModel(t)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	for i := 0; i < 9; i++ {
		m.appendEvent(render.Event{Group: "g", File: "/a.log",
			Rendered: []render.Part{{Type: "text", Value: "line"}}})
	}
	m.reconcile()
	if got := len(m.lines); got != 9 {
		t.Fatalf("seeded %d lines, want 9", got)
	}
	m.tailMode = false
	m.setStreamTopRow(3)
	if got := m.streamTopRow(); got != 3 {
		t.Fatalf("setStreamTopRow(3) resolved to %d, want 3", got)
	}
	m.matcher, _ = compileTestMatcher(t, "err")
	m.searchQuery = "err"
	got := m.compactStatus()
	if !strings.Contains(got, "ev 9") || !strings.Contains(got, "@3/9") || !strings.Contains(got, "/err") {
		t.Fatalf("compactStatus browse = %q, want ev 9 + @3/9 + /err", got)
	}
}

func compileTestMatcher(t *testing.T, q string) (*searchmatch.Matcher, error) {
	t.Helper()
	return searchmatch.Compile(q, false)
}

func keymapDefaultLinux() *keymap.Keymap { return keymap.Default("linux") }

func keymapResolveFilterF() (*keymap.Keymap, error) {
	return keymap.Resolve("linux", map[string][]string{"filter": {"F"}}, nil)
}
