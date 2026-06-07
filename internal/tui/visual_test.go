package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
)

func seedVisual(m *model, vals ...string) {
	for i, v := range vals {
		m.appendEvent(render.Event{ID: "L" + itoa36(i), Group: "g", File: "/a.log",
			Rendered: []render.Part{{Type: "text", Value: v}}})
	}
}

func key(m *model, k tea.KeyMsg) *model {
	m2, _ := m.Update(k)
	return m2.(*model)
}

var (
	keyV     = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}}
	keyJ     = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}
	keySpace = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}
	keyEsc   = tea.KeyMsg{Type: tea.KeyEsc}
)

func newVisualModel(t *testing.T, vals ...string) *model {
	t.Helper()
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	seedVisual(m, vals...)
	return m
}

func TestVisualEnter(t *testing.T) {
	m := newVisualModel(t, "a", "b", "c")
	m.tailMode = false
	m.streamTop = 0
	m = key(m, keyV)
	if !m.visualMode || m.visualAnchor != -1 {
		t.Fatalf("after v: visualMode=%v anchor=%d", m.visualMode, m.visualAnchor)
	}
	if m.tailMode {
		t.Error("v should leave tail mode")
	}
}

func TestVisualTwoSpaceCopiesRange(t *testing.T) {
	m := newVisualModel(t, "a", "b", "c", "d")
	m.tailMode = false
	m.streamTop = 0
	m = key(m, keyV)     // enter, cursor at line 0 (L0)
	m = key(m, keyJ)     // cursor → line 1 (L1)
	m = key(m, keySpace) // anchor = L1
	if m.visualAnchor != 1 {
		t.Fatalf("anchor should be 1, got %d", m.visualAnchor)
	}
	m = key(m, keyJ)     // cursor → line 2 (L2)
	m = key(m, keySpace) // copy range L1..L2, exit
	if m.visualMode {
		t.Error("second space should exit visual mode")
	}
	if m.flash != "copied range:L1..L2" {
		t.Fatalf("flash = %q, want copied range:L1..L2", m.flash)
	}
}

func TestVisualRefNormalisesOrder(t *testing.T) {
	m := newVisualModel(t, "a", "b", "c")
	m.visualAnchor = 2
	m.visualCursor = 0
	if got := buildVisualRef(m); got != "range:L0..L2" {
		t.Fatalf("buildVisualRef = %q, want range:L0..L2", got)
	}
}

func TestVisualEscCancels(t *testing.T) {
	m := newVisualModel(t, "a", "b", "c")
	m.tailMode = false
	m.streamTop = 0
	m = key(m, keyV)
	m = key(m, keyJ)
	m = key(m, keySpace) // anchor set
	m = key(m, keyEsc)   // cancel
	if m.visualMode {
		t.Error("esc should exit visual mode")
	}
	if m.flash != "" {
		t.Errorf("esc must not copy/flash, got %q", m.flash)
	}
}

func TestVisualBarRendersCursorAndSelection(t *testing.T) {
	m := newVisualModel(t, "a", "b", "c", "d")
	m.tailMode = false
	m.streamTop = 0
	m = key(m, keyV)     // cursor on line 0
	m = key(m, keyJ)     // cursor → 1
	m = key(m, keySpace) // anchor = 1
	m = key(m, keyJ)     // cursor → 2 (selection 1..2)
	view := m.renderStream(m.contentHeight())
	if !strings.Contains(view, "▶") {
		t.Fatalf("expected the visual cursor caret ▶:\n%s", view)
	}
	if !strings.Contains(view, "┃") {
		t.Fatalf("expected a selection bar ┃:\n%s", view)
	}
	for _, ln := range strings.Split(view, "\n") {
		if w := dispWidth(ln); w != m.width {
			t.Errorf("row should be exactly width %d, got %d: %q", m.width, w, ln)
		}
	}
}
