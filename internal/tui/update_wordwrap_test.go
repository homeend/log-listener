package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestToggleWordWrapKeyFlipsAndResetsPan(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = m2.(*model)
	m.horizScroll = 30
	m = key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	if !m.wordWrap {
		t.Fatal("w should turn word wrap on")
	}
	if m.horizScroll != 0 {
		t.Fatalf("enabling wrap should reset horizScroll, got %d", m.horizScroll)
	}
	m = key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	if m.wordWrap {
		t.Fatal("w should turn word wrap off again")
	}
}
