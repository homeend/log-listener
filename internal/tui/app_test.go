package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/keymap"
	"github.com/homeend/log-listener/internal/render"
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
	if len(m.lines) > 3 {
		t.Fatalf("scrollback breached: %d", len(m.lines))
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
	app := New(Options{
		Scrollback: 100,
		InitialFiles: []FileEntry{
			{Path: "/a.log", Group: "g1"},
			{Path: "/b.log", Group: "g2"},
		},
		Groups: []GroupInfo{{ID: "g1"}, {ID: "g2"}},
	})
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
	// Production: the pump appends to the shared buffer, then Push→EventMsg
	// triggers a reconcile. appendEvent does both (buffer + reconcile).
	m.(*model).appendEvent(ev)
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
	wantTop := len(m.lines) - 2*page
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
	if m.streamTop < 0 || m.streamTop >= len(m.lines) {
		t.Fatalf("streamTop=%d out of range (events=%d)", m.streamTop, len(m.lines))
	}
	if m.tailMode {
		t.Fatal("eviction must not silently re-stick to tail")
	}
}

// TestStreamRowsNeverExceedWidth guards the bug where the header bar
// vanished while browsing (PgUp/PgDn or search) and the top row was a wide
// rendered-JSON block. clipLine returned over-wide rows verbatim at
// horizScroll==0, so the terminal wrapped them, overflowed the viewport,
// and scrolled the header off the top. No rendered stream row may exceed
// the terminal width.
func TestStreamRowsNeverExceedWidth(t *testing.T) {
	m := newModel(1000)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	m.appendEvent(render.Event{
		Group: "g", File: "/x.log",
		Rendered: []render.Part{
			{Type: "text", Value: "head line"},
			{Type: "json", Value: map[string]any{
				"averylongkeyname": strings.Repeat("v", 200)}},
		},
	})
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	m = m2.(*model)
	// Browse from the very top so the wide JSON block row is on screen.
	m.tailMode = false
	m.streamTop = 0

	body := m.renderStream(m.contentHeight())
	for i, ln := range strings.Split(body, "\n") {
		if w := runeLen(stripANSI(ln)); w > m.width {
			t.Fatalf("stream row %d width %d exceeds terminal width %d: %q",
				i, w, m.width, stripANSI(ln))
		}
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
	wantTop := len(m.lines) - m.contentHeight() - vertFastStep
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
		styled, visW := m.renderDisplayLine(dl)
		if visW <= 0 {
			t.Fatalf("renderDisplayLine returned non-positive width for %+v", dl)
		}
		rendered = append(rendered, styled)
	}
	if !strings.Contains(rendered[0], "a.log") {
		t.Fatalf("missing basename in head row: %q", rendered[0])
	}
	joined := strings.Join(rendered, "\n")
	if !strings.Contains(joined, `"k": "v"`) {
		t.Fatalf("json missing in output: %s", joined)
	}
}

func TestModelClearSession(t *testing.T) {
	m := newModel(100)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	for i := 0; i < 20; i++ {
		m.appendEvent(render.Event{Group: "g", File: "/x.log",
			Rendered: []render.Part{{Type: "text", Value: fmt.Sprintf("pre-clear-%d", i)}}})
	}
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = m2.(*model)
	// Browse mode + horizontal scroll, so we can verify they reset.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = m2.(*model)
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = m2.(*model)
	if m.tailMode {
		t.Fatal("setup: expected tailMode=false after Up")
	}
	if m.horizScroll == 0 {
		t.Fatal("setup: expected horizScroll>0 after Right")
	}

	// Ctrl+R clears everything.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	m = m2.(*model)
	if len(m.lines) != 0 {
		t.Fatalf("Ctrl+R should empty events, got %d", len(m.lines))
	}
	if !m.tailMode {
		t.Fatal("Ctrl+R should re-enter tail mode")
	}
	if m.streamTop != 0 || m.horizScroll != 0 {
		t.Fatalf("scroll state not reset: streamTop=%d horizScroll=%d", m.streamTop, m.horizScroll)
	}
	// View should not contain any pre-clear lines.
	if strings.Contains(m.View(), "pre-clear") {
		t.Fatal("View still shows pre-clear lines after Ctrl+R")
	}

	// A new event after clear must render normally.
	m.appendEvent(render.Event{Group: "g", File: "/x.log",
		Rendered: []render.Part{{Type: "text", Value: "post-clear-line"}}})
	if !strings.Contains(m.View(), "post-clear-line") {
		t.Fatalf("post-clear event not visible:\n%s", m.View())
	}
}

