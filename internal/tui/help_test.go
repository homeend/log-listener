package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
