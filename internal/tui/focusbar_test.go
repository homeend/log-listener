package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
)

func seedFocus(m *model, vals ...string) {
	for _, v := range vals {
		m.appendEvent(render.Event{Group: "g", File: "/a.log",
			Rendered: []render.Part{{Type: "text", Value: v}}})
	}
}

func TestFocusBarOnBlockOnly(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	seedFocus(m, "single one", "block head:\n  cont a", "single two")
	m.tailMode = false
	m.streamTop = 1 // cursor in the block
	m.ensureBlocks()
	if _, ok := m.focusBar(1); !ok {
		t.Error("block head (1) should be focused")
	}
	if _, ok := m.focusBar(2); !ok {
		t.Error("block cont (2) should be focused")
	}
	if _, ok := m.focusBar(0); ok {
		t.Error("single line 0 should NOT be focused")
	}
	if _, ok := m.focusBar(3); ok {
		t.Error("single line 3 should NOT be focused")
	}
}

func TestFocusBarGoneWhenCursorOffBlock(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	seedFocus(m, "block head:\n  cont a", "single")
	m.tailMode = false
	m.streamTop = 2 // the trailing single line (block is lines 0-1)
	m.ensureBlocks()
	if _, ok := m.focusBar(0); ok {
		t.Error("cursor off the block → no focus bar")
	}
}

func TestFocusBarSuppressedInTailAndVisual(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	seedFocus(m, "block head:\n  cont a")
	m.ensureBlocks()
	m.tailMode = true
	if _, ok := m.focusBar(0); ok {
		t.Error("tail mode → no focus bar")
	}
	m.tailMode = false
	m.streamTop = 0
	m.visualMode = true
	if _, ok := m.focusBar(0); ok {
		t.Error("visual mode → no focus bar")
	}
}

func TestFocusBarWidthSafe(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 24, Height: 6})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	seedFocus(m, "panic: "+strings.Repeat("X", 80), "  at frame")
	m.tailMode = false
	m.streamTop = 0
	view := m.renderStream(m.contentHeight())
	if !strings.Contains(view, "│") || !strings.Contains(view, "▌") {
		t.Fatalf("expected both focus and exception bars:\n%s", view)
	}
	for _, ln := range strings.Split(view, "\n") {
		if w := dispWidth(ln); w != m.width {
			t.Errorf("row should be exactly width %d, got %d: %q", m.width, w, ln)
		}
	}
}
