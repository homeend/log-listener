package tui

import "testing"

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
