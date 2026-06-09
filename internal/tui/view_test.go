package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
)

func TestVisibleRowCostWrapOffIsOne(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "short"}}})
	if c := m.visibleRowCost(0); c != 1 {
		t.Fatalf("wrap off cost = %d, want 1", c)
	}
}

func TestCollectVisibleHeightAwareWhenWrapping(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	// Each line is ~60 visible cols of body; prefix "[g] a.log: " = 11 => ~71.
	long := "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
	for i := 0; i < 10; i++ {
		m.appendEvent(render.Event{Group: "g", File: "/a.log",
			Rendered: []render.Part{{Type: "text", Value: long}}})
	}
	m.wordWrap = true
	m.width = 40 // 71 cols / 40 => 2 rows per line
	// Ask for 6 terminal rows: 2 rows/line => 3 lines fill it.
	got := m.collectVisible(6)
	if len(got) != 3 {
		t.Fatalf("height-aware collect returned %d lines, want 3", len(got))
	}
}

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
