package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
)

func TestAnchorRoundTripSingleRowEntries(t *testing.T) {
	m := seedSearch(t, "a", "b", "c", "d") // rows 0..3, one row each
	m.reconcile()
	for i := 0; i < 4; i++ {
		a := m.anchorForRow(i)
		if a.id == "" {
			t.Fatalf("row %d: got sentinel anchor, want resolvable", i)
		}
		got, ok := m.rowForAnchor(a)
		if !ok || got != i {
			t.Fatalf("round-trip row %d: got (%d,%v), want (%d,true)", i, got, ok, i)
		}
	}
}

func TestAnchorForRowNegativeIsSentinel(t *testing.T) {
	m := seedSearch(t, "a", "b")
	m.reconcile()
	if a := m.anchorForRow(-1); a.id != "" {
		t.Fatalf("negative idx: want sentinel, got %+v", a)
	}
}

func TestAnchorForRowPastEndClampsToLastRow(t *testing.T) {
	m := seedSearch(t, "a", "b", "c") // rows 0,1,2
	m.reconcile()
	a := m.anchorForRow(99) // past end, non-empty window
	if a.id == "" {
		t.Fatal("past-end in a non-empty window must clamp to the last row, not the sentinel")
	}
	got, ok := m.rowForAnchor(a)
	if !ok || got != 2 {
		t.Fatalf("past-end clamp: got (%d,%v), want (2,true)", got, ok)
	}
}

func TestAnchorForRowEmptyWindowIsSentinel(t *testing.T) {
	m := newModel(100) // no events, no reconcile → empty window
	if a := m.anchorForRow(0); a.id != "" {
		t.Fatalf("empty window: want sentinel, got %+v", a)
	}
}

func TestRowForAnchorSentinelNotOK(t *testing.T) {
	m := seedSearch(t, "a")
	m.reconcile()
	if _, ok := m.rowForAnchor(rowAnchor{}); ok {
		t.Fatal("sentinel anchor must resolve ok=false")
	}
}

func TestRowForAnchorEvictedEntryNotOK(t *testing.T) {
	m := newModel(2)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	ev := func(v string) render.Event {
		return render.Event{Group: "g", File: "/a.log",
			Rendered: []render.Part{{Type: "text", Value: v}}}
	}
	m.appendEvent(ev("a"))
	m.reconcile()
	a := m.anchorForRow(0)
	if a.id == "" {
		t.Fatal("setup: anchor on 'a' should be resolvable")
	}
	m.appendEvent(ev("b"))
	m.appendEvent(ev("c"))
	m.reconcile() // window now holds {b,c}; "a" evicted
	if _, ok := m.rowForAnchor(a); ok {
		t.Fatal("anchor on evicted entry must resolve ok=false")
	}
}

func TestRowForAnchorClampsOffsetIntoEntry(t *testing.T) {
	m := seedSearch(t, "a")
	m.reconcile()
	id := m.window[0].ID
	got, ok := m.rowForAnchor(rowAnchor{id: id, off: 99})
	if !ok || got != 0 {
		t.Fatalf("clamp: got (%d,%v), want (0,true)", got, ok)
	}
}
