package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
)

func TestEnsureBlocksRecomputesAfterAppend(t *testing.T) {
	m := newModel(100)
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "panic: boom"}}})
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "goroutine 1 [running]:"}}})
	m.ensureBlocks()
	if len(m.blocks) != 1 {
		t.Fatalf("want 1 block, got %d", len(m.blocks))
	}
	if m.blocks[0].Exception == nil || m.blocks[0].Exception.Language != "go" {
		t.Errorf("block not flagged go: %+v", m.blocks[0])
	}
}

func TestAppendSetsBlocksDirty(t *testing.T) {
	m := newModel(100)
	m.ensureBlocks() // clean
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "x"}}})
	if !m.blocksDirty {
		t.Error("appendEvent must set blocksDirty")
	}
}

func TestInExceptionBlock(t *testing.T) {
	m := newModel(100)
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "Traceback (most recent call last):"}}})
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "  File \"a.py\", line 1, in <module>"}}})
	m.ensureBlocks()
	if !m.inExceptionBlock(0) || !m.inExceptionBlock(1) {
		t.Errorf("both rows of a python traceback should be in an exception block")
	}
}

func TestExceptionBarPrependedAndWidthSafe(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "panic: boom"}}})
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "goroutine 1 [running]:"}}})

	view := m.renderStream(m.contentHeight())
	if !strings.Contains(view, "▌") {
		t.Fatalf("expected exception bar glyph in view:\n%s", view)
	}
	for _, ln := range strings.Split(view, "\n") {
		if w := dispWidth(ln); w > m.width {
			t.Errorf("row exceeds width %d (got %d): %q", m.width, w, ln)
		}
	}

	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	m = m2.(*model)
	if strings.Contains(m.renderStream(m.contentHeight()), "▌") {
		t.Errorf("bar should disappear when marks are toggled off")
	}
}

func TestBlockNavigation(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	for _, v := range []string{"head A", "head B", "panic: boom"} {
		m.appendEvent(render.Event{Group: "g", File: "/a.log",
			Rendered: []render.Part{{Type: "text", Value: v}}})
	}
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "goroutine 1 [running]:"}}})
	// blocks: [0,0] head A, [1,1] head B, [2,3] panic (go).

	m.tailMode = false
	m.streamTop = 0
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	m = m2.(*model)
	if m.streamTop != 1 {
		t.Errorf("] from 0 → streamTop %d, want 1", m.streamTop)
	}

	m.tailMode = false
	m.streamTop = 0
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'}'}})
	m = m2.(*model)
	if m.streamTop != 2 {
		t.Errorf("} from 0 → streamTop %d, want 2 (exception head)", m.streamTop)
	}

	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	m = m2.(*model)
	if m.streamTop != 1 {
		t.Errorf("[ from 2 → streamTop %d, want 1", m.streamTop)
	}
}

func TestExceptionBarWidthSafeOnLongLine(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 20, Height: 6})
	m = m2.(*model)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	// A panic head far wider than the 20-col terminal → forces the clip path.
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "panic: " + strings.Repeat("X", 200)}}})
	m.appendEvent(render.Event{Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "  at frame"}}})

	view := m.renderStream(m.contentHeight())
	if !strings.Contains(view, "▌") {
		t.Fatalf("expected the bar on the long exception line:\n%s", view)
	}
	// Every rendered row (clipped long line, padded short line, blank fillers)
	// must be EXACTLY the terminal width — a barred row whose width accounting
	// were wrong would clip to width-2 or overflow to width+1.
	for _, ln := range strings.Split(view, "\n") {
		if w := dispWidth(ln); w != m.width {
			t.Errorf("row should be exactly width %d, got %d: %q", m.width, w, ln)
		}
	}
}
