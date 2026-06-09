package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/homeend/log-listener/internal/render"
)

func TestPlainExportLine(t *testing.T) {
	head := displayLine{group: "acp", file: "a.log", body: "hello world", bodyWidth: 11}
	if got := plainExportLine(head); got != "[acp] a.log: hello world" {
		t.Errorf("head export = %q", got)
	}
	// Block line: ANSI stripped, no prefix.
	block := displayLine{group: "acp", file: "a.log", body: dimStyle.Render("  at Foo.bar"), isBlock: true}
	if got := plainExportLine(block); got != "  at Foo.bar" {
		t.Errorf("block export = %q (want stripped, unprefixed)", got)
	}
}

func TestSnapshotScrollbackReturnsEveryLine(t *testing.T) {
	m := newModel(100)
	m.appendEvent(render.Event{Group: "g", File: "/x/a.log",
		Rendered: []render.Part{{Type: "text", Value: "LINE-ONE"}}})
	m.appendEvent(render.Event{Group: "g", File: "/x/a.log",
		Rendered: []render.Part{{Type: "text", Value: "LINE-TWO"}}})
	out := m.snapshotScrollback()
	if len(out) != 2 {
		t.Fatalf("want 2 lines, got %d: %v", len(out), out)
	}
	if !strings.Contains(out[0], "LINE-ONE") || !strings.Contains(out[1], "LINE-TWO") {
		t.Errorf("snapshot = %v", out)
	}
	if !strings.HasPrefix(out[0], "[g] a.log: ") {
		t.Errorf("head line missing prefix: %q", out[0])
	}
}

func TestSnapshotViewportMatchesVisible(t *testing.T) {
	m := newModel(100)
	m.height = 4 // contentHeight = 2 rows visible
	m.width = 80
	for i := 0; i < 10; i++ {
		m.appendEvent(render.Event{Group: "g", File: "/x/a.log",
			Rendered: []render.Part{{Type: "text", Value: "ROW"}}})
	}
	out := m.snapshotViewport()
	if len(out) != m.contentHeight() {
		t.Fatalf("viewport snapshot = %d lines, want contentHeight %d", len(out), m.contentHeight())
	}
}

func TestWriteExportNamingAndContent(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 7, 1, 33, 55, 0, time.UTC)

	p1, err := writeExport(dir, []string{"a", "b"}, now)
	if err != nil {
		t.Fatalf("writeExport: %v", err)
	}
	if base := filepath.Base(p1); base != "screen-log-listener-20260607-013355.txt" {
		t.Errorf("base name = %q", base)
	}
	got, _ := os.ReadFile(p1)
	if string(got) != "a\nb\n" {
		t.Errorf("content = %q, want trailing newline", string(got))
	}

	// Same second → numeric suffix, no overwrite.
	p2, err := writeExport(dir, []string{"c"}, now)
	if err != nil {
		t.Fatalf("writeExport 2: %v", err)
	}
	if base := filepath.Base(p2); base != "screen-log-listener-20260607-013355-1.txt" {
		t.Errorf("collision base name = %q", base)
	}
	if first, _ := os.ReadFile(p1); string(first) != "a\nb\n" {
		t.Errorf("first file was clobbered: %q", string(first))
	}
}

func TestSaveKeyWritesFileAndFlashes(t *testing.T) {
	m := newModel(100)
	m.saveDir = t.TempDir()
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = m2.(*model)
	m.appendEvent(render.Event{Group: "g", File: "/x/a.log",
		Rendered: []render.Part{{Type: "text", Value: "SAVED-ROW"}}})

	// Press S (save scrollback) → a non-nil command.
	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	m = m2.(*model)
	if cmd == nil {
		t.Fatal("save key should return a tea.Cmd")
	}

	// Run the command → a saveResultMsg; feed it back.
	msg := cmd()
	res, ok := msg.(saveResultMsg)
	if !ok {
		t.Fatalf("cmd produced %T, want saveResultMsg", msg)
	}
	if res.err != nil {
		t.Fatalf("save failed: %v", res.err)
	}
	m2, _ = m.Update(res)
	m = m2.(*model)
	if !strings.Contains(m.flash, "saved") {
		t.Errorf("flash = %q, want a 'saved …' message", m.flash)
	}
	if !strings.Contains(m.renderFooter(), "saved") {
		t.Errorf("footer should show the flash: %q", m.renderFooter())
	}

	// The written file exists and holds the row.
	got, _ := os.ReadFile(res.path)
	if !strings.Contains(string(got), "SAVED-ROW") {
		t.Errorf("file content = %q", string(got))
	}

	// Any next key clears the flash.
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = m2.(*model)
	if m.flash != "" {
		t.Errorf("flash should clear on next key, got %q", m.flash)
	}
}

func TestSaveResultErrorFlashes(t *testing.T) {
	m := newModel(100)
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = m2.(*model)
	m2, _ = m.Update(saveResultMsg{err: errors.New("disk full")})
	m = m2.(*model)
	if !strings.Contains(m.flash, "save failed:") || !strings.Contains(m.flash, "disk full") {
		t.Errorf("flash = %q, want a 'save failed: disk full' message", m.flash)
	}
	if !strings.Contains(m.renderFooter(), "save failed:") {
		t.Errorf("footer should show the error flash: %q", m.renderFooter())
	}
}

func TestSnapshotSelection(t *testing.T) {
	m := seedSearch(t, "line one", "line two", "line three")
	m.reconcile()
	// Select rows 0..1 (anchor 0, cursor 1).
	m.visualMode = true
	m.setVisualAnchorRow(0)
	m.setVisualCursorRow(1)

	got := m.snapshotSelection()
	want := []string{
		"[g] a.log: line one",
		"[g] a.log: line two",
	}
	if len(got) != len(want) {
		t.Fatalf("want %d lines, got %d: %#v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d: want %q, got %q", i, want[i], got[i])
		}
	}
}

func TestSaveViewportKeyWritesFile(t *testing.T) {
	m := newModel(100)
	m.saveDir = t.TempDir()
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = m2.(*model)
	m.appendEvent(render.Event{Group: "g", File: "/x/a.log",
		Rendered: []render.Part{{Type: "text", Value: "VIEWPORT-ROW"}}})

	m2, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = m2.(*model)
	if cmd == nil {
		t.Fatal("s (save viewport) should return a tea.Cmd")
	}
	res, ok := cmd().(saveResultMsg)
	if !ok {
		t.Fatal("cmd should produce a saveResultMsg")
	}
	if res.err != nil {
		t.Fatalf("save failed: %v", res.err)
	}
	got, _ := os.ReadFile(res.path)
	if !strings.Contains(string(got), "VIEWPORT-ROW") {
		t.Errorf("viewport file content = %q", string(got))
	}
}
