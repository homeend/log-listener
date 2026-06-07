package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
)

func seedIDs(m *model, vals ...string) {
	for i, v := range vals {
		m.appendEvent(render.Event{ID: "L" + itoa36(i), Group: "g", File: "/a.log",
			Rendered: []render.Part{{Type: "text", Value: v}}})
	}
}

func TestBuildReferenceViewportRange(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 6})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	seedIDs(m, "a", "b", "c", "d", "e", "f")
	m.tailMode = false
	m.streamTop = 0
	ref := buildReference(m)
	if len(ref) < 6 || ref[:6] != "range:" {
		t.Fatalf("viewport ref should be a range: %q", ref)
	}
}

func TestBuildReferenceSearchHitLine(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	seedIDs(m, "apple", "banana", "cherry")
	m.searchTerm = "banana"
	m.searchHit = 1
	ref := buildReference(m)
	if ref != "line:L1" {
		t.Fatalf("search hit ref: %q", ref)
	}
}

func TestBuildReferenceBlockRange(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	m.appendEvent(render.Event{ID: "L0", Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "config:\n  k=v\n  j=w"}}})
	m.tailMode = false
	m.streamTop = 0
	ref := buildReference(m)
	if ref != "range:L0..L0" {
		t.Fatalf("single-entry block ref: %q", ref)
	}
}
