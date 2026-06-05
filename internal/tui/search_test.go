package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"log-listener/internal/render"
)

// seedSearchModel pushes n events labelled "line-i". Indices where
// hitIdxs is true also include "needle" in the body so search has
// known landing points.
func seedSearchModel(t *testing.T, n int, hitIdxs map[int]bool) *model {
	t.Helper()
	m := newModel(1000)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	for i := 0; i < n; i++ {
		body := fmt.Sprintf("line-%d", i)
		if hitIdxs[i] {
			body += " needle here"
		}
		m.appendEvent(render.Event{Group: "g", File: "/x.log",
			Rendered: []render.Part{{Type: "text", Value: body}}})
	}
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	return m2.(*model)
}

// typeQuery simulates pressing "/" then the chars of q then Enter.
func typeQuery(t *testing.T, m *model, q string) *model {
	t.Helper()
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = m2.(*model)
	if !m.searchInput {
		t.Fatal("'/' should enter search input mode")
	}
	for _, r := range q {
		if r == ' ' {
			m2, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
		} else {
			m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
		m = m2.(*model)
	}
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m2.(*model)
	if m.searchInput {
		t.Fatal("Enter should commit search and exit input mode")
	}
	return m
}

func TestModelSearchBasic(t *testing.T) {
	hits := map[int]bool{2: true, 7: true, 15: true}
	m := seedSearchModel(t, 20, hits)
	m = typeQuery(t, m, "needle")

	if m.searchTerm != "needle" {
		t.Fatalf("searchTerm = %q want %q", m.searchTerm, "needle")
	}
	// In tail mode commit walks backward — last hit (15) should win.
	if m.searchHit != 15 {
		t.Fatalf("searchHit = %d want 15", m.searchHit)
	}
	if m.tailMode {
		t.Fatal("commit should unstick tail mode")
	}
	// Footer should expose the term.
	if !strings.Contains(m.View(), "/needle") {
		t.Fatalf("footer missing search term:\n%s", m.View())
	}
}

func TestModelSearchInputEscCancels(t *testing.T) {
	m := seedSearchModel(t, 5, map[int]bool{2: true})
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = m2.(*model)
	if m.searchInput {
		t.Fatal("Esc should exit input mode")
	}
	if m.searchTerm != "" {
		t.Fatalf("Esc must NOT commit a search term, got %q", m.searchTerm)
	}
	if m.searchQuery != "" {
		t.Fatalf("Esc must clear the typed query, got %q", m.searchQuery)
	}
}

func TestModelSearchInputBackspace(t *testing.T) {
	m := seedSearchModel(t, 5, nil)
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = m2.(*model)
	for _, r := range "abc" {
		m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = m2.(*model)
	}
	if m.searchQuery != "abc" {
		t.Fatalf("query = %q want abc", m.searchQuery)
	}
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = m2.(*model)
	if m.searchQuery != "ab" {
		t.Fatalf("after backspace query = %q want ab", m.searchQuery)
	}
}

func TestModelSearchNextAndPrev(t *testing.T) {
	hits := map[int]bool{2: true, 7: true, 15: true}
	m := seedSearchModel(t, 20, hits)
	m = typeQuery(t, m, "needle")
	if m.searchHit != 15 {
		t.Fatalf("initial hit = %d want 15", m.searchHit)
	}

	// p walks backward through hits.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	m = m2.(*model)
	if m.searchHit != 7 {
		t.Fatalf("after p hit = %d want 7", m.searchHit)
	}
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	m = m2.(*model)
	if m.searchHit != 2 {
		t.Fatalf("after pp hit = %d want 2", m.searchHit)
	}

	// p past the first hit triggers the wrap prompt.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	m = m2.(*model)
	if m.wrapPrompt != 'p' {
		t.Fatalf("p past start should set wrapPrompt='p', got %q", string(m.wrapPrompt))
	}
	// y answers yes — wrap from the end.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = m2.(*model)
	if m.wrapPrompt != 0 {
		t.Fatal("y should dismiss the wrap prompt")
	}
	if m.searchHit != 15 {
		t.Fatalf("after wrap hit = %d want 15", m.searchHit)
	}

	// n past the last hit also prompts.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = m2.(*model)
	if m.wrapPrompt != 'n' {
		t.Fatalf("n past end should set wrapPrompt='n', got %q", string(m.wrapPrompt))
	}
	// n answers no — dismiss without moving.
	prev := m.searchHit
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = m2.(*model)
	if m.wrapPrompt != 0 {
		t.Fatal("n should dismiss the wrap prompt")
	}
	if m.searchHit != prev {
		t.Fatal("n (decline wrap) must NOT move the hit")
	}
}

func TestModelSearchWrapForward(t *testing.T) {
	hits := map[int]bool{2: true, 7: true}
	m := seedSearchModel(t, 10, hits)
	m = typeQuery(t, m, "needle")
	// Initial hit in tail mode = 7 (last). n should prompt for wrap.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = m2.(*model)
	if m.wrapPrompt != 'n' {
		t.Fatalf("wrapPrompt = %q want 'n'", string(m.wrapPrompt))
	}
	// y wraps to first hit (2).
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = m2.(*model)
	if m.searchHit != 2 {
		t.Fatalf("after y wrap hit = %d want 2", m.searchHit)
	}
}

func TestModelSearchSkipsDisabledGroup(t *testing.T) {
	m := newModel(1000)
	m.groupOrder = []string{"a", "b"}
	m.groupEnabled["a"] = true
	m.groupEnabled["b"] = false
	// 0: a (no match), 1: b (match — disabled), 2: a (match), 3: b (match — disabled)
	m.appendEvent(render.Event{Group: "a", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "alpha"}}})
	m.appendEvent(render.Event{Group: "b", File: "/b.log",
		Rendered: []render.Part{{Type: "text", Value: "needle in b"}}})
	m.appendEvent(render.Event{Group: "a", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "needle in a"}}})
	m.appendEvent(render.Event{Group: "b", File: "/b.log",
		Rendered: []render.Part{{Type: "text", Value: "needle in b2"}}})
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = m2.(*model)

	m = typeQuery(t, m, "needle")
	if m.searchHit != 2 {
		t.Fatalf("disabled group b hits must be skipped; got hit at %d (events=%+v)",
			m.searchHit, m.lines)
	}
}

