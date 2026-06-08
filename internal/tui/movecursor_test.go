package tui

import "testing"

func TestMoveVisualCursorWithinRange(t *testing.T) {
	m := seedSearch(t, "a", "b", "c", "d")
	m.reconcile()
	m.tailMode = false
	m.setStreamTopRow(0)
	m.visualMode = true
	m.visualCursor = 1
	m.moveVisualCursor(1)
	if m.visualCursor != 2 {
		t.Fatalf("visualCursor = %d, want 2", m.visualCursor)
	}
}

func TestMoveVisualCursorClampsAtTop(t *testing.T) {
	m := seedSearch(t, "a", "b", "c")
	m.reconcile()
	m.tailMode = false
	m.setStreamTopRow(0)
	m.visualMode = true
	m.visualCursor = 0
	m.moveVisualCursor(-1)
	if m.visualCursor != 0 {
		t.Fatalf("visualCursor = %d, want 0 (clamped at top)", m.visualCursor)
	}
}

func TestMoveVisualCursorClampsAtBottom(t *testing.T) {
	m := seedSearch(t, "a", "b", "c")
	m.reconcile()
	m.tailMode = false
	m.setStreamTopRow(0)
	m.visualMode = true
	m.visualCursor = 2 // last row (len 3 → max index 2)
	m.moveVisualCursor(1)
	if m.visualCursor != 2 {
		t.Fatalf("visualCursor = %d, want 2 (clamped at bottom)", m.visualCursor)
	}
}
