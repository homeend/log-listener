package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
)

// newReconcileModel builds a sized model (owned buffer) for reconcile tests.
func newReconcileModel(t *testing.T, cap int) *model {
	t.Helper()
	m := newModel(cap)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	return m
}

func textEv(s string) render.Event {
	return render.Event{Group: "g", File: "/x.log",
		Rendered: []render.Part{{Type: "text", Value: s}}}
}

func TestReconcileBuildsLinesFromBuffer(t *testing.T) {
	m := newReconcileModel(t, 100)
	for _, s := range []string{"a", "b", "c"} {
		m.buf.Append(textEv(s))
	}
	m.reconcile()
	if len(m.lines) != 3 {
		t.Fatalf("m.lines = %d, want 3", len(m.lines))
	}
}

func TestReconcileCoalescesWhenGenUnchanged(t *testing.T) {
	m := newReconcileModel(t, 100)
	m.buf.Append(textEv("a"))
	m.reconcile()
	before := len(m.lines)
	// Second reconcile with no buffer change must be a no-op (gen unchanged).
	m.reconcile()
	if len(m.lines) != before {
		t.Fatalf("coalesced reconcile changed m.lines: %d != %d", len(m.lines), before)
	}
}

func TestReconcileReusesCacheForExistingIDs(t *testing.T) {
	m := newReconcileModel(t, 100)
	m.buf.Append(textEv("a"))
	m.reconcile()
	cached := m.displayCache["L0"]
	if len(cached) == 0 {
		t.Fatal("L0 not cached after first reconcile")
	}
	m.buf.Append(textEv("b"))
	m.reconcile()
	// The existing entry's cached rows must be reused, not rebuilt.
	if &cached[0] != &m.displayCache["L0"][0] {
		t.Fatal("existing entry's cached display rows were rebuilt (should be reused)")
	}
}

func TestReconcileEvictsCacheForDroppedIDs(t *testing.T) {
	m := newReconcileModel(t, 2) // 2-row display window
	for _, s := range []string{"a", "b", "c"} {
		m.buf.Append(textEv(s))
	}
	m.reconcile()
	// Window holds the last 2 single-row entries; the oldest (L0) is outside it.
	if _, ok := m.displayCache["L0"]; ok {
		t.Fatal("L0 should have been pruned from the display cache (outside window)")
	}
	if len(m.lines) != 2 {
		t.Fatalf("m.lines = %d, want 2 (window cap)", len(m.lines))
	}
}

func TestReconcileEvictionDragsViewState(t *testing.T) {
	m := newReconcileModel(t, 3) // 3-row window
	m.tailMode = false
	m.streamTop = 0
	for _, s := range []string{"a", "b", "c"} {
		m.buf.Append(textEv(s))
	}
	m.reconcile()
	m.streamTop = 2
	m.buf.Append(textEv("d")) // window slides by 1 row (oldest evicted)
	m.reconcile()
	if m.streamTop != 1 {
		t.Fatalf("streamTop = %d, want 1 (dragged by 1 evicted row)", m.streamTop)
	}
}
