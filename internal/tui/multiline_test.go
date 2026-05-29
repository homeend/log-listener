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
