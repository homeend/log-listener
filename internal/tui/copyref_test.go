package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
	"github.com/homeend/log-listener/internal/searchmatch"
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
	m.matcher, _ = searchmatch.Compile("banana", false)
	m.searchHit = 1
	ref := buildReference(m)
	if ref != "line:L1" {
		t.Fatalf("search hit ref: %q", ref)
	}
}

// TestBuildReferenceBlockRange: a single multi-row entry (one event with embedded
// newlines) must copy as "line:<id>", not "range:<id>..<id>".
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
	m.blockFocused = true
	ref := buildReference(m)
	if ref != "line:L0" {
		t.Fatalf("single-entry block ref = %q, want line:L0", ref)
	}
}

func TestBuildReferenceMultiEntryBlockIsRange(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	// lead L0, then a go-panic block: L1 ("panic: boom") and L2 ("goroutine 1 [running]:")
	// "goroutine " is a hasContSignature prefix, so L2 is a continuation of L1's block.
	seedIDs(m, "start", "panic: boom", "goroutine 1 [running]:")
	m.tailMode = false
	m.streamTop = 0
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}}) // focus the block at line 1
	m = m2.(*model)
	if ref := buildReference(m); ref != "range:L1..L2" {
		t.Fatalf("multi-entry block ref = %q, want range:L1..L2", ref)
	}
}

func TestBuildReferenceSingleEntryBlockIsLine(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	seedIDs(m, "start", "config:\n  k=v\n  j=w") // L0 lead, L1 multi-row entry
	m.tailMode = false
	m.streamTop = 0
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}}) // focus L1's block
	m = m2.(*model)
	if ref := buildReference(m); ref != "line:L1" {
		t.Fatalf("single-entry block ref = %q, want line:L1", ref)
	}
}

func TestBuildReferenceViewportWhenNotFocused(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	seedIDs(m, "start", "config:\n  k=v\n  j=w")
	m.tailMode = false
	m.streamTop = 1 // scrolled onto the block, NOT via nav
	if ref := buildReference(m); strings.HasPrefix(ref, "line:") {
		t.Fatalf("without explicit focus, must copy viewport range, got %q", ref)
	}
}
