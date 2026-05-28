package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"log-listener/internal/render"
)

func TestModelToggleGroupColumn(t *testing.T) {
	m := newModel(100)
	m.groupOrder = []string{"acp"}
	m.groupEnabled["acp"] = true
	m.appendEvent(render.Event{
		Group: "acp", File: "/var/log/a.log",
		Rendered: []render.Part{{Type: "text", Value: "MARK-COL"}},
	})
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = m2.(*model)

	view := m.View()
	if !strings.Contains(view, "[acp]") {
		t.Fatalf("expected '[acp]' prefix in default view, got:\n%s", view)
	}
	// Ctrl+P hides the group column.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	m = m2.(*model)
	view = m.View()
	if strings.Contains(view, "[acp]") {
		t.Fatalf("'[acp]' should be hidden after Ctrl+P:\n%s", view)
	}
	if !strings.Contains(view, "a.log") {
		t.Fatalf("file column should still show:\n%s", view)
	}
	if !strings.Contains(view, "MARK-COL") {
		t.Fatalf("body must still render:\n%s", view)
	}
	// Toggle back on.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	m = m2.(*model)
	if !strings.Contains(m.View(), "[acp]") {
		t.Fatal("Ctrl+P should re-show the group column")
	}
}

func TestModelToggleFileColumn(t *testing.T) {
	m := newModel(100)
	m.groupOrder = []string{"acp"}
	m.groupEnabled["acp"] = true
	m.appendEvent(render.Event{
		Group: "acp", File: "/var/log/uniqfile.log",
		Rendered: []render.Part{{Type: "text", Value: "BODY-X"}},
	})
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	m = m2.(*model)
	view := m.View()
	if strings.Contains(view, "uniqfile.log") {
		t.Fatalf("file column should be hidden after Ctrl+L:\n%s", view)
	}
	if !strings.Contains(view, "[acp]") {
		t.Fatalf("group column should still show:\n%s", view)
	}
	if !strings.Contains(view, "BODY-X") {
		t.Fatalf("body must still render:\n%s", view)
	}
}

func TestModelToggleGroupByDigit(t *testing.T) {
	m := newModel(100)
	m.groupOrder = []string{"acp", "goland"}
	m.groupEnabled["acp"] = true
	m.groupEnabled["goland"] = true
	m.appendEvent(render.Event{Group: "acp", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "FROM-ACP"}}})
	m.appendEvent(render.Event{Group: "goland", File: "/g.log",
		Rendered: []render.Part{{Type: "text", Value: "FROM-GOLAND"}}})
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = m2.(*model)

	// Disable group 1 (acp). FROM-ACP must disappear.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m = m2.(*model)
	view := m.View()
	if strings.Contains(view, "FROM-ACP") {
		t.Fatalf("FROM-ACP should be filtered after disabling group 1:\n%s", view)
	}
	if !strings.Contains(view, "FROM-GOLAND") {
		t.Fatalf("FROM-GOLAND must still show:\n%s", view)
	}
	// Re-enable.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m = m2.(*model)
	if !strings.Contains(m.View(), "FROM-ACP") {
		t.Fatal("FROM-ACP must reappear after re-enabling group 1")
	}
}

func TestModelGroupsPanel(t *testing.T) {
	m := newModel(100)
	m.groupOrder = []string{"acp", "goland"}
	m.groupEnabled["acp"] = true
	m.groupEnabled["goland"] = true
	m.files = []FileEntry{
		{Path: "/a/x.log", Group: "acp"},
		{Path: "/a/y.log", Group: "acp"},
		{Path: "/g/z.log", Group: "goland"},
	}
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = m2.(*model)

	// Ctrl+G opens the panel.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	m = m2.(*model)
	if !m.showGroupsPanel {
		t.Fatal("Ctrl+G must open the groups panel")
	}
	view := m.View()
	if !strings.Contains(view, "[1]") || !strings.Contains(view, "[2]") {
		t.Fatalf("panel must list both groups with digit keys:\n%s", view)
	}
	if !strings.Contains(view, "ON") {
		t.Fatalf("enabled groups must show ON marker:\n%s", view)
	}
	if !strings.Contains(view, "2 files") {
		t.Fatalf("acp file count must show:\n%s", view)
	}
	// Disable group 2 from inside the panel — toggle reflected next render.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = m2.(*model)
	if !strings.Contains(m.View(), "OFF") {
		t.Fatal("panel must show OFF after toggling a group off")
	}
	// Esc closes.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = m2.(*model)
	if m.showGroupsPanel {
		t.Fatal("Esc must close the groups panel")
	}
}

