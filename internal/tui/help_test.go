package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/keymap"
)

func TestHelpOpensAndClosesOverlays(t *testing.T) {
	m := newModel(100)
	m.showFiles = true
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	if !m.showHelp {
		t.Fatal("? did not open help")
	}
	if m.showFiles {
		t.Fatal("opening help should close the files overlay")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.showHelp {
		t.Fatal("esc did not close help")
	}
}

func TestHelpModalFilterAndScroll(t *testing.T) {
	m := newModel(100)
	m.showHelp = true
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'u'}})
	if m.helpQuery != "qu" {
		t.Fatalf("helpQuery = %q, want %q", m.helpQuery, "qu")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if m.helpQuery != "q" {
		t.Fatalf("helpQuery after backspace = %q", m.helpQuery)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	if m.showHelp {
		t.Fatal("? did not close help")
	}
}

func TestHelpRowsReflectResolvedKeys(t *testing.T) {
	m := newModel(100)
	rows := m.helpRows()
	if len(rows) != len(keymap.AllActions) {
		t.Fatalf("want %d rows, got %d", len(keymap.AllActions), len(rows))
	}
	var quit helpRow
	for _, r := range rows {
		if r.title == "Quit" {
			quit = r
		}
	}
	if quit.keys != m.resolvedKM().Display(keymap.ActionQuit) {
		t.Fatalf("quit keys %q != resolved Display %q", quit.keys, m.resolvedKM().Display(keymap.ActionQuit))
	}
}

func TestHelpRowsFilter(t *testing.T) {
	m := newModel(100)
	m.helpQuery = "quit"
	rows := m.helpRows()
	if len(rows) == 0 {
		t.Fatal("filter 'quit' matched nothing")
	}
	for _, r := range rows {
		hay := strings.ToLower(r.keys + " " + r.title + " " + r.desc)
		if !strings.Contains(hay, "quit") {
			t.Fatalf("row %q does not match filter", r.title)
		}
	}
}

func TestRenderHelpShowsFilteredTitles(t *testing.T) {
	m := newModel(100)
	m.width = 80
	m.height = 24
	m.showHelp = true
	m.helpQuery = "quit"
	out := stripANSI(m.renderHelp(10))
	if !strings.Contains(out, "Quit") {
		t.Fatalf("render missing Quit row: %q", out)
	}
	if strings.Contains(out, "Page up") {
		t.Fatalf("render should be filtered to 'quit', leaked: %q", out)
	}
}
