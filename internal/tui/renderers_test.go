package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
)

// fakePipeline records SetRendererOn calls and produces a configurable
// re-rendering of raw lines so tests can verify the TUI's
// toggle-and-re-render path without dragging in the real pipeline.
type fakePipeline struct {
	enabled []bool
	// renderFn is called by RenderFn; defaults to "emit one head with
	// raw text as body". Tests can swap it to simulate JSON expansion
	// or drop.
	renderFn func(group, file, raw string) (render.Event, bool)
}

func (fp *fakePipeline) toggle(idx int, on bool) {
	if idx < 0 || idx >= len(fp.enabled) {
		return
	}
	fp.enabled[idx] = on
}

func (fp *fakePipeline) render(group, file, raw string) (render.Event, bool) {
	if fp.renderFn != nil {
		return fp.renderFn(group, file, raw)
	}
	return render.Event{
		Group:    group,
		File:     file,
		Raw:      raw,
		Rendered: []render.Part{{Type: "text", Value: raw}},
	}, true
}

func newRendererTestApp(t *testing.T, renderers []RendererInfo, fp *fakePipeline) *model {
	t.Helper()
	app := New(Options{
		Scrollback: 100,
		Groups:     []GroupInfo{{ID: "g"}},
		Renderers:  renderers,
		SetRendererOn: func(i int, on bool) {
			fp.toggle(i, on)
		},
		RenderFn: fp.render,
	})
	// Reach into the model via the same trick TestNewSeedsInitialFiles
	// uses: New seeded a fresh model; we need a handle to it. Build a
	// parallel one and copy the wiring.
	_ = app
	m := newModel(100)
	for _, r := range renderers {
		m.rendererOrder = append(m.rendererOrder, r.Name)
		m.rendererEnabled = append(m.rendererEnabled, !r.StartOff)
	}
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	m.setRendererEnabled = func(i int, on bool) { fp.toggle(i, on) }
	m.renderFn = fp.render
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	return m2.(*model)
}

func TestModelShiftDigitTogglesRenderer(t *testing.T) {
	fp := &fakePipeline{enabled: []bool{true, true}}
	m := newRendererTestApp(t,
		[]RendererInfo{{Name: "first"}, {Name: "second"}}, fp)

	// '!' toggles renderer 0 off.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	m = m2.(*model)
	if fp.enabled[0] != false {
		t.Fatalf("! must disable pipeline renderer 0, fp.enabled=%v", fp.enabled)
	}
	if m.rendererEnabled[0] != false {
		t.Fatalf("! must mirror state in m.rendererEnabled, got %v", m.rendererEnabled)
	}

	// '@' toggles renderer 1 off.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'@'}})
	m = m2.(*model)
	if fp.enabled[1] != false {
		t.Fatalf("@ must disable pipeline renderer 1, fp.enabled=%v", fp.enabled)
	}

	// '!' again flips renderer 0 back on.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	m = m2.(*model)
	if !fp.enabled[0] {
		t.Fatalf("! again must re-enable renderer 0, fp.enabled=%v", fp.enabled)
	}
}

func TestModelRendererToggleReRendersScrollback(t *testing.T) {
	// Pipeline: when renderer 0 is on, expand into 1 head + 2 block lines;
	// when off, drop to 1 head.
	fp := &fakePipeline{enabled: []bool{true}}
	fp.renderFn = func(group, file, raw string) (render.Event, bool) {
		ev := render.Event{Group: group, File: file, Raw: raw,
			Rendered: []render.Part{{Type: "text", Value: raw}}}
		if fp.enabled[0] {
			ev.Rendered = append(ev.Rendered, render.Part{
				Type:  "json",
				Value: map[string]string{"k1": "v1", "k2": "v2"},
			})
		}
		return ev, true
	}
	m := newRendererTestApp(t,
		[]RendererInfo{{Name: "json-expander"}}, fp)

	// Seed via the full path so an entry is created.
	ev, _ := fp.render("g", "/x.log", "line-A")
	m.appendEvent(ev)
	if len(m.visibleEntries()) != 1 {
		t.Fatalf("want 1 entry, got %d", len(m.visibleEntries()))
	}
	preToggleLines := len(m.lines)
	if preToggleLines < 2 {
		t.Fatalf("pipeline should expand to >=2 lines pre-toggle, got %d", preToggleLines)
	}

	// Toggle renderer 0 off — re-render should collapse the entry.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	m = m2.(*model)
	if len(m.lines) >= preToggleLines {
		t.Fatalf("toggling renderer off should shrink line count: pre=%d post=%d",
			preToggleLines, len(m.lines))
	}
	if len(m.visibleEntries()) != 1 {
		t.Fatal("toggling must not delete the source entry")
	}

	// Toggle back on — line count restored.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	m = m2.(*model)
	if len(m.lines) != preToggleLines {
		t.Fatalf("re-enabling should restore line count: pre=%d restored=%d",
			preToggleLines, len(m.lines))
	}
}

