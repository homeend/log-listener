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

// scrollModel seeds n single-row events, sizes the window, and returns a model
// in browse mode (tail off) at streamTop=0.
func scrollModel(t *testing.T, n int) *model {
	t.Helper()
	vals := make([]string, n)
	for i := range vals {
		vals[i] = string(rune('a' + i))
	}
	m := seedSearch(t, vals...)
	m.reconcile()
	m.tailMode = false
	m.setStreamTopRow(0)
	return m
}

func TestScrollByUpClampsAtZero(t *testing.T) {
	m := scrollModel(t, 5)
	m.setStreamTopRow(1)
	m.scrollBy(-3) // would go to -2
	if m.streamTopRow() != 0 {
		t.Fatalf("streamTop = %d, want 0 (clamped)", m.streamTopRow())
	}
}

func TestScrollByUpLeavesTailMode(t *testing.T) {
	m := scrollModel(t, 5)
	m.tailMode = true
	m.setStreamTopRow(4)
	m.scrollBy(-1)
	if m.tailMode {
		t.Fatal("scrollBy(up) must leave tail mode (unstickFromTail)")
	}
}

func TestScrollByDownIsNoOpInTailMode(t *testing.T) {
	m := scrollModel(t, 5)
	m.tailMode = true
	before := m.streamTopRow()
	m.scrollBy(2)
	if m.streamTopRow() != before {
		t.Fatalf("scrollBy(down) in tail mode moved streamTop %d->%d, want no-op", before, m.streamTopRow())
	}
	if !m.tailMode {
		t.Fatal("scrollBy(down) in tail mode must stay in tail mode")
	}
}

func TestScrollByDownMovesWhenBrowsing(t *testing.T) {
	m := scrollModel(t, 20)
	m.tailMode = false
	m.setStreamTopRow(0)
	m.scrollBy(3)
	if m.streamTopRow() != 3 {
		t.Fatalf("streamTop = %d, want 3", m.streamTopRow())
	}
}

func TestScrollByZeroIsNoOp(t *testing.T) {
	m := scrollModel(t, 5)
	m.setStreamTopRow(2)
	m.scrollBy(0)
	if m.streamTopRow() != 2 {
		t.Fatalf("streamTop = %d, want 2 (zero delta no-op)", m.streamTopRow())
	}
}

func TestPanByNoopWhenWrapping(t *testing.T) {
	m := newModel(100)
	m.wordWrap = true
	m.panBy(20)
	if m.horizScroll != 0 {
		t.Fatalf("pan should be a no-op while wrapping, got %d", m.horizScroll)
	}
}
