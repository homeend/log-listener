package tui

import (
	"fmt"
	"strings"
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

func TestRenderStreamWrappedFillsRowsWithContinuations(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	long := "yyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyy"
	for i := 0; i < 8; i++ {
		m.appendEvent(render.Event{Group: "g", File: "/a.log",
			Rendered: []render.Part{{Type: "text", Value: long}}})
	}
	m.wordWrap = true
	m.width = 40 // ~71 visible cols per line => 2 wrapped rows each
	out := m.renderStream(6)
	lines := strings.Split(out, "\n")
	if len(lines) != 6 {
		t.Fatalf("renderStream produced %d rows, want exactly 6", len(lines))
	}
	for i, ln := range lines {
		if w := dispWidth(stripANSI(ln)); w != 40 {
			t.Fatalf("row %d width = %d, want 40", i, w)
		}
		if !strings.Contains(ln, "y") {
			t.Fatalf("row %d is blank; wrap should fill it with a continuation: %q", i, ln)
		}
	}
}

func TestFooterShowsWrapWhenWrapping(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = m2.(*model)
	m.wordWrap = true
	foot := stripANSI(m.renderFooter())
	if !strings.Contains(foot, "wrap") {
		t.Fatalf("footer should show wrap indicator, got %q", foot)
	}
	if strings.Contains(foot, "col:") {
		t.Fatalf("footer should not show col: while wrapping, got %q", foot)
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

// Regression (final-review C1): with filter + tail + wrap and an overflowing
// window, collectVisible's filter branch is top-anchored, so renderStreamWrapped
// must top-align — bottom-aligning would drop visible[0] (the published from
// entry) off the top of the screen.
func TestRenderStreamWrappedFilterTailTopAligns(t *testing.T) {
	m := newModel(1000)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	for i := 0; i < 8; i++ {
		body := fmt.Sprintf("needle L%02d %s", i, strings.Repeat("x", 40))
		m.appendEvent(render.Event{Group: "g", File: "/x.log",
			Rendered: []render.Part{{Type: "text", Value: body}}})
	}
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = m2.(*model)
	m = typeQuery(t, m, "needle")
	m.filterMode = true
	m.wordWrap = true
	m.width = 40 // prefix+body ~62 cols => 2 rows per line
	m.tailMode = true
	m.setStreamTopRow(0)

	visible := m.collectVisible(5)
	if len(visible) == 0 || visible[0] != 0 {
		t.Fatalf("setup: expected visible[0]=0, got %v", visible)
	}
	// 3 lines * 2 rows = 6 segs into 5 rows => overflow; top row must be the
	// START of line 0 (contains "L00"), not a continuation of a later line.
	out := m.renderStream(5)
	top := strings.Split(out, "\n")[0]
	if !strings.Contains(top, "L00") {
		t.Fatalf("filter+tail+wrap dropped visible[0] off the top; top row = %q", stripANSI(top))
	}
}
