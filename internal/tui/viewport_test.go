package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
)

func TestPublishViewportReportsVisibleRange(t *testing.T) {
	var gotFrom, gotTo string
	called := false
	m := newModel(100)
	m.setViewport = func(from, to string) { gotFrom, gotTo, called = from, to, true }
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	for i, v := range []string{"a", "b", "c"} {
		m.appendEvent(render.Event{ID: "L" + itoa36(i), Group: "g", File: "/a.log",
			Rendered: []render.Part{{Type: "text", Value: v}}})
	}
	_ = m.renderStream(m.contentHeight())
	if !called {
		t.Fatal("renderStream should publish the viewport")
	}
	if gotFrom != "L0" || gotTo != "L2" {
		t.Fatalf("viewport published %q..%q, want L0..L2", gotFrom, gotTo)
	}
}

func TestPublishViewportNoopWhenNilCallback(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	m.appendEvent(render.Event{ID: "L0", Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "x"}}})
	_ = m.renderStream(m.contentHeight())
}

func TestPublishViewportEmptyBufferStillPublishes(t *testing.T) {
	var called bool
	var gotFrom, gotTo string
	m := newModel(100)
	m.setViewport = func(from, to string) { called = true; gotFrom, gotTo = from, to }
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	// no events → empty buffer
	_ = m.renderStream(m.contentHeight())
	if !called || gotFrom != "" || gotTo != "" {
		t.Fatalf("empty buffer must publish empty/attached: called=%v from=%q to=%q", called, gotFrom, gotTo)
	}
}