func TestModelSearchEscClearsActive(t *testing.T) {
	m := seedSearchModel(t, 10, map[int]bool{3: true})
	m = typeQuery(t, m, "needle")
	if m.searchTerm == "" {
		t.Fatal("setup: term should be active")
	}
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = m2.(*model)
	if m.searchTerm != "" {
		t.Fatalf("Esc must clear active term, got %q", m.searchTerm)
	}
	if m.searchHit != -1 {
		t.Fatalf("Esc must reset searchHit to -1, got %d", m.searchHit)
	}
}

func TestModelSearchCaseInsensitive(t *testing.T) {
	m := newModel(100)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	m.appendEvent(render.Event{Group: "g", File: "/x.log",
		Rendered: []render.Part{{Type: "text", Value: "ERROR encountered"}}})
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = m2.(*model)
	m = typeQuery(t, m, "error") // lowercase — should still match
	if m.searchHit != 0 {
		t.Fatalf("case-insensitive search missed, hit=%d", m.searchHit)
	}
}

func TestModelSearchNoMatch(t *testing.T) {
	m := seedSearchModel(t, 10, nil) // no needle anywhere
	m = typeQuery(t, m, "needle")
	if m.searchHit != -1 {
		t.Fatalf("no-match commit should leave searchHit=-1, got %d", m.searchHit)
	}
	if m.searchTerm == "" {
		t.Fatal("term should stay set so n/p can wrap-prompt")
	}
	// n with no matches anywhere still prompts (could be useful if events arrive later).
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = m2.(*model)
	if m.wrapPrompt != 'n' {
		t.Fatalf("n with no hits should prompt, got %q", string(m.wrapPrompt))
	}
}

func TestModelSearchFooterDuringInput(t *testing.T) {
	m := seedSearchModel(t, 3, nil)
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	m = m2.(*model)
	view := m.View()
	if !strings.Contains(view, "/ab_") {
		t.Fatalf("footer should show '/ab_' during input:\n%s", view)
	}
}

func TestModelSearchFooterWrapPrompt(t *testing.T) {
	m := seedSearchModel(t, 5, map[int]bool{3: true})
	m = typeQuery(t, m, "needle")
	// jumps to 3 in tail mode; n moves past end → wrap prompt
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = m2.(*model)
	view := m.View()
	if !strings.Contains(view, "wrap to top") {
		t.Fatalf("forward wrap prompt missing:\n%s", view)
	}
}

func TestModelSearchHighlightInView(t *testing.T) {
	m := seedSearchModel(t, 3, map[int]bool{1: true})
	m = typeQuery(t, m, "needle")
	view := m.View()
	// The literal "needle" text must still appear (ANSI styling wraps it,
	// not replaces it).
	if !strings.Contains(stripANSI(view), "needle") {
		t.Fatalf("highlighted view missing 'needle':\n%s", view)
	}
}