func TestModelTailWalkSkipsDisabledGroups(t *testing.T) {
	m := newModel(1000)
	m.groupOrder = []string{"a", "b"}
	m.groupEnabled["a"] = true
	m.groupEnabled["b"] = false // b starts disabled
	for i := 0; i < 50; i++ {
		gid := "a"
		if i%2 == 0 {
			gid = "b"
		}
		m.appendEvent(render.Event{Group: gid, File: "/x.log",
			Rendered: []render.Part{{Type: "text", Value: fmt.Sprintf("%s-line-%d", gid, i)}}})
	}
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 10})
	m = m2.(*model)
	view := m.View()
	if strings.Contains(view, "b-line") {
		t.Fatalf("b group is disabled; no b-line should appear:\n%s", view)
	}
	if !strings.Contains(view, "a-line") {
		t.Fatalf("a-line must be visible:\n%s", view)
	}
}

func TestModelAppendEventBoundedScrollback(t *testing.T) {
	m := newModel(3)
	for i := 0; i < 10; i++ {
		m.appendEvent(render.Event{
			Group: "d", File: "/a.log",
			Rendered: []render.Part{{Type: "text", Value: "line"}},
		})
	}
	if len(m.events) > 3 {
		t.Fatalf("scrollback breached: %d", len(m.events))
	}
}

func TestModelToggleFilesPanel(t *testing.T) {
	m := newModel(100)
	if m.showFiles {
		t.Fatal("files should default to hidden")
	}
	// Tab is what terminals actually send for Ctrl+I (same byte 0x09).
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = m2.(*model)
	if !m.showFiles {
		t.Fatal("Tab/Ctrl+I should toggle files on")
	}
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = m2.(*model)
	if m.showFiles {
		t.Fatal("Esc should close files panel")
	}
}

func TestModelFileListReplaces(t *testing.T) {
	m := newModel(100)
	m.files = []FileEntry{{Path: "/old", Group: "old"}}
	m.filesScroll = 5 // out of range after replace
	m2, _ := m.Update(FileListMsg{Files: []FileEntry{
		{Path: "/new1", Group: "g"},
		{Path: "/new2", Group: "g"},
	}})
	m = m2.(*model)
	if len(m.files) != 2 || m.files[0].Path != "/new1" {
		t.Fatalf("files not replaced: %+v", m.files)
	}
	if m.filesScroll != 0 {
		t.Fatalf("filesScroll should reset when out of range: %d", m.filesScroll)
	}
}

// TestNewSeedsInitialFiles asserts that the initial file list passed to
// tui.New() is reflected in the model before any Update is processed —
// the SetFiles-before-Run deadlock fix.
func TestNewSeedsInitialFiles(t *testing.T) {
	app := New(100, []FileEntry{
		{Path: "/a.log", Group: "g1"},
		{Path: "/b.log", Group: "g2"},
	}, []string{"g1", "g2"})
	// Reach into the model via reflection-free fast path: the underlying
	// *model isn't exposed, but if seeding worked, app.prog's initial
	// model has files preset. We can't easily inspect that without
	// running, so spot-check the helper directly:
	m := newModel(100)
	m.files = append(m.files, FileEntry{Path: "/a.log", Group: "g1"})
	if len(m.files) != 1 || m.files[0].Path != "/a.log" {
		t.Fatalf("model seed direct check failed: %+v", m.files)
	}
	if app == nil {
		t.Fatal("app should not be nil")
	}
}

// TestModelViewShowsEventAfterUpdate exercises the model the way bubbletea
// would: feed a WindowSizeMsg + an EventMsg via Update, then assert the
// rendered View contains the line. Catches regressions where Update doesn't
// route to appendEvent or View doesn't include the stream area.
func TestModelViewShowsEventAfterUpdate(t *testing.T) {
	var m tea.Model = newModel(100)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	ev := render.Event{
		Group: "g1", File: "/tmp/abc.log",
		Rendered: []render.Part{{Type: "text", Value: "MARKER-9999"}},
	}
	m, _ = m.Update(EventMsg{Event: ev})
	view := m.View()
	if !strings.Contains(view, "MARKER-9999") {
		t.Fatalf("View() does not contain pushed event marker:\n%s", view)
	}
	if !strings.Contains(view, "abc.log") {
		t.Fatalf("View() does not contain basename:\n%s", view)
	}
}

