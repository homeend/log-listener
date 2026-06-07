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
	// Indices: 0 single, [1,2] multi (non-exception), 3 single, [4,5] multi
	// (exception). ]/[ must hop between the two MULTI-LINE blocks and skip the
	// single-line entries; }/{ must hit only the exception block.
	for _, v := range []string{
		"single line one",
		"config dump:",
		"  key=val",
		"single line two",
		"panic: boom",
		"goroutine 1 [running]:",
	} {
		m.appendEvent(render.Event{Group: "g", File: "/a.log",
			Rendered: []render.Part{{Type: "text", Value: v}}})
	}

	m.tailMode = false
	m.streamTop = 0
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	m = m2.(*model)
	if m.streamTop != 1 {
		t.Errorf("] from 0 → streamTop %d, want 1 (config-dump head)", m.streamTop)
	}
	// Next ] skips the single line at index 3 and lands on the exception block.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	m = m2.(*model)
	if m.streamTop != 4 {
		t.Errorf("] from 1 → streamTop %d, want 4 (skips single line at 3)", m.streamTop)
	}

	m.tailMode = false
	m.streamTop = 0
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'}'}})
	m = m2.(*model)
	if m.streamTop != 4 {
		t.Errorf("} from 0 → streamTop %d, want 4 (exception head)", m.streamTop)
	}

	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}})
	m = m2.(*model)
	if m.streamTop != 1 {
		t.Errorf("[ from 4 → streamTop %d, want 1 (prev multi-line block)", m.streamTop)
	}
}

// TestNavSkipsSingleLineLogEntries is a regression guard built from a real
// GoLand idea.log capture (saved via the `S` export): a run of single-line INFO
// entries with one multi-line entry in the middle — an INFO head followed by
// indented idea.*.path= lines. `]` must land on that multi-line entry's head
// and skip the single-line INFO entries; the entry is NOT an exception, so `}`
// finds nothing.
func TestNavSkipsSingleLineLogEntries(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	m = m2.(*model)
	m.groupOrder = []string{"goland"}
	m.groupEnabled["goland"] = true
	rows := []string{
		"2026-06-07 10:43:50,926 [  61216]   INFO - QuotaManager - Updating quota refill state",
		"2026-06-07 10:45:03,851 [     48]   INFO - AppStarter - boot library path: C:\\GoLand\\jbr\\bin",
		"2026-06-07 10:45:03,855 [     52]   INFO - AppStarter - locale=pl_PL JNU=UTF-8 file.encoding=UTF-8",
		"    idea.home.path=C:\\Users\\homee\\AppData\\Local\\Programs\\GoLand",
		"    idea.config.path=C:\\Users\\homee\\AppData\\Roaming\\JetBrains\\GoLand2026.1",
		"    idea.system.path=C:\\Users\\homee\\AppData\\Local\\JetBrains\\GoLand2026.1",
		"    idea.plugins.path=C:\\Users\\homee\\AppData\\Roaming\\JetBrains\\GoLand2026.1\\plugins",
		"    idea.log.path=C:\\Users\\homee\\AppData\\Local\\JetBrains\\GoLand2026.1\\log",
		"2026-06-07 10:45:03,857 [     54]   INFO - AppStarter - CPU cores: 16",
	}
	for _, v := range rows {
		m.appendEvent(render.Event{Group: "goland", File: "/idea.log",
			Rendered: []render.Part{{Type: "text", Value: v}}})
	}
	// The only multi-line block is the locale= entry (index 2) + its five
	// indented continuations (indices 3-7).
	m.tailMode = false
	m.streamTop = 0
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	m = m2.(*model)
	if m.streamTop != 2 {
		t.Errorf("] should land on the multi-line entry head (index 2), got %d", m.streamTop)
	}
	// No further multi-line block → another ] is a no-op (stays at 2).
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	m = m2.(*model)
	if m.streamTop != 2 {
		t.Errorf("] past the last multi-line block should stay put, got %d", m.streamTop)
	}
	// The config dump is not an exception → it is not in an exception block,
	// and } finds nothing.
	if m.inExceptionBlock(2) {
		t.Errorf("the config-dump block must not be flagged as an exception")
	}
	m.streamTop = 0
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'}'}})
	m = m2.(*model)
	if m.streamTop != 0 {
		t.Errorf("} should find no exception block here (stay at 0), got %d", m.streamTop)
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
