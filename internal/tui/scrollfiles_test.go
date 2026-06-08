package tui

import "testing"

// filesModel returns a model with n file entries and filesScroll at 0.
func filesModel(t *testing.T, n int) *model {
	t.Helper()
	m := seedSearch(t, "x") // gives a sized model
	m.files = make([]FileEntry, n)
	for i := range m.files {
		m.files[i] = FileEntry{Path: string(rune('a' + i))}
	}
	m.showFiles = true
	m.filesScroll = 0
	return m
}

func TestScrollFilesWithinRange(t *testing.T) {
	m := filesModel(t, 5)
	m.filesScroll = 1
	m.scrollFiles(2)
	if m.filesScroll != 3 {
		t.Fatalf("filesScroll = %d, want 3", m.filesScroll)
	}
}

func TestScrollFilesClampsAtTop(t *testing.T) {
	m := filesModel(t, 5)
	m.filesScroll = 0
	m.scrollFiles(-3)
	if m.filesScroll != 0 {
		t.Fatalf("filesScroll = %d, want 0 (clamped at top)", m.filesScroll)
	}
}

func TestScrollFilesClampsAtBottom(t *testing.T) {
	m := filesModel(t, 5)
	m.filesScroll = 4 // last index
	m.scrollFiles(10)
	if m.filesScroll != 4 {
		t.Fatalf("filesScroll = %d, want 4 (clamped at bottom)", m.filesScroll)
	}
}

func TestScrollFilesEmptyListStaysZero(t *testing.T) {
	m := filesModel(t, 0)
	m.filesScroll = 0
	m.scrollFiles(5) // high clamp to len-1=-1, then low clamp to 0
	if m.filesScroll != 0 {
		t.Fatalf("filesScroll = %d, want 0 (empty list)", m.filesScroll)
	}
}
