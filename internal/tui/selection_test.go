package tui

import "testing"

func TestSelectionBoundsNoAnchorIsCaretRow(t *testing.T) {
	m := seedSearch(t, "a", "b", "c")
	m.reconcile()
	m.visualCursor = 2
	m.visualAnchor = -1
	lo, hi := m.selectionBounds()
	if lo != 2 || hi != 2 {
		t.Fatalf("no anchor: got (%d,%d), want (2,2)", lo, hi)
	}
}

func TestSelectionBoundsAnchorBelowCursorIsOrdered(t *testing.T) {
	m := seedSearch(t, "a", "b", "c", "d")
	m.reconcile()
	m.visualAnchor = 1
	m.visualCursor = 3
	lo, hi := m.selectionBounds()
	if lo != 1 || hi != 3 {
		t.Fatalf("got (%d,%d), want (1,3)", lo, hi)
	}
}

func TestSelectionBoundsAnchorAboveCursorIsOrdered(t *testing.T) {
	m := seedSearch(t, "a", "b", "c", "d")
	m.reconcile()
	m.visualAnchor = 3
	m.visualCursor = 1
	lo, hi := m.selectionBounds()
	if lo != 1 || hi != 3 {
		t.Fatalf("got (%d,%d), want (1,3) (ordered)", lo, hi)
	}
}
