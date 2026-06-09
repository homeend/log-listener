package tui

import (
	"regexp"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

// ansiRE matches CSI / OSC escape sequences emitted by lipgloss. Used both
// to strip styling (stripANSI) and to walk it while preserving it during
// horizontal-scroll slicing (clipANSIWindow).
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

func runeLen(s string) int { return utf8.RuneCountInString(s) }

// dispWidth is the terminal cell width of s — wide (CJK) runes count as 2,
// zero-width/combining as 0 — with any ANSI stripped first. Width/clip math
// must use this, not runeLen: a rune is not always one column, and counting it
// as one makes a row of wide characters overflow and wrap.
func dispWidth(s string) int { return runewidth.StringWidth(stripANSI(s)) }

// runeWidth is the cell width of a single rune (0, 1, or 2). A table lookup —
// allocation-free, so it's cheap on the per-rune clip hot path.
func runeWidth(r rune) int { return runewidth.RuneWidth(r) }

// takeCols returns the longest prefix of s whose display width is <= n,
// never splitting a wide rune (so the result may be < n columns).
func takeCols(s string, n int) string {
	w := 0
	for i, r := range s {
		rw := runeWidth(r)
		if w+rw > n {
			return s[:i]
		}
		w += rw
	}
	return s
}

// takeColsRight returns the longest suffix of s whose display width is <= n,
// never splitting a wide rune.
func takeColsRight(s string, n int) string {
	rs := []rune(s)
	w := 0
	for i := len(rs) - 1; i >= 0; i-- {
		rw := runeWidth(rs[i])
		if w+rw > n {
			return string(rs[i+1:])
		}
		w += rw
	}
	return s
}

// truncateMiddle shortens s to at most maxCols display columns by replacing the
// middle with "...", measured with go-runewidth so wide/CJK names never
// overflow. s is returned unchanged if it already fits. Degenerate cases:
// maxCols <= 0 -> ""; maxCols <= 3 (no room for "..." plus content) -> the
// first maxCols columns of s with no ellipsis.
func truncateMiddle(s string, maxCols int) string {
	if maxCols <= 0 {
		return ""
	}
	if dispWidth(s) <= maxCols {
		return s
	}
	if maxCols <= 3 {
		return takeCols(s, maxCols)
	}
	avail := maxCols - 3
	left := (avail + 1) / 2
	right := avail - left
	return takeCols(s, left) + "..." + takeColsRight(s, right)
}

// wrapLine splits a fully-styled terminal row of visible width visW into
// ceil(visW/width) rows of exactly `width` display columns, reusing
// clipANSIWindow so ANSI styling, the search highlight, and wide-rune straddle
// are preserved across the wrap boundary. Always returns at least one row.
func wrapLine(line string, visW, width int) []string {
	if width <= 0 {
		return []string{line}
	}
	n := (visW + width - 1) / width
	if n < 1 {
		n = 1
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, clipANSIWindow(line, i*width, width))
	}
	return out
}