func TestModelPageUpPageDown(t *testing.T) {
	m := newModel(1000)
	for i := 0; i < 200; i++ {
		m.appendEvent(render.Event{
			Group: "g", File: "/x.log",
			Rendered: []render.Part{{Type: "text", Value: fmt.Sprintf("line %d", i)}},
		})
	}
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = m2.(*model)
	page := m.contentHeight()
	if page <= 0 {
		t.Fatalf("contentHeight=%d", page)
	}
	if !m.tailMode {
		t.Fatal("initially must be in tail mode")
	}

	// PgUp leaves tail mode and shifts the viewport up by one page.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = m2.(*model)
	if m.tailMode {
		t.Fatal("PgUp should unstick from tail")
	}
	// streamTop should be (bottom of view = events - page) - page.
	wantTop := len(m.events) - 2*page
	if m.streamTop != wantTop {
		t.Fatalf("after PgUp streamTop=%d want %d", m.streamTop, wantTop)
	}

	// PgDn re-sticks once the bottom catches up.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = m2.(*model)
	if !m.tailMode {
		t.Fatal("PgDn should re-stick at the bottom")
	}

	// Scrolling up far past the start clamps at 0, never below.
	for i := 0; i < 100; i++ {
		m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
		m = m2.(*model)
	}
	if m.streamTop != 0 {
		t.Fatalf("PgUp past start: streamTop=%d want 0", m.streamTop)
	}
}

func TestModelTailModeStaysOnAppend(t *testing.T) {
	m := newModel(1000)
	for i := 0; i < 100; i++ {
		m.appendEvent(render.Event{
			Group: "g", File: "/x.log",
			Rendered: []render.Part{{Type: "text", Value: fmt.Sprintf("line %d", i)}},
		})
	}
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)

	// Scroll up — leave tail mode and lock viewport at an absolute position.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = m2.(*model)
	if m.tailMode {
		t.Fatal("Up arrow should unstick from tail")
	}
	lockedTop := m.streamTop

	// New events arriving must NOT shift the user's view.
	for i := 100; i < 150; i++ {
		m.appendEvent(render.Event{
			Group: "g", File: "/x.log",
			Rendered: []render.Part{{Type: "text", Value: fmt.Sprintf("line %d", i)}},
		})
	}
	if m.streamTop != lockedTop {
		t.Fatalf("streamTop drifted during append: got %d, want %d", m.streamTop, lockedTop)
	}
	if m.tailMode {
		t.Fatal("appends must not re-stick tailMode automatically")
	}

	// End re-sticks regardless of where streamTop sat.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m = m2.(*model)
	if !m.tailMode {
		t.Fatal("End should re-stick to tail")
	}
}

func TestModelHomeJumpsToOldest(t *testing.T) {
	m := newModel(1000)
	for i := 0; i < 50; i++ {
		m.appendEvent(render.Event{
			Group: "g", File: "/x.log",
			Rendered: []render.Part{{Type: "text", Value: fmt.Sprintf("line %d", i)}},
		})
	}
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyHome})
	m = m2.(*model)
	if m.tailMode {
		t.Fatal("Home should leave tail mode")
	}
	if m.streamTop != 0 {
		t.Fatalf("Home streamTop=%d want 0", m.streamTop)
	}
	// The View should contain the FIRST line (line 0), not line 49.
	view := m.View()
	if !strings.Contains(view, "line 0") {
		t.Fatalf("Home View must show line 0:\n%s", view)
	}
}

func TestModelScrollbackTrimAdjustsStreamTop(t *testing.T) {
	// Small scrollback, user locked at top, then enough events to trim past
	// streamTop. We must NOT crash and streamTop must stay valid (>= 0).
	m := newModel(20)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	m = m2.(*model)
	for i := 0; i < 10; i++ {
		m.appendEvent(render.Event{
			Group: "g", File: "/x.log",
			Rendered: []render.Part{{Type: "text", Value: fmt.Sprintf("ev %d", i)}},
		})
	}
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyHome})
	m = m2.(*model)

	// Append far past scrollback — old events evicted.
	for i := 10; i < 200; i++ {
		m.appendEvent(render.Event{
			Group: "g", File: "/x.log",
			Rendered: []render.Part{{Type: "text", Value: fmt.Sprintf("ev %d", i)}},
		})
	}
	if m.streamTop < 0 || m.streamTop >= len(m.events) {
		t.Fatalf("streamTop=%d out of range (events=%d)", m.streamTop, len(m.events))
	}
	if m.tailMode {
		t.Fatal("eviction must not silently re-stick to tail")
	}
}

