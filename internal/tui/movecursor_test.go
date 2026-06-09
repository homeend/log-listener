package tui

import "testing"

func TestMoveVisualCursorWithinRange(t *testing.T) {
	m := seedSearch(t, "a", "b", "c", "d")
	m.reconcile()
	m.tailMode = false
	m.setStreamTopRow(0)
	m.visualMode = true
	m.setVisualCursorRow(1)
	m.moveVisualCursor(1)
	if m.visualCursorRow() != 2 {
		t.Fatalf("visualCursor = %d, want 2", m.visualCursorRow())
	}
}

func TestMoveVisualCursorClampsAtTop(t *testing.T) {
	m := seedSearch(t, "a", "b", "c")
	m.reconcile()
	m.tailMode = false
	m.setStreamTopRow(0)
	m.visualMode = true
	m.setVisualCursorRow(0)
	m.moveVisualCursor(-1)
	if m.visualCursorRow() != 0 {
		t.Fatalf("visualCursor = %d, want 0 (clamped at top)", m.visualCursorRow())
	}
}

func TestMoveVisualCursorClampsAtBottom(t *testing.T) {
	m := seedSearch(t, "a", "b", "c")
	m.reconcile()
	m.tailMode = false
	m.setStreamTopRow(0)
	m.visualMode = true
	m.setVisualCursorRow(2) // last row (len 3 → max index 2)
	m.moveVisualCursor(1)
	if m.visualCursorRow() != 2 {
		t.Fatalf("visualCursor = %d, want 2 (clamped at bottom)", m.visualCursorRow())
	}
}
