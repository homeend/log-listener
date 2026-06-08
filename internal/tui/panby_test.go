package tui

import "testing"

func TestPanByRightMoves(t *testing.T) {
	m := seedSearch(t, "x")
	m.horizScroll = 0
	m.panBy(10)
	if m.horizScroll != 10 {
		t.Fatalf("horizScroll = %d, want 10", m.horizScroll)
	}
}

func TestPanByLeftClampsAtZero(t *testing.T) {
	m := seedSearch(t, "x")
	m.horizScroll = 5
	m.panBy(-50)
	if m.horizScroll != 0 {
		t.Fatalf("horizScroll = %d, want 0 (clamped at left edge)", m.horizScroll)
	}
}

func TestPanByRightHasNoUpperClamp(t *testing.T) {
	m := seedSearch(t, "x")
	m.horizScroll = 1000
	m.panBy(50)
	if m.horizScroll != 1050 {
		t.Fatalf("horizScroll = %d, want 1050 (no upper clamp)", m.horizScroll)
	}
}