func TestSearchRepeatLastTerm(t *testing.T) {
	m := seedSearchModel(t, 5, map[int]bool{2: true, 4: true})
	m = typeQuery(t, m, "needle")
	if m.searchTerm != "needle" {
		t.Fatalf("term = %q", m.searchTerm)
	}
	m.clearSearch()
	if m.searchTerm != "" {
		t.Fatal("clear should drop the active term")
	}
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m2.(*model)
	if m.searchTerm != "needle" {
		t.Fatalf("empty-Enter should repeat last term, got %q", m.searchTerm)
	}
	if m.searchQuery != "needle" {
		t.Fatalf("footer query should reflect the repeated term, got %q", m.searchQuery)
	}
}

func TestSearchEmptyEnterNoPriorTermClears(t *testing.T) {
	m := seedSearchModel(t, 3, nil)
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = m2.(*model)
	if m.searchTerm != "" {
		t.Fatalf("empty Enter with no prior term should set no term, got %q", m.searchTerm)
	}
}

func TestSearchUpDownNavigateHits(t *testing.T) {
	m := seedSearchModel(t, 6, map[int]bool{1: true, 3: true, 5: true})
	m = typeQuery(t, m, "needle") // tail-mode commit lands on the last hit (5)
	if m.searchHit < 0 {
		t.Fatal("expected a current hit after commit")
	}
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = m2.(*model)
	prev := m.searchHit
	if prev >= 5 {
		t.Fatalf("Up should move to an earlier hit, got %d", prev)
	}
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = m2.(*model)
	if m.searchHit <= prev {
		t.Fatalf("Down should advance to a later hit, got %d (prev %d)", m.searchHit, prev)
	}
}

func TestUpDownScrollWhenNoSearch(t *testing.T) {
	m := newModel(1000)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	for i := 0; i < 50; i++ {
		m.appendEvent(render.Event{Group: "g", File: "/x.log",
			Rendered: []render.Part{{Type: "text", Value: fmt.Sprintf("line %d", i)}}})
	}
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = m2.(*model)
	m.tailMode = false
	m.streamTop = 20
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = m2.(*model)
	if m.streamTop != 19 {
		t.Fatalf("Up with no active search should scroll one line: streamTop=%d want 19", m.streamTop)
	}
}

// TestClipLinePreservesANSIOnHorizontalScroll guards the bug where panning
// Left/Right wiped the search highlight (and all color): horizontal scroll
// used to stripANSI the whole line before slicing, discarding every escape
// sequence. clipLine must now keep the styling that falls in the window.
//
// Tested at the clipLine layer because lipgloss renders with an Ascii (no
// color) profile under `go test`, so a View()-level assertion on highlight
// bytes can't distinguish "styled" from "plain".
func TestJumpToHitScrollsHorizontallyToMatch(t *testing.T) {
	m := newModel(1000)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	m.showGroup = false
	m.showFile = false
	body := strings.Repeat("x", 100) + "NEEDLE" + strings.Repeat("y", 20)
	m.appendEvent(render.Event{Group: "g", File: "/x.log",
		Rendered: []render.Part{{Type: "text", Value: body}}})
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	m = m2.(*model)
	m = typeQuery(t, m, "needle") // match starts at column 100, far right
	if m.horizScroll == 0 {
		t.Fatal("expected horizScroll to move to the off-screen match")
	}
	if 100 < m.horizScroll || 100 >= m.horizScroll+m.width {
		t.Fatalf("match col 100 not in view [%d,%d)", m.horizScroll, m.horizScroll+m.width)
	}
}

func TestJumpToHitKeepsHorizWhenMatchVisible(t *testing.T) {
	m := newModel(1000)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	m.showGroup = false
	m.showFile = false
	m.appendEvent(render.Event{Group: "g", File: "/x.log",
		Rendered: []render.Part{{Type: "text", Value: "early NEEDLE here"}}})
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m = typeQuery(t, m, "needle")
	if m.horizScroll != 0 {
		t.Fatalf("match already visible; horizScroll should stay 0, got %d", m.horizScroll)
	}
}

func TestHitColumnAccountsForPrefix(t *testing.T) {
	m := newModel(1000)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	m.showGroup = true // prefix "[g] " = 4 columns
	m.showFile = false
	m.appendEvent(render.Event{Group: "g", File: "/x.log",
		Rendered: []render.Part{{Type: "text", Value: "NEEDLE"}}})
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.searchTerm = "needle"
	if got := m.hitColumn(0); got != 4 {
		t.Fatalf("hitColumn with [g] prefix = %d, want 4", got)
	}
}

