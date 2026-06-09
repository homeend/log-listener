package tui

import (
	"strings"
	"testing"
)

func TestTruncateMiddleFitsUnchanged(t *testing.T) {
	if got := truncateMiddle("short.log", 16); got != "short.log" {
		t.Fatalf("want unchanged, got %q", got)
	}
	// Exactly at the limit is unchanged.
	if got := truncateMiddle("sixteen-chars.lg", 16); got != "sixteen-chars.lg" {
		t.Fatalf("want unchanged at limit, got %q", got)
	}
}

func TestTruncateMiddleLongASCII(t *testing.T) {
	// "application-server.log" is 22 cols; maxCols 16 -> avail 13, left 7, right 6.
	if got := truncateMiddle("application-server.log", 16); got != "applica...er.log" {
		t.Fatalf("got %q", got)
	}
}

func TestTruncateMiddleNeverExceedsWidth(t *testing.T) {
	// Wide (CJK) runes count as 2 cols; result must never overflow maxCols.
	s := "アプリケーションサーバ.log" // mix of wide runes + ASCII
	got := truncateMiddle(s, 16)
	if dispWidth(got) > 16 {
		t.Fatalf("overflow: %q has width %d", got, dispWidth(got))
	}
}

func TestTruncateMiddleDegenerate(t *testing.T) {
	if got := truncateMiddle("anything", 0); got != "" {
		t.Fatalf("maxCols 0 want empty, got %q", got)
	}
	// maxCols <= 3: no room for "..." plus content -> hard prefix, no ellipsis.
	if got := truncateMiddle("anything", 3); got != "any" {
		t.Fatalf("maxCols 3 want %q, got %q", "any", got)
	}
}

func TestWrapLineSingleRowWhenFits(t *testing.T) {
	got := wrapLine("hello", 5, 10)
	if len(got) != 1 {
		t.Fatalf("want 1 row, got %d: %q", len(got), got)
	}
	if stripANSI(got[0]) != "hello     " { // padded to width 10
		t.Fatalf("want padded to width, got %q", stripANSI(got[0]))
	}
}

func TestWrapLineSplitsOverflow(t *testing.T) {
	// 12 visible cols into width 5 => ceil(12/5) = 3 rows.
	line := "abcdefghijkl"
	got := wrapLine(line, 12, 5)
	if len(got) != 3 {
		t.Fatalf("want 3 rows, got %d", len(got))
	}
	for i, r := range got {
		if w := dispWidth(stripANSI(r)); w != 5 {
			t.Fatalf("row %d width = %d, want 5 (%q)", i, w, r)
		}
	}
	if joined := stripANSI(got[0]) + stripANSI(got[1]) + stripANSI(got[2]); joined != "abcdefghijkl   " {
		t.Fatalf("rejoined rows lost content: %q", joined)
	}
}

func TestWrapLineAlwaysAtLeastOneRow(t *testing.T) {
	if got := wrapLine("", 0, 10); len(got) != 1 {
		t.Fatalf("empty line should still occupy 1 row, got %d", len(got))
	}
}

// A styled span (e.g. the search highlight) that crosses a wrap boundary keeps
// its color on the continuation row: clipANSIWindow re-emits escapes preceding
// the skip offset, so wrapLine preserves styling across rows.
func TestWrapLinePreservesStyleAcrossBoundary(t *testing.T) {
	line := "\x1b[31mabcde\x1b[0m" // 5 red cols
	got := wrapLine(line, 5, 3)
	if len(got) != 2 {
		t.Fatalf("want 2 rows, got %d", len(got))
	}
	if stripANSI(got[0]) != "abc" || stripANSI(got[1]) != "de " {
		t.Fatalf("content lost across boundary: %q / %q", stripANSI(got[0]), stripANSI(got[1]))
	}
	for i, r := range got {
		if !strings.Contains(r, "\x1b[31m") {
			t.Fatalf("row %d lost its color escape: %q", i, r)
		}
	}
}