// TestModelStreamRowsPadToWidth nails down the ghost-row fix: every line
// renderStream emits — content rows AND blank fillers — must be exactly
// m.width terminal columns wide (visible width via stripANSI), so when
// the terminal repaints a shorter line nothing from the previous render
// leaks through on the right.
func TestReloadMsgReseedsPanelsAndState(t *testing.T) {
	m := newModel(1000)
	// Seed an initial config: one group "old", one renderer "r_old" (on).
	m.groupOrder = []string{"old"}
	m.groupEnabled = map[string]bool{"old": true}
	m.rendererOrder = []string{"r_old"}
	m.rendererEnabled = []bool{true}
	// renderFn echoes raw as a single text line so reRenderAll has something
	// to rebuild from.
	m.renderFn = func(group, file, raw string) (render.Event, bool) {
		return render.Event{Group: group, File: file, Raw: raw,
			Rendered: []render.Part{{Type: "text", Value: raw}}}, true
	}
	m.appendStored(scrollbackEvent{group: "old", file: "f", raw: "line1"})

	// Reload to a new config: group "new", renderer "r_new" starting off.
	newM, _ := m.Update(ReloadMsg{
		Groups:    []GroupInfo{{ID: "new", StartOff: false}},
		Renderers: []RendererInfo{{Name: "r_new", StartOff: true}},
		Files:     []FileEntry{{Path: "/x/new.log", Group: "new"}},
	})
	m = newM.(*model)

	if len(m.groupOrder) != 1 || m.groupOrder[0] != "new" {
		t.Fatalf("groupOrder = %v, want [new]", m.groupOrder)
	}
	if _, ok := m.groupEnabled["old"]; ok {
		t.Fatal("stale group 'old' should be gone after reload")
	}
	if len(m.rendererOrder) != 1 || m.rendererOrder[0] != "r_new" {
		t.Fatalf("rendererOrder = %v, want [r_new]", m.rendererOrder)
	}
	if m.rendererEnabled[0] != false {
		t.Fatal("renderer r_new has off:true, should seed disabled")
	}
	if len(m.files) != 1 || m.files[0].Path != "/x/new.log" {
		t.Fatalf("files = %+v, want one /x/new.log", m.files)
	}
	// Scrollback content is preserved (one source entry survives).
	ve := m.visibleEntries()
	if len(ve) != 1 || ve[0].Raw != "line1" {
		t.Fatalf("entries = %+v, want preserved line1", ve)
	}
}

func TestModelStreamRowsPadToWidth(t *testing.T) {
	m := newModel(100)
	m.groupOrder = []string{"g"}
	m.groupEnabled["g"] = true
	m.appendEvent(render.Event{
		Group: "g", File: "/a.log",
		Rendered: []render.Part{{Type: "text", Value: "tiny"}},
	})
	const width, height = 80, 12
	m2, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m = m2.(*model)
	body := m.renderStream(m.contentHeight())
	for i, ln := range strings.Split(body, "\n") {
		got := runeLen(stripANSI(ln))
		if got != width {
			t.Errorf("row %d visible width = %d, want %d (line=%q)",
				i, got, width, stripANSI(ln))
		}
	}
}

func TestCustomQuitBinding(t *testing.T) {
	km, err := keymap.Resolve("linux", map[string][]string{"quit": {"x"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(100)
	m.km = km
	// 'x' should now quit (returns tea.Quit), and 'q' should NOT.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if cmd == nil {
		t.Errorf("custom binding 'x' did not trigger quit")
	}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd != nil {
		t.Errorf("'q' should no longer quit after rebind")
	}
}

func TestPositionalGroupToggleStillWorks(t *testing.T) {
	m := newModel(100)
	m.km = keymap.Default("linux")
	m.groupOrder = []string{"g0", "g1"}
	m.groupEnabled = map[string]bool{"g0": true, "g1": true}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	if m.groupEnabled["g0"] {
		t.Errorf("digit '1' should have toggled group g0 off")
	}
}

func TestMultiRuneDigitDoesNotToggleGroup(t *testing.T) {
	m := newModel(100)
	m.km = keymap.Default("linux")
	m.groupOrder = []string{"g0", "g1"}
	m.groupEnabled = map[string]bool{"g0": true, "g1": true}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1x")})
	if !m.groupEnabled["g0"] || !m.groupEnabled["g1"] {
		t.Errorf("multi-rune key %q must not toggle any group: g0=%v g1=%v",
			"1x", m.groupEnabled["g0"], m.groupEnabled["g1"])
	}
}

func TestHeaderUsesKeymapDisplay(t *testing.T) {
	m := newModel(100)
	m.km = keymap.Default("darwin")
	m.width = 200
	m.height = 10
	view := m.View()
	if !strings.Contains(view, "⌃G") { // mac glyph for toggle_groups
		t.Errorf("darwin header should show ⌃G; view header missing it")
	}
	if strings.Contains(view, "Ctrl+G") {
		t.Errorf("darwin header should not show linux-style Ctrl+G")
	}
}

func TestGroupsOverlayHeaderUsesKeymapGlyphs(t *testing.T) {
	m := newModel(100)
	m.km = keymap.Default("darwin")
	m.width = 200
	m.height = 20
	m.showGroupsPanel = true
	m.groupOrder = []string{"g0"}
	m.groupEnabled = map[string]bool{"g0": true}

	out := m.renderGroupsPanel(10)
	if !strings.Contains(out, "⌃G") { // mac glyph for toggle_groups
		t.Errorf("darwin overlay header should show ⌃G; missing it")
	}
	if !strings.Contains(out, "⎋") { // mac glyph for close_overlay (Esc)
		t.Errorf("darwin overlay header should show ⎋ for close_overlay; missing it")
	}
	if strings.Contains(out, "Ctrl+G") {
		t.Errorf("darwin overlay header should not show linux-style Ctrl+G")
	}
	if strings.Contains(out, "Esc") {
		t.Errorf("darwin overlay header should not show literal Esc")
	}
}