func TestClipLinePreservesANSIOnHorizontalScroll(t *testing.T) {
	m := newModel(10)
	m.width = 20
	m.horizScroll = 5
	const esc = "\x1b[31m"
	const reset = "\x1b[0m"
	// 10 plain runes, a styled "WORD", then a plain tail.
	line := "0123456789" + esc + "WORD" + reset + "tail"
	visW := runeLen(stripANSI(line)) // 10 + 4 + 4 = 18

	got := m.clipLine(line, visW)

	// The styled span survives intact inside the scrolled window.
	if !strings.Contains(got, esc+"WORD"+reset) {
		t.Fatalf("clipLine dropped ANSI styling under horizontal scroll: %q", got)
	}
	// Visible text is the window [5, 5+20), right-padded to width.
	wantText := "56789WORDtail"
	wantPadded := wantText + strings.Repeat(" ", m.width-runeLen(wantText))
	if stripANSI(got) != wantPadded {
		t.Fatalf("visible text wrong:\n got %q\nwant %q", stripANSI(got), wantPadded)
	}
}

func TestFilterShowsWholeMatchingEntries(t *testing.T) {
	m := newModel(1000)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	m.appendEvent(render.Event{Group: "g", File: "/x.log",
		Rendered: []render.Part{{Type: "text", Value: "boring line"}}})
	m.appendEvent(render.Event{Group: "g", File: "/x.log",
		Rendered: []render.Part{
			{Type: "text", Value: "request received"},
			{Type: "json", Value: map[string]any{"userId": 42}},
		}})
	m.appendEvent(render.Event{Group: "g", File: "/x.log",
		Rendered: []render.Part{{Type: "text", Value: "userId in plain line"}}})
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = m2.(*model)
	m = typeQuery(t, m, "userId")
	m.filterMode = true

	e0 := len(m.entries[0].lines)
	e1 := len(m.entries[1].lines)
	e2 := len(m.entries[2].lines)
	if e1 < 2 {
		t.Fatalf("setup: entry1 should have a multi-line json block, got %d lines", e1)
	}
	fil := m.filteredIndices()
	if len(fil) != e1+e2 {
		t.Fatalf("filtered = %d, want %d (whole entry1 %d + entry2 %d)", len(fil), e1+e2, e1, e2)
	}
	for _, idx := range fil {
		if idx < e0 {
			t.Fatalf("entry0 (no match) line %d must be excluded", idx)
		}
	}
}

func TestFilterTNoopWithoutTerm(t *testing.T) {
	m := seedSearchModel(t, 3, map[int]bool{1: true})
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	m = m2.(*model)
	if m.filterMode {
		t.Fatal("t must be a no-op with no active search term")
	}
	m = typeQuery(t, m, "needle")
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	m = m2.(*model)
	if !m.filterMode {
		t.Fatal("t should enable the filter when a term is active")
	}
}

func TestCollectVisibleFilterModeOnlyShowsFiltered(t *testing.T) {
	m := newModel(1000)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	m.appendEvent(render.Event{Group: "g", File: "/x.log",
		Rendered: []render.Part{{Type: "text", Value: "boring"}}})
	m.appendEvent(render.Event{Group: "g", File: "/x.log",
		Rendered: []render.Part{{Type: "text", Value: "needle one"}}})
	m.appendEvent(render.Event{Group: "g", File: "/x.log",
		Rendered: []render.Part{{Type: "text", Value: "needle two"}}})
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = m2.(*model)
	m = typeQuery(t, m, "needle")
	m.filterMode = true
	m.streamTop = 0
	vis := m.collectVisible(m.contentHeight())
	filSet := map[int]bool{}
	for _, i := range m.filteredIndices() {
		filSet[i] = true
	}
	if len(vis) == 0 {
		t.Fatal("expected visible filtered rows")
	}
	for _, i := range vis {
		if !filSet[i] {
			t.Fatalf("visible idx %d is not in the filtered set", i)
		}
	}
}

func TestFooterShowsFilterTag(t *testing.T) {
	m := seedSearchModel(t, 3, map[int]bool{1: true})
	m = typeQuery(t, m, "needle")
	if strings.Contains(stripANSI(m.renderFooter()), "filter") {
		t.Fatal("filter tag should be absent when filterMode is off")
	}
	m.filterMode = true
	if !strings.Contains(stripANSI(m.renderFooter()), "filter") {
		t.Fatalf("expected filter tag in footer:\n%s", stripANSI(m.renderFooter()))
	}
}
