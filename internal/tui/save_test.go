package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
