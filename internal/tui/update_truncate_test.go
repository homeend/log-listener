package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestToggleFilenameTruncKey(t *testing.T) {
	m := newModel(100)
	if m.truncateFiles {
		t.Fatal("should start off")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if !m.truncateFiles {
		t.Fatal("f did not toggle truncation on")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	if m.truncateFiles {
		t.Fatal("f did not toggle truncation back off")
	}
}