func TestModelRenderersPanelOpensAndLists(t *testing.T) {
	fp := &fakePipeline{enabled: []bool{true, true}}
	m := newRendererTestApp(t,
		[]RendererInfo{{Name: "alpha"}, {Name: "beta"}}, fp)

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	m = m2.(*model)
	if !m.showRenderersPanel {
		t.Fatal("Ctrl+E must open the renderers panel")
	}
	view := m.View()
	if !strings.Contains(view, "alpha") || !strings.Contains(view, "beta") {
		t.Fatalf("panel must list both renderer names:\n%s", view)
	}
	if !strings.Contains(view, "[!]") || !strings.Contains(view, "[@]") {
		t.Fatalf("panel must show shift-digit chars:\n%s", view)
	}
	if !strings.Contains(view, "ON") {
		t.Fatalf("panel must show ON marker:\n%s", view)
	}

	// Toggling from inside the panel should still work — ! reaches the
	// key dispatcher even with the panel open.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	m = m2.(*model)
	if !strings.Contains(m.View(), "OFF") {
		t.Fatalf("panel should show OFF after ! toggle:\n%s", m.View())
	}

	// Esc closes.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = m2.(*model)
	if m.showRenderersPanel {
		t.Fatal("Esc must close the renderers panel")
	}
}

func TestModelRendererStartOffSeed(t *testing.T) {
	fp := &fakePipeline{enabled: []bool{false}}
	m := newRendererTestApp(t,
		[]RendererInfo{{Name: "starts-off", StartOff: true}}, fp)
	if m.rendererEnabled[0] != false {
		t.Fatalf("StartOff=true must seed rendererEnabled[0]=false, got %v", m.rendererEnabled)
	}
	// Open panel, must show OFF.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	m = m2.(*model)
	if !strings.Contains(m.View(), "OFF") {
		t.Fatalf("panel must show OFF for StartOff renderer:\n%s", m.View())
	}
}

func TestModelFooterShowsRendererStat(t *testing.T) {
	fp := &fakePipeline{enabled: []bool{true, true, true}}
	m := newRendererTestApp(t,
		[]RendererInfo{{Name: "r1"}, {Name: "r2"}, {Name: "r3"}}, fp)
	view := m.View()
	if !strings.Contains(view, "rend: 3") {
		t.Fatalf("footer missing renderer count:\n%s", view)
	}
	// Disable one.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'@'}})
	m = m2.(*model)
	view = m.View()
	if !strings.Contains(view, "rend: 3 (1 off)") {
		t.Fatalf("footer should report 1-off after @:\n%s", view)
	}
}

func TestModelRendererToggleClampsSearchHit(t *testing.T) {
	// Pipeline: when on, each line produces 1+5=6 displayLines via blocks.
	// When off, just 1. Hit at line 12 should clamp when scrollback shrinks.
	fp := &fakePipeline{enabled: []bool{true}}
	fp.renderFn = func(group, file, raw string) (render.Event, bool) {
		ev := render.Event{Group: group, File: file, Raw: raw,
			Rendered: []render.Part{{Type: "text", Value: raw}}}
		if fp.enabled[0] {
			ev.Rendered = append(ev.Rendered, render.Part{
				Type:  "json",
				Value: map[string]int{"a": 1, "b": 2, "c": 3},
			})
		}
		return ev, true
	}
	m := newRendererTestApp(t,
		[]RendererInfo{{Name: "expander"}}, fp)
	for i := 0; i < 3; i++ {
		ev, _ := fp.render("g", "/x.log", "line-"+string(rune('A'+i)))
		m.appendEvent(ev)
	}
	preLines := len(m.lines)
	// Park a search hit near the end of the buffer.
	m.setSearchHitRow(preLines - 1)

	// Toggle off — line count collapses; searchHit should clamp.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	m = m2.(*model)
	if len(m.lines) >= preLines {
		t.Fatalf("expected shrink, got pre=%d post=%d", preLines, len(m.lines))
	}
	if m.searchHitRow() != -1 && m.searchHitRow() >= len(m.lines) {
		t.Fatalf("searchHit not clamped: %d, len(lines)=%d", m.searchHitRow(), len(m.lines))
	}
}

func TestModelDollarRemoved(t *testing.T) {
	// Before this feature, $ jumped to the widest line's right edge.
	// After the renderer toggle change, $ toggles renderer 4 instead
	// and never adjusts horizScroll.
	fp := &fakePipeline{enabled: []bool{true, true, true, true}}
	m := newRendererTestApp(t,
		[]RendererInfo{{Name: "r1"}, {Name: "r2"}, {Name: "r3"}, {Name: "r4"}}, fp)
	startScroll := m.horizScroll
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'$'}})
	m = m2.(*model)
	if m.horizScroll != startScroll {
		t.Fatalf("$ must no longer adjust horizScroll: start=%d after=%d",
			startScroll, m.horizScroll)
	}
	if fp.enabled[3] {
		t.Fatal("$ must toggle renderer index 3 off")
	}
}
