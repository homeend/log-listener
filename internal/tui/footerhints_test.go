package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/keymap"
	"github.com/homeend/log-listener/internal/render"
	"github.com/homeend/log-listener/internal/searchmatch"
)

func newFooterModel(t *testing.T) *model {
	t.Helper()
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 12})
	return m2.(*model)
}

func TestCompactStatusTailMode(t *testing.T) {
	m := newFooterModel(t)
	m.lines = make([]displayLine, 7)
	m.tailMode = true
	got := m.compactStatus()
	if !strings.Contains(got, "ev 7") || !strings.Contains(got, "tail") {
		t.Fatalf("compactStatus tail = %q, want ev 7 + tail", got)
	}
}

func TestCompactStatusBrowseWithSearch(t *testing.T) {
	// Seed via appendEvent+reconcile so the anchor system (window/displayCache)
	// is populated; raw m.lines= assignment can't drive anchor-based streamTopRow.
	m := newFooterModel(t)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	for i := 0; i < 9; i++ {
		m.appendEvent(render.Event{Group: "g", File: "/a.log",
			Rendered: []render.Part{{Type: "text", Value: "line"}}})
	}
	m.reconcile()
	if got := len(m.lines); got != 9 {
		t.Fatalf("seeded %d lines, want 9", got)
	}
	m.tailMode = false
	m.setStreamTopRow(3)
	if got := m.streamTopRow(); got != 3 {
		t.Fatalf("setStreamTopRow(3) resolved to %d, want 3", got)
	}
	m.matcher, _ = compileTestMatcher(t, "err")
	m.searchQuery = "err"
	got := m.compactStatus()
	if !strings.Contains(got, "ev 9") || !strings.Contains(got, "@3/9") || !strings.Contains(got, "/err") {
		t.Fatalf("compactStatus browse = %q, want ev 9 + @3/9 + /err", got)
	}
}

func compileTestMatcher(t *testing.T, q string) (*searchmatch.Matcher, error) {
	t.Helper()
	return searchmatch.Compile(q, false)
}

func keymapDefaultLinux() *keymap.Keymap { return keymap.Default("linux") }

func keymapResolveFilterF() (*keymap.Keymap, error) {
	return keymap.Resolve("linux", map[string][]string{"filter": {"F"}}, nil)
}

func TestContextHintsDefaultTail(t *testing.T) {
	m := newFooterModel(t)
	m.km = keymapDefaultLinux()
	m.tailMode = true
	label, hints := m.contextHints()
	if label != "" {
		t.Fatalf("default label = %q, want empty", label)
	}
	joined := strings.Join(hints, " | ")
	for _, want := range []string{"search", "select", "blocks", "collapse", "help"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("default hints %q missing %q", joined, want)
		}
	}
}

func TestContextHintsBrowse(t *testing.T) {
	m := newFooterModel(t)
	m.km = keymapDefaultLinux()
	m.tailMode = false
	label, hints := m.contextHints()
	if label != "BROWSE" {
		t.Fatalf("label = %q, want BROWSE", label)
	}
	joined := strings.Join(hints, " | ")
	for _, want := range []string{"tail", "top", "scroll", "page", "select"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("browse hints %q missing %q", joined, want)
		}
	}
}

func TestContextHintsSearch(t *testing.T) {
	m := newFooterModel(t)
	m.km = keymapDefaultLinux()
	m.tailMode = false
	m.matcher, _ = compileTestMatcher(t, "x")
	label, hints := m.contextHints()
	if label != "SEARCH" {
		t.Fatalf("label = %q, want SEARCH", label)
	}
	joined := strings.Join(hints, " | ")
	for _, want := range []string{"next·prev", "filter", "blocks", "clear"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("search hints %q missing %q", joined, want)
		}
	}
	if strings.Contains(joined, "unfilter") {
		t.Fatalf("search (not filter) should say filter, not unfilter: %q", joined)
	}
}

func TestContextHintsFilterVariant(t *testing.T) {
	m := newFooterModel(t)
	m.km = keymapDefaultLinux()
	m.matcher, _ = compileTestMatcher(t, "x")
	m.filterMode = true
	label, hints := m.contextHints()
	if label != "FILTER" {
		t.Fatalf("label = %q, want FILTER", label)
	}
	if !strings.Contains(strings.Join(hints, " | "), "unfilter") {
		t.Fatalf("filter variant should say unfilter: %v", hints)
	}
}

func TestContextHintsBlock(t *testing.T) {
	m := newFooterModel(t)
	m.km = keymapDefaultLinux()
	m.blockFocused = true
	label, hints := m.contextHints()
	if label != "BLOCK" {
		t.Fatalf("label = %q, want BLOCK", label)
	}
	for _, want := range []string{"next·prev", "marked", "marks", "copy"} {
		if !strings.Contains(strings.Join(hints, " | "), want) {
			t.Fatalf("block hints %v missing %q", hints, want)
		}
	}
}

