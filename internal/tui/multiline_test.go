package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"log-listener/internal/render"
)

// pushRawLine appends an event that contains exactly one text part with
// the given body. Lets tests construct heads + whitespace-led
// continuations directly without going through a renderer.
func pushRawLine(m *model, group, file, body string) {
	m.appendEvent(render.Event{
		Group: group, File: file, Raw: body,
		Rendered: []render.Part{{Type: "text", Value: body}},
	})
}

func TestModelMultilineCollapseHidesContinuations(t *testing.T) {
	m := newModel(100)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	pushRawLine(m, "g", "/x.log", "ERROR something broke")
	pushRawLine(m, "g", "/x.log", "  at module.func line 42")
	pushRawLine(m, "g", "/x.log", "  at caller line 17")
	pushRawLine(m, "g", "/x.log", "INFO recovered")

	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = m2.(*model)

	// Expanded — all four lines visible.
	view := stripANSI(m.View())
	for _, want := range []string{"ERROR something broke", "at module.func", "at caller", "INFO recovered"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expanded view missing %q:\n%s", want, view)
		}
	}

	// Press 'm' — collapse.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m = m2.(*model)
	if !m.collapseMultiline {
		t.Fatal("m must toggle collapseMultiline on")
	}
	view = stripANSI(m.View())
	if strings.Contains(view, "at module.func") || strings.Contains(view, "at caller") {
		t.Fatalf("collapsed view should hide continuation rows:\n%s", view)
	}
	if !strings.Contains(view, "ERROR something broke [...]") {
		t.Fatalf("head with hidden continuations must get [...] suffix:\n%s", view)
	}
	// The INFO line has no continuations — no suffix.
	if strings.Contains(view, "INFO recovered [...]") {
		t.Fatalf("head with no continuations must NOT get [...]:\n%s", view)
	}
	if !strings.Contains(view, "INFO recovered") {
		t.Fatalf("non-continuation head must still show:\n%s", view)
	}

	// Toggle back — expanded again.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m = m2.(*model)
	view = stripANSI(m.View())
	if !strings.Contains(view, "at module.func") {
		t.Fatalf("expanded view should restore continuations:\n%s", view)
	}
	if strings.Contains(view, "ERROR something broke [...]") {
		t.Fatalf("expanded view must not carry [...] marker:\n%s", view)
	}
}

func TestModelMultilineCollapseHidesJSONBlocks(t *testing.T) {
	m := newModel(100)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	// Event with a JSON part → 1 head + N block lines.
	m.appendEvent(render.Event{
		Group: "g", File: "/x.log", Raw: "raw line",
		Rendered: []render.Part{
			{Type: "text", Value: "STATUS:"},
			{Type: "json", Value: map[string]string{"k1": "v1", "k2": "v2"}},
		},
	})
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = m2.(*model)

	// Expanded: JSON block lines visible.
	view := stripANSI(m.View())
	if !strings.Contains(view, `"k1"`) {
		t.Fatalf("expanded view should show JSON block:\n%s", view)
	}

	// Collapse — JSON block hides, head gets [...] suffix.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m = m2.(*model)
	view = stripANSI(m.View())
	if strings.Contains(view, `"k1"`) {
		t.Fatalf("collapsed view must hide JSON block:\n%s", view)
	}
	if !strings.Contains(view, "STATUS: [...]") {
		t.Fatalf("head must get [...] suffix:\n%s", view)
	}
}

func TestModelMultilineCollapseEmptyBodyNotContinuation(t *testing.T) {
	// A line with empty body should NOT be treated as a continuation
	// (it's not whitespace-led, it's just empty).
	dl := displayLine{group: "g", body: "", bodyWidth: 0}
	if isContinuation(dl) {
		t.Fatal("empty-body line must not be a continuation")
	}
	// A line starting with a normal character — not a continuation.
	dl.body = "hello"
	if isContinuation(dl) {
		t.Fatal("normal-body line must not be a continuation")
	}
	// Tab-led — continuation.
	dl.body = "\there"
	if !isContinuation(dl) {
		t.Fatal("tab-led line must be a continuation")
	}
	// Space-led — continuation.
	dl.body = " here"
	if !isContinuation(dl) {
		t.Fatal("space-led line must be a continuation")
	}
	// isBlock — continuation regardless of body.
	dl = displayLine{group: "g", body: "{", isBlock: true, bodyWidth: 1}
	if !isContinuation(dl) {
		t.Fatal("block line must be a continuation")
	}
}

func TestModelMultilineCollapseLastLineNoSuffix(t *testing.T) {
	// Head with a continuation that's the LAST entry — head still gets
	// [...] (the continuation is hidden but exists).
	// Head with NO continuation after it (last entry) — no [...].
	m := newModel(100)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	pushRawLine(m, "g", "/x.log", "single line")
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m = m2.(*model)
	view := stripANSI(m.View())
	if strings.Contains(view, "[...]") {
		t.Fatalf("lone head must not show [...] in collapsed mode:\n%s", view)
	}
}

// TestDecomposeNeverLeavesEmbeddedNewline guards the bug where a renderer
// emitted a text part containing a '\n' (e.g. idea-trailing-json's
// "$1\njson($2)" template when $2 is not valid JSON and json() falls back to
// text). The embedded newline ended up inside a single displayLine.body,
// which then rendered as multiple terminal rows — breaking the one-row-per-
// displayLine invariant (header overflow, broken horizontal scroll).
func TestDecomposeNeverLeavesEmbeddedNewline(t *testing.T) {
	// Mirrors the fallback shape: "$1\n" text part + non-JSON "{...}" text part.
	ev := render.Event{Group: "goland", File: "/idea.log", Rendered: []render.Part{
		{Type: "text", Value: "2026 INFO Saved path macros: \n"},
		{Type: "text", Value: "{DB_ARTIFACTS_BUNDLE=C:\\x\\artifacts}"},
	}}
	lines := decomposeEvent(ev)
	for i, dl := range lines {
		if strings.Contains(dl.body, "\n") {
			t.Fatalf("displayLine[%d].body has an embedded newline: %q", i, dl.body)
		}
	}
	if len(lines) < 2 {
		t.Fatalf("expected the line to split into >=2 display rows, got %d", len(lines))
	}
}

func TestEmbeddedNewlineKeepsHeaderRow(t *testing.T) {
	m := newModel(1000)
	m.groupOrder = []string{"goland"}
	m.groupEnabled["goland"] = true
	m.appendEvent(render.Event{Group: "goland", File: "/idea.log", Rendered: []render.Part{
		{Type: "text", Value: "INFO Saved path macros: \n"},
		{Type: "text", Value: "{DB_ARTIFACTS_BUNDLE=C:\\x\\artifacts}"},
	}})
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	m = m2.(*model)
	rows := strings.Split(m.View(), "\n")
	if len(rows) != 10 {
		t.Fatalf("View must be exactly height(10) rows, got %d", len(rows))
	}
	if !strings.Contains(rows[0], "log-listener") {
		t.Fatalf("header row missing/overflowed: %q", rows[0])
	}
}
