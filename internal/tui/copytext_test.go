package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
	"github.com/homeend/log-listener/internal/searchmatch"
)

// joinPlain renders the given displayLines through plainExportLine and joins.
func joinPlain(dls []displayLine) string {
	parts := make([]string, len(dls))
	for i, dl := range dls {
		parts[i] = plainExportLine(dl)
	}
	return strings.Join(parts, "\n")
}

func TestSelectionTextViewportMatchesSnapshot(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 6})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	seedIDs(m, "a", "b", "c", "d", "e", "f")
	m.tailMode = false
	m.streamTop = 0
	got := buildSelectionText(m)
	want := strings.Join(m.snapshotViewport(), "\n")
	if got != want {
		t.Fatalf("viewport selection text:\n got %q\nwant %q", got, want)
	}
}

func TestSelectionTextSearchHitCopiesWholeEntry(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	// L0 single row; L1 a multi-row entry (embedded newlines).
	m.appendEvent(render.Event{ID: "L0", Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "start"}}})
	m.appendEvent(render.Event{ID: "L1", Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "config:\n  k=v\n  j=w"}}})
	m.matcher, _ = searchmatch.Compile("config", false)
	m.searchHit = 1 // row 1 is L1's head row
	got := buildSelectionText(m)
	want := joinPlain(m.displayCache[m.visibleEntries()[1].ID]) // ALL of L1's rows, not just the hit row
	if got != want {
		t.Fatalf("search-hit selection text:\n got %q\nwant %q", got, want)
	}
	if !strings.Contains(got, "k=v") || !strings.Contains(got, "j=w") {
		t.Fatalf("expected the whole entry's rows, got %q", got)
	}
}

func TestSelectionTextFocusedBlock(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	// L0 lead; L1+L2 form a go-panic block ("goroutine " is a continuation sig).
	seedIDs(m, "start", "panic: boom", "goroutine 1 [running]:")
	m.tailMode = false
	m.streamTop = 0
	m = key(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}}) // focus block at line 1
	s, e, ok := m.focusedBlockRange()
	if !ok {
		t.Fatal("expected a focused block range")
	}
	got := buildSelectionText(m)
	want := joinPlain(m.lines[s : e+1])
	if got != want {
		t.Fatalf("focused-block selection text:\n got %q\nwant %q", got, want)
	}
}

func TestSelectionTextEmptyBuffer(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 6})
	m = m2.(*model)
	if got := buildSelectionText(m); got != "" {
		t.Fatalf("empty buffer selection text = %q, want empty", got)
	}
	if txt, n := m.copySelectionText(); txt != "" || n != 0 {
		t.Fatalf("copySelectionText on empty = (%q,%d), want (\"\",0)", txt, n)
	}
}

// Parity guard: Y's selection ends must equal the entries y references.
func TestCopyTextParityWithReference(t *testing.T) {
	mk := func() *model {
		m := newModel(100)
		m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 8})
		m = m2.(*model)
		m.groupOrder = []string{"g"}
		m.groupEnabled["g"] = true
		return m
	}

	// viewport context
	mv := mk()
	seedIDs(mv, "a", "b", "c", "d")
	mv.tailMode = false
	mv.streamTop = 0
	assertParity(t, "viewport", mv, mv.selectedRows())

	// focused-block context
	mb := mk()
	seedIDs(mb, "start", "panic: boom", "goroutine 1 [running]:")
	mb.tailMode = false
	mb.streamTop = 0
	mb = key(mb, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	assertParity(t, "block", mb, mb.selectedRows())

	// search-hit context
	ms := mk()
	seedIDs(ms, "apple", "banana", "cherry")
	ms.matcher, _ = searchmatch.Compile("banana", false)
	ms.searchHit = 1
	assertParity(t, "search", ms, ms.selectedRows())
}

// assertParity checks that the first/last entry ids of rows match the ids
// encoded in buildReference(m). Works for both "line:X" and "range:A..B".
func assertParity(t *testing.T, ctx string, m *model, rows []int) {
	t.Helper()
	if len(rows) == 0 {
		t.Fatalf("%s: no rows selected", ctx)
	}
	gotFirst := m.entryIDForLine(rows[0])
	gotLast := m.entryIDForLine(rows[len(rows)-1])
	refFirst, refLast := parseRefEnds(t, buildReference(m))
	if gotFirst != refFirst || gotLast != refLast {
		t.Fatalf("%s parity: Y ends (%s..%s) != y ref ends (%s..%s)",
			ctx, gotFirst, gotLast, refFirst, refLast)
	}
}

// parseRefEnds extracts (first,last) entry ids from a "line:X" or "range:A..B".
func parseRefEnds(t *testing.T, ref string) (string, string) {
	t.Helper()
	switch {
	case strings.HasPrefix(ref, "line:"):
		id := strings.TrimPrefix(ref, "line:")
		return id, id
	case strings.HasPrefix(ref, "range:"):
		body := strings.TrimPrefix(ref, "range:")
		parts := strings.SplitN(body, "..", 2)
		if len(parts) != 2 {
			t.Fatalf("bad range ref %q", ref)
		}
		return parts[0], parts[1]
	default:
		t.Fatalf("unexpected ref %q", ref)
		return "", ""
	}
}
