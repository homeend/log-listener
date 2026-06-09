package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
)

// seedEnabledDisabledRuns seeds 10 enabled (group a) + 20 disabled (group b) +
// 10 enabled (group a) display lines: indices 0-9 enabled, 10-29 disabled,
// 30-39 enabled. Browse-mode up-scroll must step over the disabled run in one
// move, not crawl raw indices through it (which renders identically = the
// "counter moves, screen frozen, then jumps" bug the user reported).
func seedEnabledDisabledRuns(t *testing.T) *model {
	t.Helper()
	m := newModel(1000)
	m.groupOrder = []string{"a", "b"}
	m.groupEnabled["a"] = true
	m.groupEnabled["b"] = false
	add := func(g, tag string, n int) {
		for i := 0; i < n; i++ {
			m.appendEvent(render.Event{Group: g, File: "/x.log",
				Rendered: []render.Part{{Type: "text", Value: fmt.Sprintf("%s-%s-%d", g, tag, i)}}})
		}
	}
	add("a", "top", 10)
	add("b", "mid", 20)
	add("a", "bot", 10)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 6})
	m = m2.(*model)
	m.reconcile()
	return m
}

func TestScrollUpStepsOverDisabledRun(t *testing.T) {
	m := seedEnabledDisabledRuns(t)
	m.tailMode = false
	m.setStreamTopRow(30) // first enabled line below the disabled run
	if got := m.streamTopRow(); got != 30 {
		t.Fatalf("seed streamTop=30 resolved to %d", got)
	}
	// One up-press must land on the next ENABLED line above (index 9), skipping
	// the 20-line disabled run in a single step — not stall on disabled index 29.
	m.scrollBy(-1)
	if got := m.streamTopRow(); got != 9 {
		t.Fatalf("scrollBy(-1) should step to next enabled line (9), got %d (frozen-scroll bug)", got)
	}
	// And the view must actually move: the top region's line becomes visible.
	if view := m.View(); !strings.Contains(view, "a-top-9") {
		t.Fatalf("after up-scroll, line 9 should be visible:\n%s", view)
	}
}

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

func seedWrapped(t *testing.T, n int) *model {
	t.Helper()
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12}) // contentHeight 10
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	long := "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz" // 60 cols
	for i := 0; i < n; i++ {
		m.appendEvent(render.Event{Group: "g", File: "/a.log",
			Rendered: []render.Part{{Type: "text", Value: long}}})
	}
	m.wordWrap = true
	m.width = 40 // prefix(11)+body(60)=71 cols => 2 rows per line
	return m
}

func TestVstepWrapOffIsIdentity(t *testing.T) {
	m := newModel(100)
	if got := m.vstep(10); got != 10 {
		t.Fatalf("wrap off vstep(10) = %d, want 10", got)
	}
}

func TestVstepShrinksWhenWrapping(t *testing.T) {
	m := seedWrapped(t, 20)
	// 6 terminal rows of wrapped lines => 3 logical lines.
	if got := m.vstep(6); got != 3 {
		t.Fatalf("wrapping vstep(6) = %d, want 3", got)
	}
}

func TestUnstickFromTailNoJumpWhenWrapping(t *testing.T) {
	m := seedWrapped(t, 20)
	// In tail mode the top visible line is the first index collectVisible
	// returns for one screen of terminal rows.
	top := m.collectVisible(m.contentHeight())[0]
	m.scrollBy(-1) // unstick + move up one logical line
	if got := m.streamTopRow(); got != top-1 {
		t.Fatalf("up-from-tail landed at %d, want %d (one line above the visible top)", got, top-1)
	}
}

func TestNoPrematureReStickWhenWrapping(t *testing.T) {
	m := seedWrapped(t, 20)
	m.tailMode = false
	m.setStreamTopRow(11) // lines 11..19 below = 9 logical lines = 18 terminal rows > 10
	m.scrollBy(1)         // down one line; must NOT re-stick (still >1 screen of rows below)
	if m.tailMode {
		t.Fatal("scrolling down re-stuck to tail prematurely (counted lines, not rows)")
	}
}
