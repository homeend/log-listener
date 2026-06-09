package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
)

func TestRenderVisibleRowIncludesPrefixWidth(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "hello"}}})
	styled, visW := m.renderVisibleRow(0)
	// prefix "[g] a.log: " (11 cols) + body "hello" (5) = 16.
	if visW != 16 {
		t.Fatalf("visW = %d, want 16", visW)
	}
	if got := dispWidth(stripANSI(styled)); got != 16 {
		t.Fatalf("styled width = %d, want 16", got)
	}
}
