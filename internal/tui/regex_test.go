package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
)

func TestSearchEscDoesNotLeakRegexMode(t *testing.T) {
	m := seedSearch(t, "x")
	m.searchInput = true
	m = key(m, tea.KeyMsg{Type: tea.KeyCtrlR}) // enable regex
	if !m.searchRegex {
		t.Fatal("Ctrl+R should enable regex")
	}
	m = key(m, tea.KeyMsg{Type: tea.KeyEsc}) // cancel
	if m.searchRegex {
		t.Fatal("Esc-cancel must reset searchRegex so the next search starts in substring mode")
	}
}

func seedSearch(t *testing.T, vals ...string) *model {
	t.Helper()
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	for _, v := range vals {
		m.appendEvent(render.Event{Group: "g", File: "/a.log",
			Rendered: []render.Part{{Type: "text", Value: v}}})
	}
	return m
}

func TestSearchRegexToggleMatches(t *testing.T) {
	m := seedSearch(t, "user-42", "user-7", "admin")
	m.searchInput = true
	m.searchQuery = "user-[0-9]+"
	m = key(m, tea.KeyMsg{Type: tea.KeyCtrlR}) // toggle regex on
	if !m.searchRegex {
		t.Fatal("Ctrl+R should enable regex")
	}
	m = key(m, tea.KeyMsg{Type: tea.KeyEnter}) // commit
	if m.matcher == nil || !m.matcher.Match("user-42") || m.matcher.Match("admin") {
		t.Fatal("regex matcher did not compile/behave as expected")
	}
	if m.searchInput {
		t.Fatal("valid regex commit should close the input box")
	}
}

func TestSearchInvalidRegexStaysInInputWithFlash(t *testing.T) {
	m := seedSearch(t, "x")
	m.searchInput = true
	m.searchQuery = "a("
	m.searchRegex = true
	m = key(m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.searchInput {
		t.Fatal("invalid regex must keep the input box open")
	}
	if m.matcher != nil {
		t.Fatal("invalid regex must not set a matcher")
	}
	if !strings.Contains(m.flash, "invalid") {
		t.Fatalf("expected invalid-regex flash, got %q", m.flash)
	}
}
