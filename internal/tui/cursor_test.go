package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
)

func TestCursorFollowsSearchHit(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	for _, v := range []string{"apple", "banana", "cherry banana", "date"} {
		m.appendEvent(render.Event{Group: "g", File: "/a.log",
			Rendered: []render.Part{{Type: "text", Value: v}}})
	}
	m = typeQuery(t, m, "banana")
	// typeQuery commits and lands on the last hit in tail mode.
	if m.searchHit < 0 {
		t.Fatalf("setup: searchHit should be >= 0 after search, got %d", m.searchHit)
	}
	if m.cursorIndex() != m.searchHit {
		t.Errorf("cursor %d should follow searchHit %d", m.cursorIndex(), m.searchHit)
	}
}