func TestContextHintsVisual(t *testing.T) {
	m := newFooterModel(t)
	m.km = keymapDefaultLinux()
	m.visualMode = true
	label, hints := m.contextHints()
	if label != "VISUAL" {
		t.Fatalf("label = %q, want VISUAL", label)
	}
	for _, want := range []string{"space anchor", "ref", "text", "save", "cancel"} {
		if !strings.Contains(strings.Join(hints, " | "), want) {
			t.Fatalf("visual hints %v missing %q", hints, want)
		}
	}
}

func TestContextHintsPrecedence(t *testing.T) {
	m := newFooterModel(t)
	m.km = keymapDefaultLinux()
	m.visualMode = true
	m.blockFocused = true
	m.matcher, _ = compileTestMatcher(t, "x")
	m.tailMode = false
	if label, _ := m.contextHints(); label != "VISUAL" {
		t.Fatalf("visual should win, got %q", label)
	}
	m.visualMode = false
	if label, _ := m.contextHints(); label != "BLOCK" {
		t.Fatalf("block should win, got %q", label)
	}
	m.blockFocused = false
	if label, _ := m.contextHints(); label != "SEARCH" {
		t.Fatalf("search should win, got %q", label)
	}
	m.matcher = nil
	if label, _ := m.contextHints(); label != "BROWSE" {
		t.Fatalf("browse should win, got %q", label)
	}
}

func TestContextHintsOverrideReflected(t *testing.T) {
	km, err := keymapResolveFilterF()
	if err != nil {
		t.Fatal(err)
	}
	m := newFooterModel(t)
	m.km = km
	m.matcher, _ = compileTestMatcher(t, "x")
	_, hints := m.contextHints()
	joined := strings.Join(hints, " | ")
	if !strings.Contains(joined, "F filter") {
		t.Fatalf("override not reflected, hints = %q", joined)
	}
}

func TestComposeFooterBarFitsWidth(t *testing.T) {
	m := newFooterModel(t)
	m.km = keymapDefaultLinux()
	m.width = 120
	m.lines = make([]displayLine, 5)
	bar := m.composeFooterBar(m.contextHints())
	if dispWidth(bar) != 120 {
		t.Fatalf("bar width = %d, want 120\n%q", dispWidth(bar), bar)
	}
	if !strings.Contains(stripANSI(bar), "ev 5") {
		t.Fatalf("bar missing status tail: %q", stripANSI(bar))
	}
}

func TestComposeFooterBarNarrowTruncates(t *testing.T) {
	m := newFooterModel(t)
	m.km = keymapDefaultLinux()
	m.width = 34 // too narrow for the full default hint list + status
	m.lines = make([]displayLine, 5)
	bar := m.composeFooterBar(m.contextHints())
	plain := stripANSI(bar)
	if dispWidth(bar) != 34 {
		t.Fatalf("narrow bar width = %d, want 34\n%q", dispWidth(bar), plain)
	}
	if !strings.Contains(plain, "…") {
		t.Fatalf("narrow bar should drop low-priority hints with an ellipsis: %q", plain)
	}
	if !strings.Contains(plain, "ev 5") {
		t.Fatalf("narrow bar must keep the status tail: %q", plain)
	}
	if !strings.Contains(plain, "search") {
		t.Fatalf("narrow bar should keep the top-priority hint: %q", plain)
	}
}

func TestRenderFooterUsesContextHints(t *testing.T) {
	m := newFooterModel(t)
	m.km = keymapDefaultLinux()
	m.lines = make([]displayLine, 3)

	// Default tail: shows default hints + status, no mode label.
	plain := stripANSI(m.renderFooter())
	if !strings.Contains(plain, "search") || !strings.Contains(plain, "ev 3") {
		t.Fatalf("tail footer = %q, want default hints + status", plain)
	}

	// Visual mode: VISUAL label + its hints.
	m.visualMode = true
	plain = stripANSI(m.renderFooter())
	if !strings.Contains(plain, "VISUAL") || !strings.Contains(plain, "save") {
		t.Fatalf("visual footer = %q, want VISUAL hints", plain)
	}
	m.visualMode = false

	// Takeover bars still win: typing a search query.
	m.searchInput = true
	m.searchQuery = "abc"
	plain = stripANSI(m.renderFooter())
	if !strings.Contains(plain, "/abc") || strings.Contains(plain, "ev 3") {
		t.Fatalf("search-input footer = %q, want /abc takeover (no status)", plain)
	}
	m.searchInput = false

	// Flash still takes over.
	m.flash = "copied 2 lines"
	plain = stripANSI(m.renderFooter())
	if !strings.Contains(plain, "copied 2 lines") {
		t.Fatalf("flash footer = %q, want flash message", plain)
	}
}