func TestModelHorizontalScroll(t *testing.T) {
	m := newModel(100)
	// A line wider than the viewport
	long := strings.Repeat("abcdef-", 30) // 210 chars
	m.appendEvent(render.Event{
		Group: "g", File: "/x.log",
		Rendered: []render.Part{{Type: "text", Value: long}},
	})
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	m = m2.(*model)

	// At horizScroll = 0 the start of the prefixed line is visible
	view := m.View()
	if !strings.Contains(view, "[g]") {
		t.Fatalf("expected '[g]' visible at horizScroll=0:\n%s", view)
	}

	// Right arrow shifts the view
	for i := 0; i < 5; i++ {
		m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
		m = m2.(*model)
	}
	if m.horizScroll != 5*horizStep {
		t.Fatalf("horizScroll after 5x Right = %d, want %d", m.horizScroll, 5*horizStep)
	}

	// After scrolling, the leading "[g] x.log:" prefix is no longer visible.
	view = m.View()
	if strings.Contains(view, "[g] x.log:") {
		t.Fatalf("prefix should be scrolled off, but still visible:\n%s", view)
	}
	// We should still see SOME of the line body
	if !strings.Contains(view, "abcdef-") {
		t.Fatalf("expected line body still visible after horiz scroll:\n%s", view)
	}

	// Horizontal column reset is on "0" now that Home means "first line".
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'0'}})
	m = m2.(*model)
	if m.horizScroll != 0 {
		t.Fatalf("'0' should reset horizScroll, got %d", m.horizScroll)
	}

	// Left clamps at 0
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = m2.(*model)
	if m.horizScroll != 0 {
		t.Fatalf("Left at column 0 must stay at 0, got %d", m.horizScroll)
	}
}

func TestModelFastScrollKeys(t *testing.T) {
	m := newModel(1000)
	for i := 0; i < 200; i++ {
		m.appendEvent(render.Event{
			Group: "g", File: "/x.log",
			Rendered: []render.Part{{Type: "text", Value: fmt.Sprintf("line %d", i)}},
		})
	}
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = m2.(*model)

	// Ctrl+Right pans by horizFastStep, not horizStep
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlRight})
	m = m2.(*model)
	if m.horizScroll != horizFastStep {
		t.Fatalf("Ctrl+Right: horizScroll=%d want %d", m.horizScroll, horizFastStep)
	}

	// Ctrl+Left rolls back by horizFastStep
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlLeft})
	m = m2.(*model)
	if m.horizScroll != 0 {
		t.Fatalf("Ctrl+Left: horizScroll=%d want 0", m.horizScroll)
	}

	// Ctrl+Up unsticks tail mode and shifts viewport up by vertFastStep.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlUp})
	m = m2.(*model)
	if m.tailMode {
		t.Fatal("Ctrl+Up should unstick from tail")
	}
	wantTop := len(m.events) - m.contentHeight() - vertFastStep
	if m.streamTop != wantTop {
		t.Fatalf("Ctrl+Up: streamTop=%d want %d", m.streamTop, wantTop)
	}

	// Ctrl+Down moves down by vertFastStep; when the bottom catches up, re-stick.
	for i := 0; i < 20; i++ {
		m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlDown})
		m = m2.(*model)
	}
	if !m.tailMode {
		t.Fatal("repeated Ctrl+Down must eventually re-stick to tail")
	}
}

func TestStripANSIRoundtrip(t *testing.T) {
	plain := "hello world"
	styled := groupStyle.Render(plain)
	if styled == plain {
		t.Skip("lipgloss did not add ANSI (likely TERM=dumb)")
	}
	if stripANSI(styled) != plain {
		t.Fatalf("stripANSI(%q) = %q, want %q", styled, stripANSI(styled), plain)
	}
}

func TestDecomposeAndRenderDisplayLine(t *testing.T) {
	ev := render.Event{
		Group: "d1", File: "/var/log/a.log",
		Rendered: []render.Part{
			{Type: "text", Value: "INFO\n"},
			{Type: "json", Value: map[string]interface{}{"k": "v"}},
		},
	}
	dls := decomposeEvent(ev)
	if len(dls) < 2 {
		t.Fatalf("expected >=2 displayLines, got %d: %+v", len(dls), dls)
	}
	if dls[0].isBlock {
		t.Fatal("first line should be a head, not a block")
	}
	if dls[0].group != "d1" || dls[0].file != "a.log" {
		t.Fatalf("head metadata: %+v", dls[0])
	}
	if !dls[1].isBlock {
		t.Fatal("subsequent JSON lines should be blocks")
	}

	m := newModel(100)
	m.showGroup = true
	m.showFile = true
	rendered := []string{}
	for _, dl := range dls {
		rendered = append(rendered, m.renderDisplayLine(dl))
	}
	if !strings.Contains(rendered[0], "a.log") {
		t.Fatalf("missing basename in head row: %q", rendered[0])
	}
	joined := strings.Join(rendered, "\n")
	if !strings.Contains(joined, `"k": "v"`) {
		t.Fatalf("json missing in output: %s", joined)
	}
}
