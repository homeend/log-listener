package tui

import (
	"fmt"
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
	keyV      = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}}
	keyJ      = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}
	keySpace  = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}}
	keyEsc    = tea.KeyMsg{Type: tea.KeyEsc}
	keyY      = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Y'}}
	keyYlower = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}
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
	m.setStreamTopRow(0)
	m = key(m, keyV)
	if !m.visualMode || m.visualAnchorRow() != -1 {
		t.Fatalf("after v: visualMode=%v anchor=%d", m.visualMode, m.visualAnchorRow())
	}
	if m.tailMode {
		t.Error("v should leave tail mode")
	}
}

func TestVisualRefNormalisesOrder(t *testing.T) {
	m := newVisualModel(t, "a", "b", "c")
	m.setVisualAnchorRow(2)
	m.setVisualCursorRow(0)
	if got := buildVisualRef(m); got != "range:L0..L2" {
		t.Fatalf("buildVisualRef = %q, want range:L0..L2", got)
	}
}

func TestVisualEscCancels(t *testing.T) {
	m := newVisualModel(t, "a", "b", "c")
	m.tailMode = false
	m.setStreamTopRow(0)
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
	m.setStreamTopRow(0)
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

func TestVisualEnterClosesOverlays(t *testing.T) {
	m := newVisualModel(t, "a", "b", "c")
	m.tailMode = false
	m.setStreamTopRow(0)
	m.showFiles = true // an overlay is open
	m = key(m, keyV)
	if !m.visualMode {
		t.Fatal("v should enter visual mode")
	}
	if m.showFiles || m.showGroupsPanel || m.showRenderersPanel {
		t.Error("entering visual mode must close any open overlay")
	}
}

func TestVisualIndicesClampOnEviction(t *testing.T) {
	m := newModel(3) // cap 3 lines
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	seedVisual(m, "a", "b", "c") // lines 0,1,2
	m.tailMode = false
	m.setStreamTopRow(0)
	m = key(m, keyV)     // cursor 0
	m = key(m, keyJ)     // cursor 1
	m = key(m, keySpace) // anchor 1
	// Before any eviction the selection is anchored mid-stream on entry "b"
	// (row 1), so the post-eviction clamp below is a real "anchored entry
	// evicted → sentinel" transition — not a vacuous match of the getters'
	// 0/-1 fallbacks (which would also satisfy the final asserts).
	if got := m.visualCursorRow(); got != 1 {
		t.Fatalf("setup: visualCursor should anchor row 1 before eviction, got %d", got)
	}
	if got := m.visualAnchorRow(); got != 1 {
		t.Fatalf("setup: visualAnchor should anchor row 1 before eviction, got %d", got)
	}
	// Appending two more entries evicts the two oldest lines (cap 3), one at a
	// time. Each eviction drops dropLines=1.
	//   After "d": cursor 1→0, anchor 1→0 (not negative, no unset yet).
	//   After "e": cursor 0→-1→clamp(0), anchor 0→-1→unset(-1).
	// Without the fix the indices stay frozen at 1 (drift — they point at the
	// wrong line). With the fix they are dragged down correctly.
	seedVisual(m, "d", "e")
	// visualCursor must have been dragged: frozen at 1 means bug; dragged to 0
	// (clamped from -1 on the second eviction) means fix is working.
	if m.visualCursorRow() != 0 {
		t.Errorf("visualCursor not dragged on eviction: got %d, want 0", m.visualCursorRow())
	}
	// visualAnchor must have been unset: frozen at 1 means bug; dragged to -1
	// (scrolled off on the second eviction) means fix is working.
	if m.visualAnchorRow() != -1 {
		t.Errorf("visualAnchor not unset on eviction: got %d, want -1", m.visualAnchorRow())
	}
}

// The visual-mode footer hint must describe the unified flow (space anchors;
// y/Y copy), not the removed two-space behavior.
func TestVisualFooterDescribesUnifiedFlow(t *testing.T) {
	m := newVisualModel(t, "a", "b", "c")
	m.tailMode = false
	m.setStreamTopRow(0)
	m = key(m, keyV)
	foot := m.renderFooter()
	for _, want := range []string{"space anchor", "y ref", "Y text"} {
		if !strings.Contains(foot, want) {
			t.Fatalf("visual footer missing %q: %q", want, foot)
		}
	}
	if strings.Contains(foot, "set/copy") {
		t.Fatalf("visual footer still shows the removed two-space hint: %q", foot)
	}
}

func TestVisualTextSpan(t *testing.T) {
	m := newVisualModel(t, "a", "b", "c", "d")
	m.setVisualAnchorRow(1)
	m.setVisualCursorRow(2)
	got := buildVisualText(m)
	want := joinPlain(m.lines[1:3]) // rows 1,2
	if got != want {
		t.Fatalf("visual span text:\n got %q\nwant %q", got, want)
	}
}

func TestVisualTextNoAnchorIsCaretRow(t *testing.T) {
	m := newVisualModel(t, "a", "b", "c")
	m.setVisualAnchorRow(-1)
	m.setVisualCursorRow(1)
	got := buildVisualText(m)
	want := plainExportLine(m.lines[1])
	if got != want {
		t.Fatalf("no-anchor visual text = %q, want %q", got, want)
	}
}

func TestVisualSpaceOnlyAnchors(t *testing.T) {
	m := newVisualModel(t, "a", "b", "c")
	m.tailMode = false
	m.setStreamTopRow(0)
	m = key(m, keyV)
	m = key(m, keyJ)     // cursor → 1
	m = key(m, keySpace) // anchor = 1
	if !m.visualMode {
		t.Error("space must NOT exit visual mode")
	}
	if m.visualAnchorRow() != 1 {
		t.Fatalf("space should set anchor to 1, got %d", m.visualAnchorRow())
	}
	if m.flash != "" {
		t.Errorf("space must not copy/flash, got %q", m.flash)
	}
	m = key(m, keySpace) // re-anchor (cursor still 1) — stays in visual
	if m.visualAnchorRow() != 1 || !m.visualMode {
		t.Errorf("second space should re-anchor and stay in visual (anchor=%d visual=%v)", m.visualAnchorRow(), m.visualMode)
	}
}

func TestVisualYCopiesRangeAndExits(t *testing.T) {
	m := newVisualModel(t, "a", "b", "c", "d")
	m.tailMode = false
	m.setStreamTopRow(0)
	m = key(m, keyV)      // cursor at row 0
	m = key(m, keyJ)      // cursor → 1
	m = key(m, keySpace)  // anchor = 1
	if m.visualAnchorRow() != 1 {
		t.Fatalf("anchor should be 1, got %d", m.visualAnchorRow())
	}
	m = key(m, keyJ)      // cursor → 2
	m = key(m, keyYlower) // copy range L1..L2, exit
	if m.visualMode {
		t.Error("y should exit visual mode")
	}
	if m.flash != "copied range:L1..L2" {
		t.Fatalf("flash = %q, want copied range:L1..L2", m.flash)
	}
}

func TestVisualCapitalYCopiesTextAndExits(t *testing.T) {
	m := newVisualModel(t, "a", "b", "c", "d")
	m.tailMode = false
	m.setStreamTopRow(0)
	m = key(m, keyV)     // cursor at row 0
	m = key(m, keyJ)     // row 1
	m = key(m, keySpace) // anchor = 1
	m = key(m, keyJ)     // cursor → row 2
	m = key(m, keyY)     // copy text rows 1..2, exit
	if m.visualMode {
		t.Error("Y should exit visual mode")
	}
	if m.flash != "copied 2 lines" {
		t.Fatalf("flash = %q, want \"copied 2 lines\"", m.flash)
	}
}

func TestVisualNoAnchorYCopiesCaretLine(t *testing.T) {
	m := newVisualModel(t, "a", "b", "c")
	m.tailMode = false
	m.setStreamTopRow(0)
	m = key(m, keyV)      // cursor at row 0 (L0), no anchor
	m = key(m, keyJ)      // cursor → row 1 (L1)
	m = key(m, keyYlower) // copy caret line, exit
	if m.visualMode {
		t.Error("y should exit visual mode")
	}
	if m.flash != "copied line:L1" {
		t.Fatalf("flash = %q, want copied line:L1", m.flash)
	}
}

func TestNormalCapitalYCopiesViewportText(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 6})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	seedIDs(m, "a", "b", "c")
	m.tailMode = false
	m.setStreamTopRow(0)
	want := fmt.Sprintf("copied %d lines", len(m.snapshotViewport()))
	m = key(m, keyY)
	if m.flash != want {
		t.Fatalf("flash = %q, want %q", m.flash, want)
	}
}
