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
