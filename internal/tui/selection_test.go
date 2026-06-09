package tui

import "testing"

func TestSelectionBoundsNoAnchorIsCaretRow(t *testing.T) {
	m := seedSearch(t, "a", "b", "c")
	m.reconcile()
	m.setVisualCursorRow(2)
	m.setVisualAnchorRow(-1)
	lo, hi := m.selectionBounds()
	if lo != 2 || hi != 2 {
		t.Fatalf("no anchor: got (%d,%d), want (2,2)", lo, hi)
	}
}

func TestSelectionBoundsAnchorBelowCursorIsOrdered(t *testing.T) {
	m := seedSearch(t, "a", "b", "c", "d")
	m.reconcile()
	m.setVisualAnchorRow(1)
	m.setVisualCursorRow(3)
	lo, hi := m.selectionBounds()
	if lo != 1 || hi != 3 {
		t.Fatalf("got (%d,%d), want (1,3)", lo, hi)
	}
}

func TestSelectionBoundsAnchorAboveCursorIsOrdered(t *testing.T) {
	m := seedSearch(t, "a", "b", "c", "d")
	m.reconcile()
	m.setVisualAnchorRow(3)
	m.setVisualCursorRow(1)
	lo, hi := m.selectionBounds()
	if lo != 1 || hi != 3 {
		t.Fatalf("got (%d,%d), want (1,3) (ordered)", lo, hi)
	}
}
